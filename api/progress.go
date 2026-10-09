package api

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

// TaskProgressInfo stores the progress of an API-submitted task. All fields are
// guarded by mu. It implements taskevent.Sink so the task layer can update it
// without knowing about the API.
type TaskProgressInfo struct {
	mu              sync.Mutex
	TaskID          string
	Type            string
	Status          TaskStatus
	Title           string
	TotalBytes      int64
	DownloadedBytes int64
	TotalFiles      int
	DownloadedFiles int
	Storage         string
	Path            string
	Error           string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	StartedAt       time.Time
	Webhook         string
	webhookNotified bool
	webhookContext  context.Context
	ResultPolicy    taskresult.Policy
	ResultSummary   *taskresult.Counts
}

// progressStore holds all API tasks. Entries are removed a fixed duration after
// they reach a terminal state to bound memory usage.
type progressStore struct {
	mu        sync.RWMutex
	tasks     map[string]*TaskProgressInfo
	retention time.Duration
}

var store = &progressStore{
	tasks:     make(map[string]*TaskProgressInfo),
	retention: 24 * time.Hour,
}

// RegisterTask registers a new API task and returns its progress info.
func RegisterTask(taskID, taskType, storage, path, title, webhook string) *TaskProgressInfo {
	info := &TaskProgressInfo{
		TaskID:    taskID,
		Type:      taskType,
		Status:    TaskStatusQueued,
		Title:     title,
		Storage:   storage,
		Path:      path,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Webhook:   webhook,
	}

	store.mu.Lock()
	store.tasks[taskID] = info
	store.mu.Unlock()

	return info
}

// GetTask returns the progress info for a task.
func GetTask(taskID string) (*TaskProgressInfo, bool) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	info, ok := store.tasks[taskID]
	return info, ok
}

// GetAllTasks returns all tracked tasks.
func GetAllTasks() []*TaskProgressInfo {
	store.mu.RLock()
	defer store.mu.RUnlock()

	tasks := make([]*TaskProgressInfo, 0, len(store.tasks))
	for _, info := range store.tasks {
		tasks = append(tasks, info)
	}
	return tasks
}

// DeleteTask removes a task record.
func DeleteTask(taskID string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	delete(store.tasks, taskID)
}

// CleanupExpired removes tasks that reached a terminal state more than the
// store's retention duration ago. It is safe to call periodically.
func CleanupExpired() {
	now := time.Now()
	store.mu.Lock()
	defer store.mu.Unlock()
	for id, info := range store.tasks {
		info.mu.Lock()
		terminal := info.Status == TaskStatusCompleted || info.Status == TaskStatusFailed || info.Status == TaskStatusCancelled
		stale := terminal && now.Sub(info.UpdatedAt) > store.retention
		info.mu.Unlock()
		if stale {
			delete(store.tasks, id)
		}
	}
}

// StartCleanupLoop runs CleanupExpired on a fixed interval until ctx is done.
// It should be started once during API server initialization.
func StartCleanupLoop(ctx interface{ Done() <-chan struct{} }) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				CleanupExpired()
			}
		}
	}()
}

// UpdateStatus sets the task status.
func (t *TaskProgressInfo) UpdateStatus(status TaskStatus) {
	t.mu.Lock()
	if t.Status.terminal() {
		t.mu.Unlock()
		return
	}
	t.Status = status
	t.UpdatedAt = time.Now()
	if status == TaskStatusRunning && t.StartedAt.IsZero() {
		t.StartedAt = t.UpdatedAt
	}
	payload := t.notificationLocked()
	ctx := t.webhookContext
	t.mu.Unlock()
	SendWebhook(ctx, payload)
}

// SetError marks the task failed with an error message.
func (t *TaskProgressInfo) SetError(err string) {
	t.mu.Lock()
	if t.Status.terminal() {
		t.mu.Unlock()
		return
	}
	t.Error = err
	t.Status = TaskStatusFailed
	t.UpdatedAt = time.Now()
	payload := t.notificationLocked()
	ctx := t.webhookContext
	t.mu.Unlock()
	SendWebhook(ctx, payload)
}

// snapshot returns a point-in-time copy of the fields needed to render a
// response, so callers never touch the mutex directly.
func (t *TaskProgressInfo) snapshot() (status TaskStatus, total, downloaded int64, totalFiles, downloadedFiles int, startedAt time.Time, err string, updatedAt time.Time) {
	s := t.responseSnapshot()
	return s.status, s.total, s.downloaded, s.totalFiles, s.downloadedFiles, s.startedAt, s.err, s.updatedAt
}

type taskProgressSnapshot struct {
	status                      TaskStatus
	total, downloaded           int64
	totalFiles, downloadedFiles int
	startedAt, updatedAt        time.Time
	err                         string
	resultPolicy                taskresult.Policy
	resultSummary               *taskresult.Counts
}

func (t *TaskProgressInfo) responseSnapshot() taskProgressSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	return taskProgressSnapshot{status: t.Status, total: t.TotalBytes, downloaded: t.DownloadedBytes,
		totalFiles: t.TotalFiles, downloadedFiles: t.DownloadedFiles, startedAt: t.StartedAt, err: t.Error,
		updatedAt: t.UpdatedAt, resultPolicy: t.ResultPolicy, resultSummary: cloneResultSummary(t.ResultSummary)}
}

// Emit implements taskevent.Sink. It translates task lifecycle events into
// status/progress updates and fires the webhook on terminal transitions.
func (t *TaskProgressInfo) Emit(e taskevent.Event) {
	t.mu.Lock()
	if t.Status.terminal() {
		// DELETE reports cancellation immediately, before Execute finishes. Allow
		// its final counts to enrich GET without changing the terminal decision
		// or sending a second webhook. Other late terminal events remain ignored.
		if t.Status == TaskStatusCancelled && e.Phase == taskevent.PhaseDone && t.ResultSummary == nil {
			if t.acceptResultSummaryLocked(e.ResultSummary) {
				t.UpdatedAt = time.Now()
			}
		}
		t.mu.Unlock()
		return
	}
	switch e.Phase {
	case taskevent.PhaseStart:
		t.Status = TaskStatusRunning
		if t.StartedAt.IsZero() {
			t.StartedAt = time.Now()
		}
		if e.TotalBytes > 0 {
			t.TotalBytes = e.TotalBytes
		}
	case taskevent.PhaseProgress:
		t.Status = TaskStatusRunning
		if e.TotalBytes > 0 {
			t.TotalBytes = e.TotalBytes
		}
		t.DownloadedBytes = e.DownloadedBytes
		if e.TotalFiles > 0 {
			t.TotalFiles = e.TotalFiles
		}
		if e.DownloadedFiles > 0 {
			t.DownloadedFiles = e.DownloadedFiles
		}
	case taskevent.PhaseDone:
		t.acceptResultSummaryLocked(e.ResultSummary)
		outcome := e.Outcome
		if outcome == taskresult.OutcomeUnknown {
			// Older producers have no task-context decision. Keep their existing
			// error-based contract; worker events always carry an explicit outcome.
			switch {
			case errors.Is(e.Err, context.Canceled):
				outcome = taskresult.OutcomeCancelled
			case e.Err != nil:
				outcome = taskresult.OutcomeFailed
			default:
				outcome = taskresult.OutcomeSuccess
			}
		}
		if outcome == taskresult.OutcomeCancelled {
			t.Status = TaskStatusCancelled
		} else if outcome == taskresult.OutcomeFailed {
			t.Status = TaskStatusFailed
			if e.Err != nil {
				t.Error = e.Err.Error()
			}
		} else {
			t.Status = TaskStatusCompleted
		}
	}
	t.UpdatedAt = time.Now()
	payload := t.notificationLocked()
	ctx := t.webhookContext
	t.mu.Unlock()
	SendWebhook(ctx, payload)
}

func (t *TaskProgressInfo) acceptResultSummaryLocked(summary *taskresult.Counts) bool {
	if t.ResultPolicy != "" && summary != nil && summary.Valid() {
		t.ResultSummary = cloneResultSummary(summary)
		return true
	}
	return false
}

func (s TaskStatus) terminal() bool {
	return s == TaskStatusCompleted || s == TaskStatusFailed || s == TaskStatusCancelled
}

// notificationLocked makes one notification decision using a consistent snapshot.
// Delivery may retry; receivers should deduplicate by task ID and terminal status.
func (t *TaskProgressInfo) notificationLocked() *WebhookPayload {
	if t.Webhook == "" || t.webhookNotified || !t.Status.terminal() {
		return nil
	}
	t.webhookNotified = true
	var err error
	if t.Error != "" {
		err = errors.New(t.Error)
	}
	payload := CreateWebhookPayload(t.TaskID, t.Type, t.Status, t.Storage, t.Path, err)
	payload.ResultPolicy = t.ResultPolicy
	payload.ResultSummary = cloneResultSummary(t.ResultSummary)
	return payload
}

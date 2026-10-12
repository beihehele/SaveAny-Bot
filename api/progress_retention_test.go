package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func retentionStore(limit int) *progressStore {
	return &progressStore{tasks: make(map[string]*TaskProgressInfo), terminalLimit: limit, retention: 24 * time.Hour}
}

func TestProgressRetentionAgeAndCount(t *testing.T) {
	now := time.Now()
	s := retentionStore(3)
	for id, state := range map[string]TaskStatus{
		"queued": TaskStatusQueued, "running": TaskStatusRunning,
		"expired": TaskStatusCompleted, "old": TaskStatusFailed,
		"tie-a": TaskStatusCancelled, "tie-b": TaskStatusFailed, "new": TaskStatusCompleted,
	} {
		updated := now.Add(-time.Hour)
		switch id {
		case "queued", "running", "expired":
			updated = now.Add(-24 * time.Hour)
		case "old":
			updated = now.Add(-2 * time.Hour)
		case "new":
			updated = now
		}
		s.tasks[id] = &TaskProgressInfo{TaskID: id, Status: state, UpdatedAt: updated}
	}
	s.prune(now)
	for _, id := range []string{"queued", "running", "tie-a", "tie-b", "new"} {
		if s.tasks[id] == nil {
			t.Errorf("lost retained task %s", id)
		}
	}
	for _, id := range []string{"expired", "old"} {
		if s.tasks[id] != nil {
			t.Errorf("retained stale/surplus task %s", id)
		}
	}
	s.terminalLimit = 2
	s.prune(now)
	if s.tasks["tie-a"] != nil || s.tasks["tie-b"] == nil {
		t.Fatal("timestamp ties are not evicted deterministically by ID")
	}
}

func TestProgressRetentionPrunesOnRegistrationAndTerminalEvents(t *testing.T) {
	for _, transition := range []string{"status", "error", "event"} {
		t.Run(transition, func(t *testing.T) {
			s := retentionStore(3)
			active := s.register("active", "tgfiles", "local", "", "active", "")
			active.Emit(taskevent.Event{Phase: taskevent.PhaseStart})
			for i := range 20 {
				info := s.register(fmt.Sprintf("done-%02d", i), "tgfiles", "local", "", "done", "")
				switch transition {
				case "status":
					info.UpdateStatus(TaskStatusCompleted)
				case "error":
					info.SetError("failed")
				case "event":
					info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
				}
				if len(s.tasks) != min(i+1, 3)+1 || s.tasks["active"] != active {
					t.Fatalf("history grew or lost active task: %d", len(s.tasks))
				}
			}
			for _, id := range []string{"done-17", "done-18", "done-19"} {
				if s.tasks[id] == nil {
					t.Errorf("missing newest record %s", id)
				}
			}
			// Registration also removes records that expire during an idle period.
			s.tasks["done-19"].UpdatedAt = time.Now().Add(-25 * time.Hour)
			s.register("next", "tgfiles", "local", "", "next", "")
			if s.tasks["done-19"] != nil || s.tasks["next"] == nil {
				t.Fatal("registration did not expire idle history")
			}
		})
	}
}

func TestProgressRetentionConcurrentTransitionsAndCleanup(t *testing.T) {
	s := retentionStore(10)
	active := s.register("active", "tgfiles", "local", "", "active", "")
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 40 {
				info := s.register(fmt.Sprintf("%d-%d", worker, i), "tgfiles", "local", "", "done", "")
				info.Emit(taskevent.Event{Phase: taskevent.PhaseStart})
				info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
				info.Emit(taskevent.Event{Phase: taskevent.PhaseProgress, DownloadedBytes: 99})
				s.prune(time.Now())
			}
		}()
	}
	wg.Wait()
	s.prune(time.Now())
	if len(s.tasks) != 11 || s.tasks["active"] != active {
		t.Fatalf("wrong concurrent retention: %d", len(s.tasks))
	}
}

func TestEvictedProgressStillDeliversWebhookAndRetry(t *testing.T) {
	received := make(chan WebhookPayload, 3)
	attempt := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		attempt++
		received <- payload
		if attempt == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	s := retentionStore(0)
	info := s.register(t.Name(), "tgfiles", "local", "", "done", server.URL)
	info.webhookContext = t.Context()
	info.SetError("original failure")
	if len(s.tasks) != 0 {
		t.Fatal("zero history budget did not evict completed record")
	}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
	info.UpdateStatus(TaskStatusCancelled)
	for range 2 {
		select {
		case payload := <-received:
			if payload.TaskID != t.Name() || payload.Status != TaskStatusFailed || payload.Error != "original failure" {
				t.Fatalf("eviction changed notification: %+v", payload)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("eviction suppressed webhook or retry")
		}
	}
	select {
	case extra := <-received:
		t.Fatalf("late event sent another notification: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

type retentionCancellationTask struct{ cancellationTask }

func (retentionCancellationTask) ResultSummary() taskresult.Summary {
	return taskresult.Summary{Total: 1, Cancelled: 1}
}

func TestProgressRetentionPreservesExecutingCancellation(t *testing.T) {
	const helperEnv = "SAVEANY_RETENTION_TEST_PROCESS"
	if os.Getenv(helperEnv) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestProgressRetentionPreservesExecutingCancellation$", "-test.count=1")
		command.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated executing retention: %v\n%s", err, output)
		}
		return
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	core.Prepare()
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	info := RegisterTask(t.Name(), "tgfiles", "local", "", "cancelled", "")
	defer DeleteTask(t.Name())
	store.terminalLimit = 0
	task := retentionCancellationTask{cancellationTask{id: t.Name(), run: func(taskCtx context.Context) error {
		close(started)
		<-release
		return taskCtx.Err()
	}}}
	if err := core.AddTask(taskevent.WithSink(ctx, info), task); err != nil {
		t.Fatal(err)
	}
	done := core.Run(ctx)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start")
	}
	if err := core.CancelTask(ctx, t.Name()); err != nil {
		t.Fatal(err)
	}
	info.UpdateStatus(TaskStatusCancelled)
	info.mu.Lock()
	info.UpdatedAt = time.Now().Add(-25 * time.Hour)
	info.mu.Unlock()
	CleanupExpired()
	if got, ok := GetTask(t.Name()); !ok || got != info {
		t.Fatal("evicted cancelled task while Execute was still active")
	}
	// Stop releases workers after the outstanding task leaves Execute.
	stop()
	release <- struct{}{}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	counts := taskresult.Counts{Total: 1, Cancelled: 1}
	if got := info.responseSnapshot(); got.status != TaskStatusCancelled || got.resultSummary == nil || *got.resultSummary != counts {
		t.Fatal("lost final worker cancellation counts")
	}
	CleanupExpired()
	if _, ok := GetTask(t.Name()); ok {
		t.Fatal("completed cancellation did not enter terminal history budget")
	}
}

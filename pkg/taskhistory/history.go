// Package taskhistory retains bounded, in-memory observations of queue tasks.
// It never schedules work, changes hooks, or replays tasks after restart.
package taskhistory

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

// TerminalLimit bounds retained terminal observations.
const TerminalLimit = 1000

// Retention bounds terminal age; active records survive this deadline.
const Retention = 24 * time.Hour

type sourceKey struct{}

// WithSource labels submissions without changing the task's lifetime.
func WithSource(ctx context.Context, source string) context.Context {
	return context.WithValue(ctx, sourceKey{}, source)
}

// Source returns the submission label, defaulting to Telegram Bot workflows.
func Source(ctx context.Context) string {
	if source, ok := ctx.Value(sourceKey{}).(string); ok && source != "" {
		return source
	}
	return "bot"
}

// Record is a detached task observation. ResultSummary is copied on read.
type Record struct {
	ID, Type, Title, Source, Status, Error string
	CreatedAt, UpdatedAt                   time.Time
	TotalBytes, DownloadedBytes            int64
	TotalFiles, DownloadedFiles            int
	ResultSummary                          *taskresult.Counts
	CopyProgress                           *taskevent.CopyProgress
	DeliveryProgress                       *taskevent.DeliveryProgress
	ResultLinks                            []string
}

type entry struct {
	store  *Store
	record Record
}

// Store retains active records and at most limit terminal records for retention.
// Active records are not evicted; their number follows the existing queue size.
type Store struct {
	mu        sync.Mutex
	records   map[string]*entry
	limit     int
	retention time.Duration
	now       func() time.Time
}

// NewStore creates a store with an explicit terminal retention budget.
func NewStore(limit int, retention time.Duration) *Store {
	return &Store{records: make(map[string]*entry), limit: max(0, limit), retention: retention, now: time.Now}
}

// Default is the instance-wide observation store.
var Default = NewStore(TerminalLimit, Retention)

// Delete removes a finished observation. Late events cannot recreate it.
func (s *Store) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e := s.records[id]; e != nil && terminal(e.record.Status) {
		delete(s.records, id)
	}
}

// CompleteUpload updates an existing observation after durable remote completion.
func (s *Store) CompleteUpload(id string, status, message string, counts taskresult.Counts) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if entry := s.records[id]; entry != nil {
		entry.record.Status, entry.record.Error = status, message
		entry.record.UpdatedAt = s.now()
		entry.record.ResultSummary = &counts
	}
	s.pruneLocked()
}

func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

// Track attaches a sink before admission. Rejected batches must call rollback.
// An existing ID is never overwritten or deleted by a duplicate submission.
func (s *Store) Track(ctx context.Context, id, typ, title string) (context.Context, func(), func(context.Context)) {
	s.mu.Lock()
	s.pruneLocked()
	if _, exists := s.records[id]; exists {
		s.mu.Unlock()
		return ctx, func() {}, func(context.Context) {}
	}
	now := s.now()
	e := &entry{store: s, record: Record{ID: id, Type: typ, Title: title, Source: Source(ctx), Status: "queued", CreatedAt: now, UpdatedAt: now}}
	s.records[id] = e
	s.mu.Unlock()
	rollback := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.records[id] == e {
			delete(s.records, id)
		}
	}
	watch := func(taskCtx context.Context) {
		context.AfterFunc(taskCtx, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			// A checked-out task's final outcome is decided by the worker. Cancelled
			// queued tasks can be dropped by Get without producing worker events.
			if s.records[id] == e && e.record.Status == "queued" {
				e.record.Status = "cancelled"
				e.record.UpdatedAt = s.now()
				s.pruneLocked()
			}
		})
	}
	return taskevent.WithSink(ctx, e), rollback, watch
}

func (e *entry) Emit(event taskevent.Event) {
	if event.Phase == taskevent.PhaseDone && event.Outcome == taskresult.OutcomeUnknown {
		return
	}
	s := e.store
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records[e.record.ID] != e || terminal(e.record.Status) {
		return
	}
	r := &e.record
	switch event.Phase {
	case taskevent.PhaseStart:
		r.Status = "running"
	case taskevent.PhaseProgress:
		r.Status = "running"
		if event.CopyProgress != nil {
			progress := *event.CopyProgress
			r.CopyProgress = &progress
		}
		if event.ResultSummary != nil && event.ResultSummary.Valid() {
			counts := *event.ResultSummary
			r.ResultSummary = &counts
		}
		if event.TotalBytes > 0 {
			r.TotalBytes = event.TotalBytes
		}
		r.DownloadedBytes = event.DownloadedBytes
		if event.TotalFiles > 0 {
			r.TotalFiles = event.TotalFiles
		}
		if event.DownloadedFiles > 0 {
			r.DownloadedFiles = event.DownloadedFiles
		}
	case taskevent.PhaseDone:
		switch event.Outcome {
		case taskresult.OutcomeCancelled:
			r.Status = "cancelled"
		case taskresult.OutcomeFailed:
			r.Status = "failed"
		case taskresult.OutcomeSuccess:
			r.Status = "completed"
		}
		if event.Err != nil {
			r.Error = event.Err.Error()
		}
		if event.ResultSummary != nil && event.ResultSummary.Valid() {
			counts := *event.ResultSummary
			r.ResultSummary = &counts
		}
	}
	if event.DeliveryProgress != nil {
		p := *event.DeliveryProgress
		r.DeliveryProgress = &p
	}
	r.ResultLinks = append(r.ResultLinks, event.ResultLinks...)

	r.UpdatedAt = s.now()
	if terminal(r.Status) {
		s.pruneLocked()
	}
}

func (s *Store) pruneLocked() {
	now := s.now()
	var done []*entry
	for id, e := range s.records {
		if !terminal(e.record.Status) {
			continue
		}
		if now.Sub(e.record.UpdatedAt) >= s.retention {
			delete(s.records, id)
			continue
		}
		done = append(done, e)
	}
	if len(done) <= s.limit {
		return
	}
	sort.Slice(done, func(i, j int) bool {
		if done[i].record.UpdatedAt.Equal(done[j].record.UpdatedAt) {
			return done[i].record.ID < done[j].record.ID
		}
		return done[i].record.UpdatedAt.Before(done[j].record.UpdatedAt)
	})
	for _, e := range done[:len(done)-s.limit] {
		delete(s.records, e.record.ID)
	}
}

// Snapshots returns current records and expires old terminal observations.
func (s *Store) Snapshots() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	out := make([]Record, 0, len(s.records))
	for _, e := range s.records {
		r := e.record
		if r.ResultSummary != nil {
			counts := *r.ResultSummary
			r.ResultSummary = &counts
		}
		if r.CopyProgress != nil {
			progress := *r.CopyProgress
			r.CopyProgress = &progress
		}
		if r.DeliveryProgress != nil {
			p := *r.DeliveryProgress
			r.DeliveryProgress = &p
		}
		r.ResultLinks = append([]string(nil), r.ResultLinks...)

		out = append(out, r)
	}
	return out
}

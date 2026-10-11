package taskhistory

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestHistoryRetentionIsolationAndDuplicateRollback(t *testing.T) {
	s := NewStore(2, time.Hour)
	now := time.Now()
	s.now = func() time.Time { return now }
	active, _, _ := s.Track(WithSource(t.Context(), "admin"), "active", "tgfiles", "active")
	for i := 0; i < 4; i++ {
		ctx, _, _ := s.Track(t.Context(), fmt.Sprint(i), "tgfiles", "done")
		taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess, ResultSummary: &taskresult.Counts{Total: 1, Succeeded: 1}})
		now = now.Add(time.Minute)
	}
	records := s.Snapshots()
	if len(records) != 3 {
		t.Fatalf("terminal budget failed: %+v", records)
	}
	_, rollback, _ := s.Track(t.Context(), "active", "copy", "duplicate")
	rollback()
	for _, r := range s.Snapshots() {
		if r.ID == "active" && (r.Source != "admin" || r.Status != "queued") {
			t.Fatal("duplicate replaced active record")
		}
		if r.ResultSummary != nil {
			r.ResultSummary.Succeeded = 100
		}
	}
	for _, r := range s.Snapshots() {
		if r.ResultSummary != nil && r.ResultSummary.Succeeded != 1 {
			t.Fatal("snapshot mutated store")
		}
	}
	now = now.Add(2 * time.Hour)
	if got := s.Snapshots(); len(got) != 1 || got[0].ID != "active" {
		t.Fatalf("expiry removed active work: %+v", got)
	}
	taskevent.Emit(active, taskevent.Event{Phase: taskevent.PhaseStart})
	taskevent.Emit(active, taskevent.Event{Phase: taskevent.PhaseDone, Err: errors.New("failure"), Outcome: taskresult.OutcomeFailed})
	taskevent.Emit(active, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeCancelled})
	if s.Snapshots()[0].Status != "failed" {
		t.Fatal("late event changed terminal outcome")
	}
}

func TestQueuedCancellationAndRunningDecision(t *testing.T) {
	for _, running := range []bool{false, true} {
		t.Run(fmt.Sprint(running), func(t *testing.T) {
			s := NewStore(10, time.Hour)
			parent, cancel := context.WithCancel(t.Context())
			ctx, rollback, watch := s.Track(parent, "task", "tgfiles", "task")
			watch(ctx)
			if running {
				taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseStart})
			}
			cancel()
			if running {
				taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
			}
			deadline := time.Now().Add(time.Second)
			for {
				r := s.Snapshots()[0]
				if r.Status == "cancelled" || r.Status == "completed" {
					if running && r.Status != "completed" {
						t.Fatal("cancel watcher overrode worker")
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("queued cancellation not recorded")
				}
				time.Sleep(time.Millisecond)
			}
			rollback()
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeFailed})
			if len(s.Snapshots()) != 0 {
				t.Fatal("rejected record reappeared")
			}
		})
	}
}

func TestConcurrentHistoryObservations(t *testing.T) {
	s := NewStore(20, time.Hour)
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Go(func() {
			ctx, _, _ := s.Track(t.Context(), fmt.Sprint(i), "tgfiles", "task")
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseStart})
			s.Snapshots()
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
			s.Snapshots()
		})
	}
	wg.Wait()
	if len(s.Snapshots()) != 20 {
		t.Fatal("concurrent completion exceeded terminal budget")
	}
}

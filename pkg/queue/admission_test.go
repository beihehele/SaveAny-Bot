package queue

import (
	"context"
	"testing"
)

func TestRejectedAddPreservesCallerOwnership(t *testing.T) {
	qe := NewTaskQueue[int]()
	t.Cleanup(qe.CloseAndCancel)
	accepted := NewTask(t.Context(), "duplicate", "accepted", 1)
	if err := qe.Add(accepted); err != nil {
		t.Fatal(err)
	}
	// Re-submitting the same pointer must not cancel the accepted task.
	if err := qe.Add(accepted); err == nil {
		t.Fatal("duplicate pointer accepted")
	}
	if accepted.Context().Err() != nil || qe.ActiveLength() != 1 {
		t.Fatal("rejecting a duplicate cancelled accepted work")
	}
	for _, state := range []string{"duplicate", "closed"} {
		t.Run(state, func(t *testing.T) {
			if state == "closed" {
				qe.Close()
			}
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			candidate := NewTask(parent, "duplicate", "rejected", 2)
			defer candidate.Cancel()
			if err := qe.Add(candidate); err == nil {
				t.Fatal("invalid submission accepted")
			}
			if candidate.Context().Err() != nil || parent.Err() != nil {
				t.Fatal("queue took ownership of rejected work")
			}
			candidate.Cancel()
			if parent.Err() != nil || accepted.Context().Err() != nil {
				t.Fatal("caller cleanup cancelled unrelated work")
			}
		})
	}
}

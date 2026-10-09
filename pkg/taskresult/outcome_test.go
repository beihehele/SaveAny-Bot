package taskresult

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestOutcomeUsesTaskContextAndPreservesNilSuccess(t *testing.T) {
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	deadline, stop := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer stop()
	internal := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	for _, tc := range []struct {
		name string
		ctx  context.Context
		err  error
		want Outcome
	}{
		{"active success", t.Context(), nil, OutcomeSuccess},
		{"internal cancellation is failure", t.Context(), internal, OutcomeFailed},
		{"ordinary failure", t.Context(), errors.New("EOF"), OutcomeFailed},
		{"task cancellation with ordinary error", cancelled, errors.New("EOF"), OutcomeCancelled},
		{"task cancellation with wrapped error", cancelled, internal, OutcomeCancelled},
		{"nil after cancellation preserves legacy success", cancelled, nil, OutcomeSuccess},
		{"deadline with internal cancellation is failure", deadline, internal, OutcomeFailed},
		{"deadline is failure", deadline, context.DeadlineExceeded, OutcomeFailed},
		{"nil after deadline preserves legacy success", deadline, nil, OutcomeSuccess},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyOutcome(tc.ctx, tc.err); got != tc.want {
				t.Fatalf("outcome=%v want=%v", got, tc.want)
			}
		})
	}
}

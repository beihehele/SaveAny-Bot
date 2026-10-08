package taskresult

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"
)

func TestCompletionPolicyPreservesErrorsAndDefaults(t *testing.T) {
	failed := errors.New("storage unavailable")
	for _, tc := range []struct {
		name      string
		policy    Policy
		counts    *Counts
		err, want error
		cancel    bool
	}{
		{name: "omitted keeps ignored failure", counts: &Counts{Total: 2, Succeeded: 1, Failed: 1}},
		{name: "explicit legacy keeps ignored failure", policy: Legacy, counts: &Counts{Total: 2, Failed: 2}},
		{name: "strict success", policy: Strict, counts: &Counts{Total: 2, Succeeded: 2}},
		{name: "strict partial", policy: Strict, counts: &Counts{Total: 2, Succeeded: 1, Failed: 1}, want: ErrIncomplete},
		{name: "strict all failed", policy: Strict, counts: &Counts{Total: 2, Failed: 2}, want: ErrIncomplete},
		{name: "strict pending", policy: Strict, counts: &Counts{Total: 2, Succeeded: 1, Pending: 1}, want: ErrIncomplete},
		{name: "strict running", policy: Strict, counts: &Counts{Total: 2, Succeeded: 1, Running: 1}, want: ErrIncomplete},
		{name: "strict interrupted", policy: Strict, counts: &Counts{Total: 2, Interrupted: 2}, want: ErrIncomplete},
		{name: "strict absent", policy: Strict, want: ErrInvalidSummary},
		{name: "strict invalid", policy: Strict, counts: &Counts{Total: 2, Succeeded: 3}, want: ErrInvalidSummary},
		{name: "strict original file error", policy: Strict, err: failed, want: failed},
		{name: "strict wrapped cancel", policy: Strict, err: fmt.Errorf("wrapped: %w", context.Canceled), want: context.Canceled},
		{name: "strict wrapped deadline", policy: Strict, err: fmt.Errorf("wrapped: %w", context.DeadlineExceeded), want: context.DeadlineExceeded},
		{name: "strict nil after cancellation", policy: Strict, counts: &Counts{Total: 2, Failed: 2}, cancel: true, want: context.Canceled},
		{name: "legacy nil after cancellation", policy: Legacy, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(WithPolicy(t.Context(), tc.policy))
			defer cancel()
			if tc.cancel {
				cancel()
			}
			got := CompletionError(ctx, tc.err, tc.counts)
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			if tc.err != nil && got != tc.err {
				t.Fatal("execution error identity changed")
			}
		})
	}
	ctx, cancel := context.WithDeadline(WithPolicy(t.Context(), Strict), time.Now().Add(-time.Second))
	defer cancel()
	if err := CompletionError(ctx, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline was replaced by a summary error")
	}
}

func TestCountsRejectNegativeInconsistentAndOverflowValues(t *testing.T) {
	for _, counts := range []Counts{
		{Total: -1}, {Total: 1, Failed: -1, Succeeded: 2}, {Total: 2, Succeeded: 1},
		{Total: math.MaxInt, Succeeded: math.MaxInt, Failed: math.MaxInt},
	} {
		if counts.Valid() {
			t.Fatalf("invalid counts accepted: %+v", counts)
		}
	}
	if !(Counts{}).Valid() || !(Counts{Total: 7, Pending: 1, Running: 1, Succeeded: 1, Failed: 1, Cancelled: 1, Interrupted: 2}).Valid() {
		t.Fatal("consistent counts rejected")
	}
}

package taskresult

import (
	"context"
	"errors"
)

// Outcome is the whole-task decision after applying its completion policy.
// It is independent of individual file states and preserves the original error.
type Outcome uint8

const (
	OutcomeUnknown Outcome = iota
	OutcomeSuccess
	OutcomeFailed
	OutcomeCancelled
)

// ClassifyOutcome distinguishes task cancellation from an internal operation
// returning context.Canceled. A nil return remains successful; strict policy
// callers must apply CompletionError first. Deadlines retain failure semantics.
func ClassifyOutcome(ctx context.Context, executionErr error) Outcome {
	if executionErr == nil {
		return OutcomeSuccess
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return OutcomeCancelled
	}
	return OutcomeFailed
}

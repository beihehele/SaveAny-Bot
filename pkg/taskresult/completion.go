package taskresult

import (
	"context"
	"errors"
	"fmt"
)

// Policy selects a completion contract independently of file scheduling.
type Policy string

const (
	Legacy Policy = "legacy"
	Strict Policy = "strict"
)

type policyKey struct{}

// WithPolicy opts into completion reporting. An empty policy preserves defaults.
func WithPolicy(ctx context.Context, policy Policy) context.Context {
	return context.WithValue(ctx, policyKey{}, policy)
}

// PolicyFromContext returns the explicitly requested policy, or an empty value.
func PolicyFromContext(ctx context.Context) Policy {
	policy, _ := ctx.Value(policyKey{}).(Policy)
	return policy
}

// Provider exposes submitted file results after Execute has returned.
type Provider interface {
	ResultSummary() Summary
}

// Counts is a bounded wire representation; it excludes names and error texts.
// Failed includes policy-skipped (unsaved) files to preserve the wire schema.
type Counts struct {
	Total       int `json:"total"`
	Pending     int `json:"pending"`
	Running     int `json:"running"`
	Succeeded   int `json:"succeeded"`
	Failed      int `json:"failed"`
	Cancelled   int `json:"cancelled"`
	Interrupted int `json:"interrupted"`
}

// Counts returns only the counts in this detached summary.
func (s Summary) Counts() Counts {
	return Counts{Total: s.Total, Pending: s.Pending, Running: s.Running, Succeeded: s.Succeeded,
		Failed: s.Failed + s.Skipped, Cancelled: s.Cancelled, Interrupted: s.Interrupted}
}

// Valid checks that nonnegative counts exactly cover the submitted inputs.
func (c Counts) Valid() bool {
	remaining := c.Total
	if remaining < 0 {
		return false
	}
	for _, count := range []int{c.Pending, c.Running, c.Succeeded, c.Failed, c.Cancelled, c.Interrupted} {
		if count < 0 || count > remaining {
			return false
		}
		remaining -= count
	}
	return remaining == 0
}

var (
	// ErrIncomplete means a strict task returned nil without saving all inputs.
	ErrIncomplete = errors.New("not all submitted files succeeded")
	// ErrInvalidSummary means strict completion has no consistent result counts.
	ErrInvalidSummary = errors.New("invalid or unavailable file result summary")
)

// CompletionError preserves execution errors. For strict nil returns, parent
// cancellation takes precedence over interpreting the submitted-file counts.
// It does not change task scheduling, trigger retries or roll back saved files.
func CompletionError(ctx context.Context, executionErr error, counts *Counts) error {
	if executionErr != nil || PolicyFromContext(ctx) != Strict {
		return executionErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if counts == nil || !counts.Valid() {
		return ErrInvalidSummary
	}
	if counts.Succeeded != counts.Total {
		return fmt.Errorf("%w: saved=%d total=%d failed=%d cancelled=%d interrupted=%d pending=%d running=%d",
			ErrIncomplete, counts.Succeeded, counts.Total, counts.Failed, counts.Cancelled,
			counts.Interrupted, counts.Pending, counts.Running)
	}
	return nil
}

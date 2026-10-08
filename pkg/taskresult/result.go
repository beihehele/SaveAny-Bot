// Package taskresult records file outcomes without changing execution policy.
package taskresult

import (
	"context"
	"errors"
	"sync"
)

// State describes a submitted element, not a file excluded before submission.
type State string

const (
	Pending     State = "pending"
	Running     State = "running"
	Succeeded   State = "succeeded"
	Failed      State = "failed"
	Cancelled   State = "cancelled"
	Interrupted State = "interrupted"
)

// Element is an immutable snapshot of one submitted file's outcome.
type Element struct {
	ID    string
	Name  string
	State State
	Error string
}

// Summary counts submitted elements. Pending and Running are incomplete;
// Interrupted means internal cancellation (e.g. a sibling failed), distinct
// from cancellation/deadline of the parent task. Bytes are tracked separately.
type Summary struct {
	Total, Pending, Running, Succeeded, Failed, Cancelled, Interrupted int
	Elements                                                           []Element
}

// Tracker records outcomes by input index, so even duplicate IDs stay distinct.
// Its zero value is ready to use. Reset must precede starting an execution;
// callers must join all writers before reusing a tracker for another execution.
type Tracker struct {
	mu       sync.RWMutex
	elements []Element
}

// Reset starts a new execution using an independent copy of its input metadata.
func (t *Tracker) Reset(elements []Element) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.elements = append([]Element(nil), elements...)
	for i := range t.elements {
		t.elements[i].State = Pending
		t.elements[i].Error = ""
	}
}

// Start marks an input as running. The index must belong to the Reset input.
func (t *Tracker) Start(index int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.elements[index].State = Running
	t.elements[index].Error = ""
}

// Finish records the operation result, preserving the original error as text.
// Parent cancellation only classifies cancellation-shaped errors; it does not
// reclassify an already-successful save or an ordinary file error.
func (t *Tracker) Finish(index int, err, parentErr error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	element := &t.elements[index]
	element.Error = ""
	switch {
	case err == nil:
		element.State = Succeeded
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		if parentErr != nil {
			element.State = Cancelled
		} else {
			element.State = Interrupted
		}
	default:
		element.State = Failed
	}
	if err != nil {
		element.Error = err.Error()
	}
}

// Snapshot returns consistent counts and detached, input-ordered file results.
func (t *Tracker) Snapshot() Summary {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := Summary{Total: len(t.elements), Elements: append([]Element(nil), t.elements...)}
	for _, element := range t.elements {
		switch element.State {
		case Pending:
			result.Pending++
		case Running:
			result.Running++
		case Succeeded:
			result.Succeeded++
		case Failed:
			result.Failed++
		case Cancelled:
			result.Cancelled++
		case Interrupted:
			result.Interrupted++
		}
	}
	return result
}

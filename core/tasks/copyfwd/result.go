package copyfwd

import (
	"context"
	"errors"

	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

// ResultSummary counts selected logical items: one message or one whole album.
// It retains only aggregate counts, never message IDs, captions or error texts.
// Pending items were not attempted; a successful call does not prove album
// completeness or completion of the best-effort caption attribution edit.
func (t *Task) ResultSummary() taskresult.Summary {
	t.resultMu.RLock()
	defer t.resultMu.RUnlock()
	return t.result
}

func (t *Task) resetResults(total int) {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	t.result = taskresult.Summary{Total: total, Pending: total}
}

func (t *Task) startResult() {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	t.result.Pending--
	t.result.Running++
}

func (t *Task) finishResult(err, parentErr error) {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	t.result.Running--
	switch {
	case err == nil:
		t.result.Succeeded++
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		if parentErr != nil {
			t.result.Cancelled++
		} else {
			t.result.Interrupted++
		}
	default:
		t.result.Failed++
	}
}

package taskevent

import (
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestResultCountsAreDetachedForEachSink(t *testing.T) {
	counts := taskresult.Counts{Total: 2, Succeeded: 1, Failed: 1}
	var retained *taskresult.Counts
	ctx := WithSink(t.Context(), SinkFunc(func(e Event) {
		e.ResultSummary.Failed = 0
		retained = e.ResultSummary
	}), SinkFunc(func(e Event) {
		if *e.ResultSummary != counts {
			t.Error("previous sink changed result counts")
		}
	}))
	Emit(ctx, Event{Phase: PhaseDone, ResultSummary: &counts})
	retained.Total = 0
	if counts.Total != 2 || counts.Failed != 1 {
		t.Fatal("sink mutated producer result")
	}
}

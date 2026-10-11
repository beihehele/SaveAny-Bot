package core

import (
	"context"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

type discardTask struct {
	lifecycleTask
	discarded bool
}

func (t *discardTask) Discard() { t.discarded = true }

func TestWorkerDiscardsCancelledCheckoutWithoutExecuting(t *testing.T) {
	lifecycleConfig(t, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx = taskevent.WithSink(ctx, taskevent.SinkFunc(func(e taskevent.Event) {
		if e.Phase == taskevent.PhaseStart {
			cancel()
		}
	}))
	task := &discardTask{lifecycleTask: lifecycleTask{id: "checkout-cancel", run: func(context.Context) error { t.Fatal("cancelled task executed"); return nil }}}
	qe := queue.NewTaskQueue[Executable]()
	if err := qe.Add(queue.NewTask[Executable](ctx, task.TaskID(), task.Title(), task)); err != nil {
		t.Fatal(err)
	}
	qe.Close()
	worker(t.Context(), qe)
	if !task.discarded {
		t.Fatal("cancelled checkout leaked resources without Execute")
	}
}

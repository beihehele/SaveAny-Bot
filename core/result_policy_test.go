package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type resultLifecycleTask struct {
	lifecycleTask
	summary taskresult.Summary
	reads   atomic.Int64
}

func (t *resultLifecycleTask) ResultSummary() taskresult.Summary {
	t.reads.Add(1)
	return t.summary
}

func TestWorkerResultPoliciesSelectHooksAndTerminalEvents(t *testing.T) {
	fileError := errors.New("storage unavailable")
	for _, tc := range []struct {
		name                        string
		policy                      taskresult.Policy
		summary                     taskresult.Summary
		err, want                   error
		hook                        string
		cancel, cancelBeforeExecute bool
	}{
		{name: "omitted retains success", summary: taskresult.Summary{Total: 2, Succeeded: 1, Failed: 1}, hook: "task_success"},
		{name: "legacy reports partial without changing hook", policy: taskresult.Legacy, summary: taskresult.Summary{Total: 2, Succeeded: 1, Failed: 1}, hook: "task_success"},
		{name: "strict partial fails", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Succeeded: 1, Failed: 1}, want: taskresult.ErrIncomplete, hook: "task_fail"},
		{name: "strict all failed", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Failed: 2}, want: taskresult.ErrIncomplete, hook: "task_fail"},
		{name: "strict all saved", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Succeeded: 2}, hook: "task_success"},
		{name: "strict preserves error", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Failed: 2}, err: fileError, want: fileError, hook: "task_fail"},
		{name: "strict preserves deadline", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Cancelled: 2}, err: context.DeadlineExceeded, want: context.DeadlineExceeded, hook: "task_fail"},
		{name: "strict nil after cancel", policy: taskresult.Strict, summary: taskresult.Summary{Total: 2, Succeeded: 2}, cancel: true, want: context.Canceled, hook: "task_cancel"},
		{name: "strict cancellation before execute has no summary", policy: taskresult.Strict, cancelBeforeExecute: true, want: context.Canceled, hook: "task_cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hookFile := filepath.Join(t.TempDir(), "hooks.txt")
			lifecycleConfig(t, hookFile)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := doneSink{done: make(chan taskevent.Event, 1)}
			ctx = taskevent.WithSink(taskresult.WithPolicy(ctx, tc.policy), sink, taskevent.SinkFunc(func(e taskevent.Event) {
				if tc.cancelBeforeExecute && e.Phase == taskevent.PhaseStart {
					cancel()
				}
			}))
			exe := &resultLifecycleTask{summary: tc.summary, lifecycleTask: lifecycleTask{id: "policy", run: func(context.Context) error {
				if tc.cancelBeforeExecute {
					t.Error("executed cancelled task")
				}
				if tc.cancel {
					cancel()
				}
				return tc.err
			}}}
			qe := queue.NewTaskQueue[Executable]()
			if err := qe.Add(queue.NewTask[Executable](ctx, exe.TaskID(), exe.Title(), exe)); err != nil {
				t.Fatal(err)
			}
			qe.Close()
			done := make(chan struct{})
			go func() { worker(t.Context(), qe); close(done) }()
			// This contract test runs two real shell hooks per task. Allow for
			// Windows process startup under a full-project race/build workload;
			// the shorter shutdown tests retain their existing deadline.
			select {
			case <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("workers did not finish task and shell hooks")
			}
			e := <-sink.done
			if !errors.Is(e.Err, tc.want) {
				t.Fatalf("event error=%v want=%v", e.Err, tc.want)
			}
			wantSummary := tc.policy != "" && !tc.cancelBeforeExecute
			if (e.ResultSummary != nil) != wantSummary {
				t.Fatalf("unexpected summary presence: %+v", e)
			}
			if wantSummary && *e.ResultSummary != tc.summary.Counts() {
				t.Fatal("terminal counts changed")
			}
			if !wantSummary && exe.reads.Load() != 0 {
				t.Fatal("default/skipped task performed a new result read")
			}
			b, err := os.ReadFile(hookFile)
			if err != nil {
				t.Fatal(err)
			}
			for _, hook := range []string{"task_success", "task_fail", "task_cancel"} {
				want := 0
				if hook == tc.hook {
					want = 1
				}
				if strings.Count(string(b), hook) != want {
					t.Fatalf("wrong hooks: %s", b)
				}
			}
			wantBefore := 1
			if tc.cancelBeforeExecute {
				wantBefore = 0
			}
			if strings.Count(string(b), "task_before_start") != wantBefore {
				t.Fatalf("wrong before-start count: %s", b)
			}
		})
	}
}

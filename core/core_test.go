package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

type lifecycleTask struct {
	id  string
	run func(context.Context) error
}

func (t lifecycleTask) TaskID() string                    { return t.id }
func (t lifecycleTask) Title() string                     { return t.id }
func (t lifecycleTask) Type() tasktype.TaskType           { return tasktype.TaskTypeTgfiles }
func (t lifecycleTask) Execute(ctx context.Context) error { return t.run(ctx) }

func resetCoreQueue(t *testing.T) {
	t.Helper()
	queueMu.Lock()
	old := queueInstance
	queueInstance = nil
	queueMu.Unlock()
	t.Cleanup(func() { queueMu.Lock(); queueInstance = old; queueMu.Unlock() })
}

func lifecycleConfig(t *testing.T, hookFile string) {
	t.Helper()
	text := "workers = 1\n"
	if hookFile != "" {
		text += "[hook.exec]\n"
		for _, hook := range []string{"task_before_start", "task_success", "task_fail", "task_cancel"} {
			text += hook + " = " + strconv.Quote(fmt.Sprintf("echo %s >> %q", hook, filepath.ToSlash(hookFile))) + "\n"
		}
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}

func TestQueuePreparationBeforeTaskEntry(t *testing.T) {
	resetCoreQueue(t)
	exe := lifecycleTask{id: "prepared", run: func(context.Context) error { return nil }}
	if err := AddTask(t.Context(), exe); err == nil {
		t.Fatal("accepted before queue initialization")
	}
	if err := CancelTask(t.Context(), "missing"); err == nil {
		t.Fatal("cancel before initialization succeeded")
	}
	if GetLength(t.Context()) != 0 || IsTaskExecuting("missing") {
		t.Fatal("uninitialized queue reported work")
	}
	Prepare()
	Prepare()
	if err := AddTask(t.Context(), exe); err != nil {
		t.Fatal(err)
	}
	if GetLength(t.Context()) != 1 || len(GetQueuedTasks(t.Context())) != 1 {
		t.Fatal("startup task not queued")
	}
	currentQueue().CloseAndCancel()
}

func TestIdleWorkersStopOnServiceCancellation(t *testing.T) {
	resetCoreQueue(t)
	lifecycleConfig(t, "")
	ctx, cancel := context.WithCancel(t.Context())
	done := Run(ctx)
	cancel()
	awaitWorkers(t, done)
}

func TestShutdownCancelsIndependentTasksAndKeepsCancelHook(t *testing.T) {
	resetCoreQueue(t)
	hookFile := filepath.Join(t.TempDir(), "hooks.txt")
	lifecycleConfig(t, hookFile)
	Prepare()
	started := make(chan struct{})
	exe := lifecycleTask{id: "running", run: func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }}
	if err := AddTask(context.Background(), exe); err != nil {
		t.Fatal(err)
	}
	if err := AddTask(context.Background(), lifecycleTask{id: "queued", run: func(context.Context) error { t.Error("executed queued task during abort"); return nil }}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	done := Run(ctx)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("task never started")
	}
	cancel()
	awaitWorkers(t, done)
	if IsTaskExecuting("running") || GetLength(t.Context()) != 0 {
		t.Fatal("shutdown left active tasks")
	}
	if err := AddTask(t.Context(), exe); err == nil {
		t.Fatal("accepted after shutdown")
	}
	b, err := os.ReadFile(hookFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(b), "task_before_start") != 1 || strings.Count(string(b), "task_cancel") != 1 || strings.Contains(string(b), "task_success") || strings.Contains(string(b), "task_fail") {
		t.Fatalf("incorrect hooks: %q", b)
	}
}

type doneSink struct{ done chan taskevent.Event }

func (s doneSink) Emit(e taskevent.Event) {
	if e.Phase == taskevent.PhaseDone {
		s.done <- e
	}
}

func TestWorkerSuccessAndFailureHooks(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			hookFile := filepath.Join(t.TempDir(), "hooks.txt")
			lifecycleConfig(t, hookFile)
			qe := queue.NewTaskQueue[Executable]()
			sink := doneSink{done: make(chan taskevent.Event, 1)}
			ctx := taskevent.WithSink(t.Context(), sink)
			exe := lifecycleTask{id: "hooks", run: func(context.Context) error {
				if fail {
					return errors.New("failed")
				}
				return nil
			}}
			if err := qe.Add(queue.NewTask[Executable](ctx, exe.id, exe.id, exe)); err != nil {
				t.Fatal(err)
			}
			stopped := make(chan struct{})
			go func() { worker(t.Context(), qe); close(stopped) }()
			select {
			case e := <-sink.done:
				if (e.Err != nil) != fail {
					t.Fatalf("error=%v", e.Err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing terminal event")
			}
			qe.Close()
			awaitWorkers(t, stopped)
			b, err := os.ReadFile(hookFile)
			if err != nil {
				t.Fatal(err)
			}
			want, absent := "task_success", "task_fail"
			if fail {
				want, absent = absent, want
			}
			if strings.Count(string(b), "task_before_start") != 1 || strings.Count(string(b), want) != 1 || strings.Contains(string(b), absent) {
				t.Fatalf("incorrect hooks: %q", b)
			}
		})
	}
}

func awaitWorkers(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("workers did not stop")
	}
}

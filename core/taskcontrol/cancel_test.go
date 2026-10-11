package taskcontrol

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
)

type heldCopy struct{ started, release chan struct{} }

func (heldCopy) TaskID() string          { return "running-copy" }
func (heldCopy) Title() string           { return "running-copy" }
func (heldCopy) Type() tasktype.TaskType { return tasktype.TaskTypeCopy }
func (task heldCopy) Execute(context.Context) error {
	defer copyfwd.End(91001, "running-copy")
	close(task.started)
	<-task.release
	return context.Canceled
}

// Own the global queue in a subprocess, with a real queued copy and a checked
// out copy whose RPC-equivalent operation takes time to acknowledge cancellation.
func TestCancelCopyOwnership(t *testing.T) {
	const helper = "SAVEANY_COPY_CANCEL_TEST_PROCESS"
	if os.Getenv(helper) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestCancelCopyOwnership$", "-test.count=1")
		command.Env = append(os.Environ(), helper+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated cancellation: %v\n%s", err, output)
		}
		return
	}
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte("workers=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	core.Prepare()
	running := heldCopy{make(chan struct{}), make(chan struct{})}
	if !copyfwd.TryBegin(91001, running.TaskID()) {
		t.Fatal("cannot acquire running slot")
	}
	if err := core.AddTask(ctx, running); err != nil {
		t.Fatal(err)
	}
	done := core.Run(ctx)
	t.Cleanup(func() {
		close(running.release)
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("worker did not exit")
		}
	})
	select {
	case <-running.started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	queued := copyfwd.NewTask("queued-copy", 91002, 100, 200, 0, "", 1, 0, 0, nil)
	if !copyfwd.TryBegin(91002, queued.TaskID()) {
		t.Fatal("cannot acquire queued slot")
	}
	if err := core.AddTask(ctx, queued); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := CancelTask(ctx, queued.TaskID()); err != nil {
			t.Fatal(err)
		}
	}
	if !copyfwd.TryBegin(91002, "next-queued-copy") {
		t.Fatal("queued cancellation leaked copy slot")
	}
	copyfwd.End(91002, "next-queued-copy")
	for range 2 {
		if err := CancelTask(ctx, running.TaskID()); err != nil {
			t.Fatal(err)
		}
	}
	if copyfwd.TryBegin(91001, "overlapping-copy") {
		t.Fatal("running cancellation released slot before execution ended")
	}
	if !copyfwd.TryBegin(91003, "unsubmitted-copy") {
		t.Fatal("cannot acquire unsubmitted slot")
	}
	defer copyfwd.End(91003, "unsubmitted-copy")
	if err := CancelTask(ctx, "unsubmitted-copy"); err == nil {
		t.Fatal("missing task reported success")
	}
	if copyfwd.TryBegin(91003, "unexpected-release") {
		t.Fatal("failed cancellation released unrelated ownership")
	}
}

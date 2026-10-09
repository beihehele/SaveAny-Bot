package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestWorkerKeepsFailureDecisionWhenCancelledDuringTerminalHook(t *testing.T) {
	dir := t.TempDir()
	ready, release := filepath.Join(dir, "ready"), filepath.Join(dir, "release")
	command := fmt.Sprintf("echo ready > %q; while [ ! -f %q ]; do sleep 0.01; done", filepath.ToSlash(ready), filepath.ToSlash(release))
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[hook.exec]\ntask_fail="+strconv.Quote(command)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	sink := doneSink{done: make(chan taskevent.Event, 1)}
	qe := queue.NewTaskQueue[Executable]()
	failure := errors.New("storage unavailable")
	task := lifecycleTask{id: "hook-race", run: func(context.Context) error { return failure }}
	if err := qe.Add(queue.NewTask[Executable](taskevent.WithSink(ctx, sink), task.id, task.id, task)); err != nil {
		t.Fatal(err)
	}
	qe.Close()
	done := make(chan struct{})
	go func() { worker(t.Context(), qe); close(done) }()
	// Release the real shell hook even if an assertion fails, and join its worker
	// before restoring config or deleting the fixture directory.
	t.Cleanup(func() {
		if err := os.WriteFile(release, nil, 0600); err != nil {
			t.Error(err)
		}
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("worker did not leave terminal hook")
		}
	})
	waitCtx, stop := context.WithTimeout(t.Context(), 20*time.Second)
	defer stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatal("failure hook did not start")
		}
	}
	cancel()
	if err := os.WriteFile(release, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-sink.done:
		if e.Err != failure || e.Outcome != taskresult.OutcomeFailed {
			t.Fatalf("late cancellation changed the selected failure: %+v", e)
		}
	case <-waitCtx.Done():
		t.Fatal("missing terminal event")
	}
}

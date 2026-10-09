package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestHookHelperProcess(t *testing.T) {
	mode := os.Getenv("SAVEANY_TEST_HOOK_MODE")
	if mode == "" {
		return
	}
	if mode == "child" {
		writeHookFixture(t, os.Getenv("SAVEANY_TEST_HOOK_CHILD_READY"), strconv.Itoa(os.Getpid()))
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	if mode == "tree" {
		child := exec.Command(os.Args[0], "-test.run=^TestHookHelperProcess$")
		child.Env = append(os.Environ(), "SAVEANY_TEST_HOOK_MODE=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		go child.Wait()
	}
	if ready := os.Getenv("SAVEANY_TEST_HOOK_READY"); ready != "" {
		writeHookFixture(t, ready, strconv.Itoa(os.Getpid()))
	}
	if release := os.Getenv("SAVEANY_TEST_HOOK_RELEASE"); release != "" {
		for limit := time.Now().Add(time.Minute); time.Now().Before(limit); {
			if _, err := os.Stat(release); err == nil {
				os.Exit(0)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func writeHookFixture(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func hookTestCommand() string {
	return fmt.Sprintf("%q -test.run=^TestHookHelperProcess$", filepath.ToSlash(os.Args[0]))
}

func hookBudgetConfig(t *testing.T, timeout time.Duration, hook string) {
	t.Helper()
	text := "workers=1\n[hook.exec]\ntimeout=" + strconv.Quote(timeout.String()) + "\n"
	if hook != "" {
		text += hook + "=" + strconv.Quote(hookTestCommand()) + "\n"
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	writeHookFixture(t, path, text)
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
}

func TestHookTimeoutPreservesCauseAndReleasesWorker(t *testing.T) {
	for _, hook := range []string{"task_before_start", "task_success", "task_fail", "task_cancel"} {
		t.Run(hook, func(t *testing.T) {
			t.Setenv("SAVEANY_TEST_HOOK_MODE", "wait")
			checkHookFixtureCleanup(t)
			hookBudgetConfig(t, time.Second, hook)
			qe := queue.NewTaskQueue[Executable]()
			parent, cancel := context.WithCancel(t.Context())
			defer cancel()
			sink := doneSink{done: make(chan taskevent.Event, 2)}
			failure := errors.New("storage failure")
			executed := 0
			first := lifecycleTask{id: "slow-hook", run: func(context.Context) error {
				executed++
				switch hook {
				case "task_fail":
					return failure
				case "task_cancel":
					cancel()
					return context.Canceled
				default:
					return nil
				}
			}}
			second := lifecycleTask{id: "next-task", run: func(context.Context) error { executed++; return nil }}
			for _, task := range []lifecycleTask{first, second} {
				ctx := taskevent.WithSink(t.Context(), sink)
				if task.id == first.id {
					ctx = taskevent.WithSink(parent, sink)
				}
				if err := qe.Add(queue.NewTask[Executable](ctx, task.id, task.id, task)); err != nil {
					t.Fatal(err)
				}
			}
			qe.Close()
			service, stop := context.WithTimeout(t.Context(), 10*time.Second)
			defer stop()
			stopped := make(chan struct{})
			go func() { worker(service, qe); close(stopped) }()
			t.Cleanup(func() {
				stop()
				qe.CloseAndCancel()
				select {
				case <-stopped:
				case <-time.After(20 * time.Second):
					t.Error("hook fixture worker did not stop")
				}
			})
			select {
			case <-stopped:
			case <-service.Done():
				t.Fatal("hook blocked the worker beyond its budget")
			}
			if executed != 2 || len(sink.done) != 2 || qe.IsExecuting(first.id) {
				t.Fatalf("hook prevented progress or terminal events: executions=%d events=%d", executed, len(sink.done))
			}
			event := <-sink.done
			wantOutcome, wantErr := taskresult.OutcomeSuccess, error(nil)
			switch hook {
			case "task_fail":
				wantOutcome, wantErr = taskresult.OutcomeFailed, failure
			case "task_cancel":
				wantOutcome, wantErr = taskresult.OutcomeCancelled, context.Canceled
			}
			if event.Outcome != wantOutcome || event.Err != wantErr {
				t.Fatalf("hook timeout changed the task result: %+v", event)
			}
		})
	}
	for _, callerWins := range []bool{false, true} {
		t.Run(fmt.Sprintf("caller_deadline_%t", callerWins), func(t *testing.T) {
			t.Setenv("SAVEANY_TEST_HOOK_MODE", "wait")
			checkHookFixtureCleanup(t)
			budget, parentLimit := 100*time.Millisecond, 5*time.Second
			if callerWins {
				budget, parentLimit = time.Minute, 100*time.Millisecond
			}
			hookBudgetConfig(t, budget, "")
			ctx, cancel := context.WithTimeout(t.Context(), parentLimit)
			defer cancel()
			if err := ExecCommandString(ctx, hookTestCommand()); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("hook lost deadline identity: %v", err)
			}
			if (ctx.Err() != nil) != callerWins {
				t.Fatalf("configured hook deadline affected its parent: callerWins=%t parent=%v", callerWins, ctx.Err())
			}
		})
	}
}

func TestHookCancellationStopsDescendant(t *testing.T) {
	dir := t.TempDir()
	ready, childReady := filepath.Join(dir, "ready"), filepath.Join(dir, "child-ready")
	for name, value := range map[string]string{
		"SAVEANY_TEST_HOOK_MODE": "tree", "SAVEANY_TEST_HOOK_READY": ready,
		"SAVEANY_TEST_HOOK_CHILD_READY": childReady,
	} {
		t.Setenv(name, value)
	}
	hookBudgetConfig(t, 30*time.Second, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var ownedProcesses []*os.Process
	// Always clean up test-owned children, including on an assertion failure.
	t.Cleanup(func() {
		if len(ownedProcesses) == 0 {
			ownedProcesses = findHookFixtureProcesses(ready, childReady)
		}
		cancel()
		cleanupHookFixtures(t, ownedProcesses)
	})
	result := make(chan error, 1)
	go func() { result <- ExecCommandString(ctx, hookTestCommand()) }()
	wait := time.NewTimer(10 * time.Second)
	defer wait.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(childReady); err == nil {
			if _, err := os.Stat(ready); err == nil {
				break
			}
		}
		select {
		case err := <-result:
			t.Fatalf("hook ended before spawning child: %v", err)
		case <-ticker.C:
		case <-wait.C:
			t.Fatal("hook descendant did not start")
		}
	}
	// Hold process handles while the test cancels/reaps the shell so Windows
	// cannot recycle a fixture PID before the cleanup callback uses it.
	ownedProcesses = findHookFixtureProcesses(ready, childReady)
	if len(ownedProcesses) != 2 {
		t.Fatal("could not track both hook fixture processes")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("hook lost cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook did not stop after cancellation")
	}
	assertHookFixturesExited(t, ownedProcesses)
}

func waitHookFixtureExit(process *os.Process) bool {
	limit := time.Now().Add(2 * time.Second)
	for {
		if hookFixtureExited(process) {
			return true
		}
		if time.Now().After(limit) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func assertHookFixturesExited(t *testing.T, processes []*os.Process) {
	t.Helper()
	for _, process := range processes {
		if !waitHookFixtureExit(process) {
			t.Errorf("hook fixture still running before cleanup: pid=%d", process.Pid)
		}
	}
}

func cleanupHookFixtures(t *testing.T, processes []*os.Process) {
	t.Helper()
	for _, process := range processes {
		if !hookFixtureExited(process) {
			if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("clean up hook fixture pid=%d: %v", process.Pid, err)
			}
			if !waitHookFixtureExit(process) {
				t.Errorf("hook fixture did not exit after cleanup: pid=%d", process.Pid)
			}
		}
		if err := process.Release(); err != nil {
			t.Errorf("release hook fixture handle: %v", err)
		}
	}
}

func checkHookFixtureCleanup(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		// A short deadline can expire before the helper writes its ready file.
		// Discover only the private executable belonging to this test invocation,
		// keeping process handles before inspecting or terminating any fixture.
		processes, err := findRunningHookFixtureProcesses()
		if err != nil {
			t.Errorf("find hook fixtures: %v", err)
		}
		assertHookFixturesExited(t, processes)
		cleanupHookFixtures(t, processes)
	})
}

func findHookFixtureProcesses(paths ...string) []*os.Process {
	var processes []*os.Process
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
		if err != nil || pid <= 0 {
			continue
		}
		if process, err := os.FindProcess(pid); err == nil {
			processes = append(processes, process)
		}
	}
	return processes
}

func TestTerminalHookKeepsShutdownGraceAndShorterBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	hookBudgetConfig(t, time.Minute, "")
	marker := filepath.Join(t.TempDir(), "shutdown-hook")
	command := fmt.Sprintf("echo terminal > %q", filepath.ToSlash(marker))
	if err := execTerminalHook(ctx, command); err != nil {
		t.Fatalf("shutdown skipped terminal hook: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "wait")
	checkHookFixtureCleanup(t)
	hookBudgetConfig(t, 100*time.Millisecond, "")
	if err := execTerminalHook(ctx, hookTestCommand()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown ignored shorter hook budget: %v", err)
	}
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("terminal hook mutated the service context")
	}
}

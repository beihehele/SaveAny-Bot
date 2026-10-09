package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsHookCompletionStopsBackgroundChild(t *testing.T) {
	dir := t.TempDir()
	ready, childReady, release := filepath.Join(dir, "ready"), filepath.Join(dir, "child-ready"), filepath.Join(dir, "release")
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "tree")
	t.Setenv("SAVEANY_TEST_HOOK_READY", ready)
	t.Setenv("SAVEANY_TEST_HOOK_CHILD_READY", childReady)
	t.Setenv("SAVEANY_TEST_HOOK_RELEASE", release)
	hookBudgetConfig(t, 30*time.Second, "")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	consumed := false
	var processes []*os.Process
	t.Cleanup(func() {
		cancel()
		if !consumed {
			select {
			case <-result:
			case <-time.After(5 * time.Second):
				t.Error("hook completion fixture did not finish")
			}
		}
		cleanupHookFixtures(t, processes)
	})
	go func() { result <- ExecCommandString(ctx, hookTestCommand()) }()
	for limit := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(childReady); err == nil {
			if _, err := os.Stat(ready); err == nil {
				processes = findHookFixtureProcesses(ready, childReady)
				break
			}
		}
		if time.Now().After(limit) {
			t.Fatal("hook background child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(processes) != 2 {
		t.Fatal("could not hold both hook process handles")
	}
	writeHookFixture(t, release, "exit parent")
	select {
	case err := <-result:
		consumed = true
		if err != nil {
			t.Fatalf("normal hook completion changed result: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook did not complete after releasing its parent")
	}
	assertHookFixturesExited(t, processes)
}

func TestWindowsHookCancellationKeepsSibling(t *testing.T) {
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "wait")
	sibling := exec.Command(os.Args[0], "-test.run=^TestHookHelperProcess$")
	if err := sibling.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !hookFixtureExited(sibling.Process) {
			if err := sibling.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Error(err)
			}
		}
		var exitErr *exec.ExitError
		if err := sibling.Wait(); err != nil && !errors.As(err, &exitErr) {
			t.Error(err)
		}
	})
	hookBudgetConfig(t, 100*time.Millisecond, "")
	if err := ExecCommandString(t.Context(), hookTestCommand()); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hook lost deadline: %v", err)
	}
	if hookFixtureExited(sibling.Process) {
		t.Fatal("hook cleanup stopped an unrelated sibling")
	}
	processes, err := findRunningHookFixtureProcesses()
	if err != nil {
		t.Error(err)
	}
	var fixtures []*os.Process
	t.Cleanup(func() { cleanupHookFixtures(t, fixtures) })
	for _, process := range processes {
		if process.Pid == sibling.Process.Pid {
			if err := process.Release(); err != nil {
				t.Error(err)
			}
		} else {
			fixtures = append(fixtures, process)
		}
	}
	assertHookFixturesExited(t, fixtures)
}

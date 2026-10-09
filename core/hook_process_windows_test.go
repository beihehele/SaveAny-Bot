package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func init() {
	if os.Getenv("SAVEANY_TEST_STALLED_TASKKILL") == "1" && strings.EqualFold(filepath.Base(os.Args[0]), "taskkill.exe") {
		if err := os.WriteFile(os.Getenv("SAVEANY_TEST_TASKKILL_CALLED"), []byte("called"), 0600); err != nil {
			os.Exit(2)
		}
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

func TestTerminalHookShutdownDoesNotRequireTaskkill(t *testing.T) {
	dir := t.TempDir()
	data, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskkill.exe"), data, 0700); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(dir, "taskkill-called")
	ready := filepath.Join(dir, "ready")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SAVEANY_TEST_STALLED_TASKKILL", "1")
	t.Setenv("SAVEANY_TEST_TASKKILL_CALLED", called)
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "wait")
	t.Setenv("SAVEANY_TEST_HOOK_READY", ready)
	hookBudgetConfig(t, time.Minute, "")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var process *os.Process
	result := make(chan error, 1)
	consumed := false
	t.Cleanup(func() {
		if !consumed {
			select {
			case <-result:
			case <-time.After(10 * time.Second):
				t.Error("hook fixture did not finish")
			}
		}
		// Retain cleanup even if a regression invokes the stalled external tool.
		if process == nil {
			if owned := findHookFixtureProcesses(ready); len(owned) == 1 {
				process = owned[0]
			}
		}
		if process != nil {
			if !hookFixtureExited(process) {
				if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
					t.Error(err)
				}
			}
			if err := process.Release(); err != nil {
				t.Error(err)
			}
		}
	})
	started := time.Now()
	go func() { result <- execTerminalHook(ctx, hookTestCommand()) }()
	for limit := time.Now().Add(3 * time.Second); process == nil && time.Now().Before(limit); {
		if data, err := os.ReadFile(ready); err == nil {
			if pid, err := strconv.Atoi(string(data)); err == nil && pid > 0 {
				process, err = os.FindProcess(pid)
				if err != nil {
					t.Fatal(err)
				}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if process == nil {
		t.Fatal("hook fixture did not start")
	}
	select {
	case err := <-result:
		consumed = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shutdown hook lost deadline: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stalled tree cleanup blocked shutdown")
	}
	if elapsed := time.Since(started); elapsed >= 7750*time.Millisecond {
		t.Fatalf("hook cleanup exceeds service shutdown allowance: %v", elapsed)
	}
	if _, err := os.Stat(called); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("native hook cleanup invoked taskkill: %v", err)
	}
	assertHookFixturesExited(t, []*os.Process{process})
}

func hookFixtureExited(process *os.Process) bool {
	// The test holds its original handle, preventing PID reuse during this query.
	handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(process.Pid))
	if err != nil {
		return false
	}
	status, err := syscall.WaitForSingleObject(handle, 0)
	closeErr := syscall.CloseHandle(handle)
	return err == nil && closeErr == nil && status == syscall.WAIT_OBJECT_0
}

func findRunningHookFixtureProcesses() (processes []*os.Process, err error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return nil, err
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(snapshot)) }()
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	nextErr := windows.Process32First(snapshot, &entry)
	for ; nextErr == nil; nextErr = windows.Process32Next(snapshot, &entry) {
		if int(entry.ProcessID) == os.Getpid() || !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) {
			continue
		}
		handle, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			if errors.Is(openErr, windows.ERROR_INVALID_PARAMETER) {
				continue // Process exited after the snapshot.
			}
			return processes, fmt.Errorf("inspect fixture candidate %d: %w", entry.ProcessID, openErr)
		}
		buffer := make([]uint16, 32768)
		length := uint32(len(buffer))
		queryErr := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &length)
		if queryErr == nil && strings.EqualFold(filepath.Clean(windows.UTF16ToString(buffer[:length])), executable) {
			// Keep the queried process handle until FindProcess has acquired its
			// own handle, preventing PID reuse between identity check and capture.
			process, findErr := os.FindProcess(int(entry.ProcessID))
			if findErr == nil {
				processes = append(processes, process)
			} else {
				queryErr = findErr
			}
		}
		if closeErr := windows.CloseHandle(handle); queryErr != nil || closeErr != nil {
			return processes, errors.Join(queryErr, closeErr)
		}
	}
	if !errors.Is(nextErr, windows.ERROR_NO_MORE_FILES) {
		return processes, nextErr
	}
	return processes, nil
}

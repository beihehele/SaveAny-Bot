package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"github.com/charmbracelet/log"
	"golang.org/x/sys/windows"
)

func hookCommand(ctx context.Context, command string) (*exec.Cmd, func() error, func() error) {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	// Suspend the shell until the job owns it, preventing children from escaping
	// during a short deadline. Preserve the configured cmd.exe shell quoting.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /D /S /C "` + command + `"`, HideWindow: true, CreationFlags: windows.CREATE_SUSPENDED}
	var job windows.Handle
	var mu sync.Mutex
	closeJob := func() error {
		mu.Lock()
		defer mu.Unlock()
		if job == 0 {
			return nil
		}
		if err := windows.CloseHandle(job); err != nil {
			return err
		}
		job = 0
		return nil
	}
	ready := make(chan struct{})
	var signalOnce sync.Once
	signalReady := func() { signalOnce.Do(func() { close(ready) }) }
	assigned := false
	cmd.Cancel = func() error {
		<-ready
		if err := closeJob(); err == nil && assigned {
			return nil
		} else if err != nil {
			log.FromContext(ctx).Warn("Failed to close hook job; stopping shell", "pid", cmd.Process.Pid, "error", err)
		}
		err := cmd.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			return nil
		}
		return err
	}
	start := func() error {
		defer signalReady()
		var err error
		job, err = windows.CreateJobObject(nil, nil)
		if err != nil {
			return fmt.Errorf("create hook job: %w", err)
		}
		limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
		limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
		// The Windows wrapper accepts uintptr, so keep the Go storage stable
		// across its wrapper and syscall, including a possible stack growth.
		var pinned runtime.Pinner
		pinned.Pin(&limits)
		defer pinned.Unpin()
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
			return fmt.Errorf("configure hook job: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		// No Wait has released os/exec's original handle yet, so this PID still
		// identifies our suspended shell even if another actor has stopped it.
		process, prepareErr := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if prepareErr == nil {
			prepareErr = windows.AssignProcessToJobObject(job, process)
			assigned = prepareErr == nil
			prepareErr = errors.Join(prepareErr, windows.CloseHandle(process))
		}
		if prepareErr == nil && ctx.Err() == nil {
			prepareErr = resumeWindowsHookShell(uint32(cmd.Process.Pid))
		}
		if prepareErr != nil {
			// Start succeeded, so always reap the shell on setup failure. Unblock
			// Cancel before Wait, which joins os/exec's context watcher.
			killErr := cmd.Process.Kill()
			if errors.Is(killErr, os.ErrProcessDone) {
				killErr = nil
			}
			signalReady()
			return errors.Join(fmt.Errorf("prepare hook shell: %w", prepareErr), killErr, cmd.Wait())
		}
		return nil
	}
	return cmd, start, closeJob
}

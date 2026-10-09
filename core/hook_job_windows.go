package core

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

func resumeWindowsHookShell(pid uint32) (err error) {
	// os/exec closes the primary thread handle after CreateProcess. The shell
	// remains suspended and has not run code that could create another thread.
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, windows.CloseHandle(snapshot)) }()
	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	nextErr := windows.Thread32First(snapshot, &entry)
	for ; nextErr == nil; nextErr = windows.Thread32Next(snapshot, &entry) {
		if entry.OwnerProcessID != pid {
			continue
		}
		thread, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, entry.ThreadID)
		if err != nil {
			return err
		}
		_, resumeErr := windows.ResumeThread(thread)
		return errors.Join(resumeErr, windows.CloseHandle(thread))
	}
	if !errors.Is(nextErr, windows.ERROR_NO_MORE_FILES) {
		return nextErr
	}
	return fmt.Errorf("hook shell %d has no suspended primary thread", pid)
}

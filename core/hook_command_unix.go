//go:build !windows

package core

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func hookCommand(ctx context.Context, command string) (*exec.Cmd, func() error, func() error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	// Hook scripts commonly wait for curl/sleep or another child. Killing only
	// the shell would leave those processes running after the worker moves on.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	return cmd, cmd.Start, func() error { return nil }
}

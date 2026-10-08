package core

import (
	"context"
	"os/exec"
	"syscall"
)

func hookCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe")
	// cmd.exe does not use CommandLineToArgvW. Preserve the configured shell
	// expression instead of letting os/exec escape its inner quotes as arguments.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /D /S /C "` + command + `"`}
	return cmd
}

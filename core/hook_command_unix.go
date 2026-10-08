//go:build !windows

package core

import (
	"context"
	"os/exec"
)

func hookCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}

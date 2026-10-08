package core

import (
	"context"
	"os"
)

func ExecCommandString(ctx context.Context, cmd string) error {
	if cmd == "" {
		return nil
	}
	execCmd := hookCommand(ctx, cmd)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	return execCmd.Run()
}

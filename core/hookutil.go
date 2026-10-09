package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/krau/SaveAny-Bot/config"
)

// ExecCommandString executes a hook within its configured budget. Hook errors
// are diagnostic; callers retain the task's already selected execution result.
func ExecCommandString(ctx context.Context, cmd string) error {
	if cmd == "" {
		return nil
	}
	timeout := config.C().Hook.Exec.Timeout
	if timeout <= 0 {
		timeout = config.DefaultHookExecTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCmd, start, release := hookCommand(ctx, cmd)
	execCmd.Stdout = os.Stdout
	execCmd.Stderr = os.Stderr
	execCmd.WaitDelay = 500 * time.Millisecond
	err := start()
	if err == nil {
		err = execCmd.Wait()
	}
	err = errors.Join(err, release())
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("hook execution stopped: %w", ctxErr)
	}
	return err
}

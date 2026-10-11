// Package taskcontrol coordinates task actions shared by Bot, API and admin.
package taskcontrol

import (
	"context"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
)

// CancelTask requests cancellation and releases a copy slot only if execution
// never started or has already ended. In-flight copies retain their slot until
// Execute returns, preventing another copy from overlapping Telegram RPCs.
func CancelTask(ctx context.Context, taskID string) error {
	if err := core.CancelTask(ctx, taskID); err != nil {
		return err
	}
	if !core.IsTaskExecuting(taskID) {
		copyfwd.EndByTaskID(taskID)
	}
	return nil
}

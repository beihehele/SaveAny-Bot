package core

import (
	"context"
	"errors"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
)

// CompleteShardReel runs the existing terminal hook only after the remote result.
func CompleteShardReel(ctx context.Context, group database.ShardReelTask) error {
	if group.HookDone {
		return nil
	}
	db := database.ShardReelDB(ctx)
	if db == nil {
		return errors.New("database is not ready")
	}
	var pending int64
	if err := db.Model(&database.ShardReelTask{}).
		Where("id = ? AND result_version = ? AND hook_done = ?", group.ID, group.ResultVersion, false).Count(&pending).Error; err != nil {
		return err
	}
	if pending == 0 {
		return nil
	}
	hook := config.C().Hook.Exec.TaskSuccess
	switch group.Status {
	case "failed":
		hook = config.C().Hook.Exec.TaskFail
	case "cancelled":
		hook = config.C().Hook.Exec.TaskCancel
	}
	if err := execTerminalHook(ctx, hook); err != nil {
		log.FromContext(ctx).Warn("ShardReel terminal hook failed", "task", group.ID, "error", err)
	}
	return db.Model(&database.ShardReelTask{}).Where("id = ? AND result_version = ?", group.ID, group.ResultVersion).Update("hook_done", true).Error
}

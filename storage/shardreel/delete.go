package shardreel

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/database"
)

// ErrTaskNotFinished prevents removal before execution and result delivery finish.
var ErrTaskNotFinished = errors.New("task is still active or delivering its result")

// DeleteTask removes terminal history only after local cleanup and remote ACK.
// A failed cleanup retains the records. Cancellation is durable so these files
// cannot be retried while their receipts or payloads are being removed.
func DeleteTask(ctx context.Context, id string, stores map[string]*Store) (bool, error) {
	db := database.ShardReelDB(ctx)
	if db == nil {
		return false, nil
	}
	tables, err := db.Migrator().GetTables()
	if err != nil {
		return false, err
	}
	if !slices.Contains(tables, "shard_reel_tasks") {
		return false, nil
	}
	return deleteTask(ctx, db, id, stores)
}

func deleteTask(ctx context.Context, db *gorm.DB, id string, stores map[string]*Store) (bool, error) {
	found := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Take the write lock before inspecting state, serializing with Retry.
		result := tx.Model(&database.ShardReelTask{}).Where("id = ? AND download_done = ? AND notified = ? AND status IN ?", id, true, true, []string{"completed", "failed", "cancelled"}).UpdateColumn("cancel_requested", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var count int64
			if err := tx.Model(&database.ShardReelTask{}).Where("id = ?", id).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return ErrTaskNotFinished
			}
			return nil
		}
		found = true
		return checkTerminalFiles(tx, id)
	})
	if err != nil || !found {
		return found, err
	}
	var files []database.ShardReelFile
	if err := db.WithContext(ctx).Where("task_id = ?", id).Find(&files).Error; err != nil {
		return true, err
	}
	for _, file := range files {
		if file.LocalClean && file.RemoteAcked {
			continue
		}
		store := stores[file.Storage]
		if store == nil {
			return true, fmt.Errorf("storage %q is unavailable for cleanup", file.Storage)
		}
		if err := store.deleteFile(ctx, &file); err != nil {
			return true, err
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// A concurrent cancellation must finish its result delivery first.
		result := tx.Model(&database.ShardReelTask{}).Where("id = ? AND notified = ? AND status IN ?", id, true, []string{"completed", "failed", "cancelled"}).UpdateColumn("cancel_requested", true)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTaskNotFinished
		}
		if err := checkTerminalFiles(tx, id); err != nil {
			return err
		}
		var retained int64
		if err := tx.Model(&database.ShardReelFile{}).Where("task_id = ? AND (local_clean = ? OR remote_acked = ?)", id, false, false).Count(&retained).Error; err != nil {
			return err
		}
		if retained != 0 {
			return errors.New("upload cleanup is not complete")
		}
		if err := tx.Where("task_id = ?", id).Delete(&database.ShardReelFile{}).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", id).Delete(&database.ShardReelTask{}).Error
	})
	return true, err
}

func checkTerminalFiles(tx *gorm.DB, id string) error {
	var active int64
	if err := tx.Model(&database.ShardReelFile{}).Where("task_id = ? AND status NOT IN ?", id, []string{"succeeded", "skipped", "failed", "cancelled"}).Count(&active).Error; err != nil {
		return err
	}
	if active != 0 {
		return ErrTaskNotFinished
	}
	return nil
}

func (s *Store) deleteFile(ctx context.Context, file *database.ShardReelFile) error {
	if file.Endpoint != s.cfg.URL || file.Spool != s.cfg.SpoolPath || !safeID.MatchString(file.ID) {
		return errors.New("storage configuration changed; cannot clean this upload")
	}
	var remote remoteResult
	if file.Status == "failed" && file.RemoteStarted && !file.RemoteAcked {
		if err := s.call(ctx, "DELETE", "/uploads/"+file.ID, nil, nil, &remote); err != nil {
			if !isTransferMissing(err) {
				return fmt.Errorf("cancel retained upload: %w", err)
			}
		} else if remote.Status != "succeeded" && remote.Status != "skipped" && remote.Status != "cancelled" {
			return errors.New("remote upload is still stopping; try deleting again shortly")
		}
	}
	// finish already cleans the payload; do not repeat its filesystem work.
	if remote.Status != "" {
		if err := s.finish(ctx, file, remote); err != nil {
			return err
		}
	} else if !file.LocalClean {
		if err := s.clean(ctx, file); err != nil {
			return err
		}
	}
	if !file.LocalClean {
		// clean records filesystem errors for maintenance instead of returning them.
		var current database.ShardReelFile
		if err := s.db.WithContext(ctx).First(&current, "id = ?", file.ID).Error; err != nil {
			return err
		}
		if !current.LocalClean {
			return fmt.Errorf("clean retained upload: %s", current.CleanupError)
		}
	}
	if file.RemoteStarted && !file.RemoteAcked {
		if err := s.call(ctx, "POST", "/uploads/"+file.ID+"/ack", nil, nil, nil); err != nil && !isTransferMissing(err) {
			return fmt.Errorf("acknowledge upload receipt: %w", err)
		}
		return s.update(ctx, file.ID, map[string]any{"remote_acked": true})
	}
	return nil
}

package shardreel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

var resultMu sync.Mutex

// FinishDownload transfers final responsibility to the durable upload lifecycle.
// Only a confirmed missing record returns false; read errors must not release
// a pending upload to ordinary completion before startup recovery.
func FinishDownload(ctx context.Context, id string, summary taskresult.Summary, downloadErr error) (bool, error) {
	db := database.ShardReelDB(ctx)
	if db == nil {
		return true, errors.New("database is not ready")
	}
	if !db.Migrator().HasTable(&database.ShardReelTask{}) {
		return true, errors.New("ShardReel task table is not ready")
	}
	var group database.ShardReelTask
	err := db.First(&group, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	var files []database.ShardReelFile
	if err := db.Where("task_id = ?", id).Find(&files).Error; err != nil {
		return true, err
	}
	if summary.Total == 0 {
		summary.Total = len(files)
		for _, file := range files {
			summary.Elements = append(summary.Elements, taskresult.Element{ID: file.ElementID, Name: file.Path, State: taskresult.Pending})
		}
	}
	if !group.CancelRequested {
		for index := range summary.Elements {
			if summary.Elements[index].State == taskresult.Cancelled {
				summary.Elements[index].State = taskresult.Interrupted
			}
		}
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		return true, err
	}
	message := ""
	if downloadErr != nil {
		message = strings.TrimSpace(strings.ReplaceAll(downloadErr.Error(), storagetypes.ErrSaveQueued.Error(), ""))
	}
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&group).Updates(map[string]any{"download_done": true, "summary": string(encoded), "error": message}).Error; err != nil {
			return err
		}
		// Read after taking the write lock, so a concurrent cancellation cannot
		// leave a partial download failed and outside the cancellation queue.
		var cancelled bool
		if err := tx.Model(&group).Where("id = ?", id).Select("cancel_requested").Scan(&cancelled).Error; err != nil {
			return err
		}
		status := "failed"
		fileError := "download did not produce a complete staged file"
		if cancelled {
			status = "cancelled"
			fileError = ""
		}
		return tx.Model(&database.ShardReelFile{}).Where("task_id = ? AND status = ?", id, "preparing").Updates(map[string]any{"status": status, "error": fileError}).Error
	})
	return true, err
}

// RequestCancel also works after the Telegram queue has handed a task off.
func RequestCancel(ctx context.Context, id string) (bool, error) {
	db := database.ShardReelDB(ctx)
	if db == nil || !db.Migrator().HasTable(&database.ShardReelTask{}) {
		return false, nil
	}
	found := false
	err := db.Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&database.ShardReelTask{}).Where("id = ? AND status NOT IN ?", id, []string{"completed", "cancelled"}).
			Updates(map[string]any{"cancel_requested": true, "status": "cancelling", "notified": false, "hook_done": false,
				"result_version": gorm.Expr("result_version + 1")})
		if result.Error != nil || result.RowsAffected == 0 {
			return result.Error
		}
		found = true
		return tx.Model(&database.ShardReelFile{}).Where("task_id = ? AND status IN ?", id, []string{"failed", "retry_wait", "queued", "processing"}).
			Updates(map[string]any{"status": "cancelling", "attempts": 0, "next_attempt_at": time.Time{}}).Error
	})
	return found, err
}

// Retry reuses intact files and their remote request IDs, leaving completed siblings alone.
func Retry(ctx context.Context, id string) error {
	db := database.ShardReelDB(ctx)
	if db == nil {
		return errors.New("database is not ready")
	}
	return retryTask(ctx, db, id)
}

func retryTask(ctx context.Context, db *gorm.DB, id string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Acquire the write lock before reading, so deletion cannot discard a
		// payload after this transaction has decided it can be retried.
		if err := tx.Model(&database.ShardReelTask{}).Where("id = ?", id).
			UpdateColumn("result_version", gorm.Expr("result_version")).Error; err != nil {
			return err
		}
		var group database.ShardReelTask
		if err := tx.First(&group, "id = ?", id).Error; err != nil {
			return err
		}
		if group.CancelRequested {
			return errors.New("cancelled tasks must be submitted again")
		}
		result := tx.Model(&database.ShardReelFile{}).Where("task_id = ? AND status IN ? AND sha256 <> ? AND local_clean = ?", id, []string{"failed", "retry_wait"}, "", false).
			Updates(map[string]any{"status": "queued", "attempts": 0, "next_attempt_at": time.Time{}, "error": ""})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return errors.New("no retained file can be retried; download the Telegram source again")
		}
		return tx.Model(&group).Updates(map[string]any{"status": "running", "error": "", "notified": false, "hook_done": false,
			"result_version": gorm.Expr("result_version + 1")}).Error
	})
}

// Summary reconstructs a group's outcomes without storing a second file history.
func Summary(group database.ShardReelTask, files []database.ShardReelFile) taskresult.Summary {
	var previous taskresult.Summary
	if group.Summary != "" {
		if err := json.Unmarshal([]byte(group.Summary), &previous); err != nil {
			previous = taskresult.Summary{}
		}
	}
	byID := map[string]database.ShardReelFile{}
	for _, file := range files {
		byID[file.ElementID] = file
	}
	if len(previous.Elements) == 0 {
		for _, file := range files {
			previous.Elements = append(previous.Elements, taskresult.Element{ID: file.ElementID, Name: file.Path, State: taskresult.Pending})
		}
		for len(previous.Elements) < group.ExpectedFiles {
			state := taskresult.Interrupted
			if !group.DownloadDone && group.Status == "running" {
				state = taskresult.Pending
			}
			previous.Elements = append(previous.Elements, taskresult.Element{ID: fmt.Sprint("missing_", len(previous.Elements)), State: state})
		}
	}
	for index := range previous.Elements {
		element := &previous.Elements[index]
		file, ok := byID[element.ID]
		if !ok {
			if group.CancelRequested && element.State != taskresult.Succeeded && element.State != taskresult.Skipped {
				element.State, element.Error = taskresult.Cancelled, ""
			}
			continue
		}
		element.Error = file.Error
		switch file.Status {
		case "succeeded":
			element.State = taskresult.Succeeded
		case "skipped":
			element.State = taskresult.Skipped
		case "cancelled":
			element.State = taskresult.Cancelled
		case "failed":
			element.State = taskresult.Failed
		default:
			element.State = taskresult.Pending
		}
	}
	result := taskresult.Summary{Total: len(previous.Elements), Elements: previous.Elements}
	for _, element := range result.Elements {
		switch element.State {
		case taskresult.Succeeded:
			result.Succeeded++
		case taskresult.Skipped:
			result.Skipped++
		case taskresult.Failed:
			result.Failed++
		case taskresult.Cancelled:
			result.Cancelled++
		case taskresult.Interrupted:
			result.Interrupted++
		case taskresult.Running:
			result.Running++
		default:
			result.Pending++
		}
	}
	return result
}

func reconcile(ctx context.Context, db *gorm.DB, notify func(context.Context, database.ShardReelTask) error) error {
	resultMu.Lock()
	defer resultMu.Unlock()
	var groups []database.ShardReelTask
	if err := db.WithContext(ctx).Where("download_done = ? AND notified = ?", true, false).Find(&groups).Error; err != nil {
		return err
	}
	for _, group := range groups {
		var files []database.ShardReelFile
		if err := db.WithContext(ctx).Where("task_id = ?", group.ID).Find(&files).Error; err != nil {
			return err
		}
		summary := Summary(group, files)
		if summary.Pending+summary.Running > 0 {
			continue
		}
		group.Status = "completed"
		if summary.Failed+summary.Interrupted > 0 {
			group.Status = "failed"
		}
		if summary.Cancelled > 0 && summary.Failed == 0 {
			group.Status = "cancelled"
		}
		if group.Status == "completed" || group.Status == "cancelled" {
			group.Error = ""
		}
		if group.Status == "failed" && group.Error == "" {
			group.Error = "some files were not saved"
			for _, file := range files {
				if file.Error != "" {
					group.Error = file.Error
					break
				}
			}
		}
		encoded, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		group.Summary = string(encoded)
		updated := db.WithContext(ctx).Model(&group).Where("result_version = ?", group.ResultVersion).
			Updates(map[string]any{"status": group.Status, "error": group.Error, "summary": group.Summary})
		if updated.Error != nil {
			return updated.Error
		}
		if updated.RowsAffected == 0 {
			continue
		}
		if notify != nil {
			if err := notify(ctx, group); err != nil {
				return err
			}
		}
		// Retry/cancel may happen while a hook or notification is running.
		// Its new result still needs delivery, even if it finishes similarly.
		if err := db.WithContext(ctx).Model(&group).Where("result_version = ?", group.ResultVersion).Update("notified", true).Error; err != nil {
			return err
		}
	}
	return nil
}

func pruneHistory(ctx context.Context, db *gorm.DB) error {
	var groups []database.ShardReelTask
	if err := db.WithContext(ctx).Where("notified = ? AND status IN ?", true, []string{"completed", "cancelled"}).Order("updated_at DESC").Find(&groups).Error; err != nil {
		return err
	}
	for index, group := range groups {
		if index < 1000 && time.Since(group.UpdatedAt) < 7*24*time.Hour {
			continue
		}
		var retained int64
		if err := db.WithContext(ctx).Model(&database.ShardReelFile{}).Where("task_id = ? AND (local_clean = ? OR remote_acked = ?)", group.ID, false, false).Count(&retained).Error; err != nil {
			return err
		}
		if retained != 0 {
			continue
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Where("task_id = ?", group.ID).Delete(&database.ShardReelFile{}).Error; err != nil {
				return err
			}
			return tx.Delete(&group).Error
		}); err != nil {
			return err
		}
	}
	return nil
}

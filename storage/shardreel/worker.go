package shardreel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/charmbracelet/log"
	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

var retryDelays = []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour}

// Run resumes this store's outbox. Shutdown leaves uploads intact for restart.
func (s *Store) Run(ctx context.Context, notify func(context.Context, database.ShardReelTask) error) {
	logger := log.FromContext(ctx)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	lastMaintenance := time.Time{}
	nextProbe := time.Time{}
	connected := false
	for ctx.Err() == nil {
		if time.Now().After(nextProbe) {
			_, probeErr := s.Check(ctx)
			connected = probeErr == nil
			nextProbe = time.Now().Add(30 * time.Second)
			if probeErr != nil && ctx.Err() == nil {
				logger.Warn("ShardReel unavailable; outbox retained", "storage", s.Name(), "error", probeErr)
			}
		}
		if err := s.step(ctx, connected); err != nil && ctx.Err() == nil {
			logger.Warn("ShardReel upload", "storage", s.Name(), "error", err)
		}
		if err := reconcile(ctx, s.db, notify); err != nil && ctx.Err() == nil {
			logger.Warn("ShardReel results", "error", err)
		}
		if time.Since(lastMaintenance) >= time.Hour {
			if err := s.maintenance(ctx); err != nil && ctx.Err() == nil {
				logger.Warn("ShardReel cleanup", "error", err)
			}
			lastMaintenance = time.Now()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Store) Recover(ctx context.Context) error {
	var rows []database.ShardReelFile
	if err := s.db.WithContext(ctx).Where("storage = ?", s.Name()).Find(&rows).Error; err != nil {
		return err
	}
	root, err := os.OpenRoot(s.cfg.SpoolPath)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, row := range rows {
		if row.Endpoint != s.cfg.URL || row.Spool != s.cfg.SpoolPath {
			if !terminalFile(row.Status) {
				if err := s.update(ctx, row.ID, map[string]any{"status": "failed", "error": "storage endpoint or spool changed; restore the original configuration"}); err != nil {
					return err
				}
			}
			continue
		}
		switch row.Status {
		case "preparing":
			if _, err := root.Stat(row.ID + "/payload"); err == nil {
				if err := s.recoverPayload(ctx, &row, root); !errors.Is(err, storagetypes.ErrSaveQueued) {
					if err := s.update(ctx, row.ID, map[string]any{"status": "failed", "error": err.Error()}); err != nil {
						return err
					}
				}
			} else if errors.Is(err, os.ErrNotExist) {
				cancelled, err := s.cancelled(ctx, row.TaskID)
				if err != nil {
					return err
				}
				status, message := "failed", "download interrupted; download the Telegram file again"
				if cancelled {
					status, message = "cancelled", ""
				}
				if err := s.update(ctx, row.ID, map[string]any{"status": status, "error": message}); err != nil {
					return err
				}
				if err := s.clean(ctx, &row); err != nil {
					return err
				}
			} else {
				if err := s.update(ctx, row.ID, map[string]any{"status": "failed", "error": "cannot inspect staged file"}); err != nil {
					return err
				}
			}
		case "uploading":
			if err := s.update(ctx, row.ID, map[string]any{"status": "queued"}); err != nil {
				return err
			}
		}
	}
	// Only a previous process can leave DownloadDone false at startup.
	return s.db.WithContext(ctx).Model(&database.ShardReelTask{}).Where("download_done = ?", false).
		Updates(map[string]any{"download_done": true, "error": "download phase interrupted; complete staged files will continue"}).Error
}

func (s *Store) update(ctx context.Context, id string, values map[string]any) error {
	return s.db.WithContext(ctx).Model(&database.ShardReelFile{}).Where("id = ?", id).Updates(values).Error
}

func terminalFile(status string) bool {
	return status == "succeeded" || status == "skipped" || status == "cancelled" || status == "failed"
}

func (s *Store) step(ctx context.Context, uploadAllowed bool) error {
	var row database.ShardReelFile
	query := s.db.WithContext(ctx).Where("storage = ? AND next_attempt_at <= ?", s.Name(), time.Now()).
		Where("status IN ? OR (status = ? AND local_clean = ? AND cleanup_error = ?)",
			[]string{"queued", "retry_wait", "processing", "cancelling"}, "cancelled", false, "")
	if !uploadAllowed {
		cancelledTasks := s.db.WithContext(ctx).Model(&database.ShardReelTask{}).Select("id").Where("cancel_requested = ?", true)
		query = query.Where("task_id IN (?)", cancelledTasks)
	}
	err := query.Order("created_at").First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.Endpoint != s.cfg.URL || row.Spool != s.cfg.SpoolPath {
		return s.update(ctx, row.ID, map[string]any{"status": "failed", "error": "storage configuration changed"})
	}
	if row.Status == "cancelled" {
		return s.clean(ctx, &row)
	}
	claimed := s.db.WithContext(ctx).Model(&database.ShardReelFile{}).Where("id = ? AND status = ?", row.ID, row.Status).Update("status", "uploading")
	if claimed.Error != nil {
		return claimed.Error
	}
	if claimed.RowsAffected != 1 {
		return nil
	}
	err = s.upload(ctx, &row)
	if err == nil {
		return nil
	}
	var current database.ShardReelFile
	if loadErr := s.db.WithContext(context.WithoutCancel(ctx)).First(&current, "id = ?", row.ID).Error; loadErr != nil {
		return loadErr
	}
	if current.Status == "succeeded" || current.Status == "skipped" || current.Status == "cancelled" {
		return s.update(context.WithoutCancel(ctx), row.ID, map[string]any{"cleanup_error": err.Error()})
	}
	if ctx.Err() != nil {
		status := row.Status
		if status != "processing" && status != "cancelling" {
			status = "queued"
		}
		return s.update(context.WithoutCancel(ctx), row.ID, map[string]any{"status": status})
	}
	if errors.Is(err, errObservation) {
		// An accepted remote job remains observable even after the retry budget
		// is spent. A temporary status-query failure must not abandon it.
		return s.update(ctx, row.ID, map[string]any{"status": row.Status, "error": err.Error(), "next_attempt_at": time.Now().Add(time.Minute)})
	}
	var remote *httpError
	permanent := errors.As(err, &remote) && remote.status >= 400 && remote.status < 500 && remote.status != 429
	if row.Attempts >= len(retryDelays) || permanent || errors.Is(err, errIntegrity) || errors.Is(err, errRetryStopped) {
		return s.update(ctx, row.ID, map[string]any{"status": "failed", "error": err.Error()})
	}
	next := time.Now().Add(retryDelays[row.Attempts])
	return s.update(ctx, row.ID, map[string]any{"status": "retry_wait", "attempts": row.Attempts + 1, "next_attempt_at": next, "error": err.Error()})
}

var errIntegrity = errors.New("staged file integrity check failed; upload stopped")
var errRetryStopped = errors.New("remote ingest cannot be retried automatically")
var errObservation = errors.New("remote status temporarily unavailable")

// observe handles receipts shared by GET, initialization, retry and completion.
func (s *Store) observe(ctx context.Context, row *database.ShardReelFile, remote remoteResult) (bool, error) {
	switch remote.Status {
	case "succeeded", "skipped", "cancelled":
		return true, s.finish(ctx, row, remote)
	case "processing", "cancelling":
		return true, s.update(ctx, row.ID, map[string]any{"status": remote.Status, "error": "", "next_attempt_at": time.Now().Add(2 * time.Second)})
	}
	return false, nil
}

func (s *Store) cancelled(ctx context.Context, taskID string) (bool, error) {
	var task database.ShardReelTask
	err := s.db.WithContext(ctx).Select("cancel_requested").First(&task, "id = ?", taskID).Error
	return task.CancelRequested, err
}

func (s *Store) upload(ctx context.Context, row *database.ShardReelFile) error {
	suffix := "/uploads/" + row.ID
	cancel, err := s.cancelled(ctx, row.TaskID)
	if err != nil {
		return err
	}
	var remote remoteResult
	if row.RemoteStarted {
		err := s.call(ctx, "GET", suffix, nil, nil, &remote)
		if err != nil && !isTransferMissing(err) {
			var failure *httpError
			if (row.Status == "processing" || row.Status == "cancelling") &&
				(!errors.As(err, &failure) || failure.status >= 500 || failure.status == 429) {
				return fmt.Errorf("%w: %w", errObservation, err)
			}
			return err
		}
	}
	if cancel {
		if row.RemoteStarted && remote.Status != "" {
			if err := s.call(ctx, "DELETE", suffix, nil, nil, &remote); err != nil {
				return err
			}
			if remote.Status == "succeeded" || remote.Status == "skipped" {
				return s.finish(ctx, row, remote)
			}
			if remote.Status != "cancelled" {
				return s.update(ctx, row.ID, map[string]any{"status": "cancelling", "next_attempt_at": time.Now().Add(2 * time.Second)})
			}
		}
		row.Status = "cancelled"
		if err := s.update(ctx, row.ID, map[string]any{"status": "cancelled", "error": "", "remote_acked": !row.RemoteStarted}); err != nil {
			return err
		}
		return s.clean(ctx, row)
	}
	if handled, err := s.observe(ctx, row, remote); handled {
		return err
	}
	if remote.Status == "failed" {
		if remote.ErrorCode == "FILE_CHECKSUM_MISMATCH" {
			return errIntegrity
		}
		if !remote.CanRetry || (row.Attempts >= len(retryDelays) && row.Status != "retry_wait") {
			return fmt.Errorf("%w (%s); inspect the remote task or download the file again", errRetryStopped, remote.ErrorCode)
		}
		if row.Status != "retry_wait" {
			return errors.New("remote ingest failed; retry scheduled")
		}
		// retry_wait already reserved this attempt and waited for its delay.
		// Persist observation before sending: a lost reply may still have
		// started remote work, including on the last permitted attempt.
		if err := s.update(ctx, row.ID, map[string]any{"status": "processing"}); err != nil {
			return err
		}
		row.Status = "processing"
		if err := s.call(ctx, "POST", suffix+"/retry", nil, nil, &remote); err != nil {
			var failure *httpError
			if !errors.As(err, &failure) || failure.status >= 500 || failure.status == 429 {
				return fmt.Errorf("%w: %w", errObservation, err)
			}
			return err
		}
		if handled, err := s.observe(ctx, row, remote); handled {
			return err
		}
		return errRetryStopped
	}
	root, err := os.OpenRoot(s.cfg.SpoolPath)
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(row.ID + "/payload")
	if err != nil {
		return errIntegrity
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, &contextReader{ctx, file})
	if err != nil {
		return err
	}
	if size != row.Size || hex.EncodeToString(hash.Sum(nil)) != row.SHA256 {
		return errIntegrity
	}
	if remote.Status == "" || remote.Status == "expired" {
		if err := s.update(ctx, row.ID, map[string]any{"remote_started": true}); err != nil {
			return err
		}
		row.RemoteStarted = true
		body := map[string]any{"request_id": row.ID, "path": row.Path, "size": row.Size, "sha256": row.SHA256, "on_conflict": row.Conflict}
		if err := s.call(ctx, "POST", "/uploads", body, nil, &remote); err != nil {
			return err
		}
		if remote.Status != "uploading" {
			// Close before observing a terminal receipt so Windows can remove it.
			if err := file.Close(); err != nil {
				return err
			}
			if handled, err := s.observe(ctx, row, remote); handled {
				return err
			}
			return errors.New("unexpected remote initialization state")
		}
	}
	if remote.Received < 0 || remote.Received > row.Size {
		return errIntegrity
	}
	if _, err := file.Seek(remote.Received, io.SeekStart); err != nil {
		return err
	}
	buffer := make([]byte, 4<<20)
	for remote.Received < row.Size {
		cancel, err := s.cancelled(ctx, row.TaskID)
		if err != nil {
			return err
		}
		if cancel {
			return s.update(ctx, row.ID, map[string]any{"status": "cancelling"})
		}
		length := min(int64(len(buffer)), row.Size-remote.Received)
		if _, err := io.ReadFull(file, buffer[:length]); err != nil {
			return errIntegrity
		}
		headers := map[string]string{"Content-Type": "application/octet-stream", "Content-Range": fmt.Sprintf("bytes %d-%d/%d", remote.Received, remote.Received+length-1, row.Size)}
		var ack struct {
			Received int64 `json:"received"`
		}
		if err := s.call(ctx, "PUT", suffix, buffer[:length], headers, &ack); err != nil {
			if isTransferStateConflict(err) {
				return s.update(ctx, row.ID, map[string]any{"status": "processing", "error": "", "next_attempt_at": time.Now().Add(2 * time.Second)})
			}
			return err
		}
		if ack.Received != remote.Received+length {
			return errors.New("remote upload offset changed; verify before retry")
		}
		remote.Received = ack.Received
		if err := s.update(ctx, row.ID, map[string]any{"uploaded_bytes": ack.Received}); err != nil {
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	cancel, err = s.cancelled(ctx, row.TaskID)
	if err != nil {
		return err
	}
	if cancel {
		return s.update(ctx, row.ID, map[string]any{"status": "cancelling"})
	}
	if err := s.call(ctx, "POST", suffix+"/complete", nil, nil, &remote); err != nil {
		if isTransferStateConflict(err) {
			return s.update(ctx, row.ID, map[string]any{"status": "processing", "error": "", "next_attempt_at": time.Now().Add(2 * time.Second)})
		}
		return err
	}
	if handled, err := s.observe(ctx, row, remote); handled {
		return err
	}
	return errors.New("unexpected remote completion state")
}

func (s *Store) finish(ctx context.Context, row *database.ShardReelFile, remote remoteResult) error {
	if remote.Status == "succeeded" && (remote.Size != row.Size || remote.SHA256 != row.SHA256) {
		return errIntegrity
	}
	if remote.Status != "succeeded" && remote.Status != "skipped" && remote.Status != "cancelled" {
		return errors.New("unexpected remote result")
	}
	row.Status = remote.Status
	values := map[string]any{"status": remote.Status, "saved_path": remote.Path, "error": "", "next_attempt_at": time.Time{}}
	if remote.Status == "succeeded" {
		values["uploaded_bytes"] = row.Size
	}
	if err := s.update(ctx, row.ID, values); err != nil {
		return err
	}
	// The durable successful result precedes deletion. Cleanup never reuploads.
	return s.clean(ctx, row)
}

func (s *Store) clean(ctx context.Context, row *database.ShardReelFile) error {
	if row.Endpoint != s.cfg.URL || row.Spool != s.cfg.SpoolPath || !safeID.MatchString(row.ID) {
		return errors.New("refusing cleanup outside the configured outbox")
	}
	root, err := os.OpenRoot(s.cfg.SpoolPath)
	if err != nil {
		return s.update(ctx, row.ID, map[string]any{"cleanup_error": err.Error()})
	}
	defer root.Close()
	for _, suffix := range []string{"/payload", "/payload.part", ""} {
		if err := root.Remove(row.ID + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.update(ctx, row.ID, map[string]any{"cleanup_error": err.Error()})
		}
	}
	values := map[string]any{"local_clean": true, "cleanup_error": ""}
	if !row.RemoteStarted {
		values["remote_acked"] = true
	}
	return s.update(ctx, row.ID, values)
}

func (s *Store) maintenance(ctx context.Context) error {
	var rows []database.ShardReelFile
	if err := s.db.WithContext(ctx).Where("storage = ? AND (status IN ? OR (status = ? AND sha256 = ?))", s.Name(), []string{"succeeded", "skipped", "cancelled"}, "failed", "").Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if !row.LocalClean {
			if err := s.clean(ctx, &row); err != nil {
				return err
			}
		}
		if row.RemoteStarted && !row.RemoteAcked {
			if err := s.call(ctx, "POST", "/uploads/"+row.ID+"/ack", nil, nil, nil); err != nil && !isTransferMissing(err) {
				continue
			}
			if err := s.update(ctx, row.ID, map[string]any{"remote_acked": true}); err != nil {
				return err
			}
		}
	}
	return pruneHistory(ctx, s.db)
}

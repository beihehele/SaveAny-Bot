package tfile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/common/utils/retryutil"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

func (t *Task) Execute(ctx context.Context) (err error) {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("file[%s]", t.File.Name()))
	meta := storagetypes.SaveRequestFrom(ctx)
	if meta.TaskID == "" {
		meta.TaskID = t.ID
		meta.Title = t.File.Name()
	}
	meta.FileID = t.ID
	if progress, ok := t.Progress.(interface{ UploadDestination() (int64, int) }); ok {
		meta.ChatID, meta.MessageID = progress.UploadDestination()
	}
	ctx = storagetypes.WithSaveRequest(ctx, meta)
	if _, ok := t.Storage.(storage.MessageStorage); ok {
		ctx = tgutil.WithDeliveryFeedback(ctx, t.ID, t.Progress)
	}
	if t.Progress != nil {
		t.Progress.OnStart(ctx, t)
	}
	if t.stream {
		return executeStream(ctx, t)
	}
	defer func() {
		if t.Progress != nil {
			t.Progress.OnDone(ctx, t, err)
		}
	}()
	if store, ok := t.Storage.(storage.MessageStorage); ok {
		file, ok := t.File.(tfile.TGFileMessage)
		if !ok {
			return fmt.Errorf("collection requires original messages")
		}
		links, saveErr := store.SaveMessages(ctx, []tfile.TGFileMessage{file})
		t.resultMu.Lock()
		t.links = links
		t.resultMu.Unlock()
		return saveErr
	}

	if capacity, ok := t.Storage.(interface {
		Reserve(context.Context, int64, string) error
	}); ok {
		if err := capacity.Reserve(ctx, t.File.Size(), t.Path); err != nil {
			return err
		}
	}

	logger.Info("Starting file download")
	// Allocate only after Execute starts: rejected/queued tasks own no files,
	// and Telegram filenames never determine cache paths.
	localFile, err := fsutil.CreateTempFile(t.cacheDir, "telegram-*")
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer func() {
		if err := localFile.CloseAndRemove(); err != nil {
			logger.Errorf("Failed to close local file: %v", err)
		}
	}()
	localPath := localFile.Name()
	wrAt := newWriterAt(ctx, localFile, t.Progress, t)
	_, err = tdler.NewDownloader(t.File).Parallel(ctx, wrAt)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	logger.Infof("File downloaded successfully")
	if path.Ext(t.File.Name()) == "" {
		ext := fsutil.DetectFileExt(localPath)
		if ext != "" {
			t.Path = t.Path + ext
		}
	}
	var fileStat os.FileInfo
	fileStat, err = os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to get file stat: %w", err)
	}
	vctx := context.WithValue(ctx, ctxkey.ContentLength, fileStat.Size())
	err = retryutil.RetrySave(vctx, func() error {
		file, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("failed to open cache file: %w", err)
		}
		defer file.Close()
		if err = t.Storage.Save(vctx, file, t.Path); err != nil {
			return fmt.Errorf("failed to save file: %w", err)
		}
		return nil
	}, retry.RetryTimes(uint(config.C().Retry)), retry.Context(vctx))
	if errors.Is(err, storagetypes.ErrSaveQueued) {
		return storagetypes.ErrSaveQueued
	}
	if err != nil {
		return fmt.Errorf("failed to save file after retries: %w", err)
	}
	return nil
}

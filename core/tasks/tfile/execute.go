package tfile

import (
	"context"
	"fmt"
	"os"
	"path"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/common/utils/retryutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
)

func (t *Task) Execute(ctx context.Context) (err error) {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("file[%s]", t.File.Name()))
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
	if err != nil {
		return fmt.Errorf("failed to save file after retries: %w", err)
	}
	return nil
}

package batchtfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/common/utils/ioutil"
	"github.com/krau/SaveAny-Bot/common/utils/retryutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"golang.org/x/sync/errgroup"
)

func (t *Task) Execute(ctx context.Context) error {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("batch_file[%s]", t.ID))
	logger.Info("Starting batch file task")
	t.results.Reset(t.resultElements())
	if t.Progress != nil {
		t.Progress.OnStart(ctx, t)
	}
	workers := config.C().Workers
	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(workers)
	for index, elem := range t.elems {
		eg.Go(func() (err error) {
			t.results.Start(index)
			var resultErr error
			defer func() {
				if resultErr == nil {
					resultErr = err
				}
				t.results.Finish(index, resultErr, ctx.Err())
			}()
			t.processingMu.Lock()
			if t.processing[elem.ID] != nil {
				t.processingMu.Unlock()
				return fmt.Errorf("element with ID %s is already being processed", elem.ID)
			}
			t.processing[elem.ID] = &elem
			t.processingMu.Unlock()
			defer func() {
				t.processingMu.Lock()
				delete(t.processing, elem.ID)
				t.processingMu.Unlock()
			}()
			err = t.processElement(gctx, elem)
			resultErr = err
			if errors.Is(err, storagetypes.ErrSaveSkipped) && ctx.Err() == nil {
				// A policy skip must not cancel the remaining album files. Keep
				// the skip for the deferred result while returning nil to errgroup.
				return nil
			}
			return err
		})
	}
	err := eg.Wait()
	summary := t.ResultSummary()
	logger.Info("Batch file outcomes", "total", summary.Total, "succeeded", summary.Succeeded,
		"skipped", summary.Skipped, "failed", summary.Failed, "cancelled", summary.Cancelled, "interrupted", summary.Interrupted,
		"pending", summary.Pending, "running", summary.Running)
	if err != nil {
		logger.Errorf("Error during batch file processing: %v", err)
	} else {
		logger.Info("Batch file task completed successfully")
	}
	if t.Progress != nil {
		t.Progress.OnDone(ctx, t, err)
	}
	return err
}

func (t *Task) processElement(ctx context.Context, elem TaskElement) error {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("file[%s]", elem.File.Name()))
	if elem.stream {
		pr, pw := io.Pipe()
		defer pr.Close()
		errg, uploadCtx := errgroup.WithContext(ctx)
		// Cancellation alone cannot interrupt a blocked Pipe.Read or Pipe.Write.
		stopClose := context.AfterFunc(uploadCtx, func() {
			pr.CloseWithError(uploadCtx.Err())
			pw.CloseWithError(uploadCtx.Err())
		})
		defer stopClose()
		errg.Go(func() error {
			err := elem.Storage.Save(uploadCtx, pr, elem.Path)
			// Release the downloader even when storage stops reading early.
			pr.CloseWithError(err)
			return err
		})
		wr := ioutil.NewProgressWriter(pw, func(n int) {
			downloaded := t.downloaded.Add(int64(n))
			if t.Progress != nil {
				t.Progress.OnProgress(ctx, t)
			}
			taskevent.Emit(ctx, taskevent.Event{
				TaskID:          t.ID,
				Phase:           taskevent.PhaseProgress,
				TotalBytes:      t.totalSize,
				DownloadedBytes: downloaded,
			})
		})
		errg.Go(func() error {
			defer pw.Close()
			logger.Info("Starting file download in stream mode")
			_, err := tdler.NewDownloader(elem.File).Stream(uploadCtx, wr)
			if err != nil {
				logger.Errorf("Failed to download file: %v", err)
				pw.CloseWithError(err)
			}
			return err
		})
		if err := errg.Wait(); err != nil {
			// Closing both pipe ends can report ErrClosedPipe; keep cancellation
			// recognizable so the queue runs TaskCancel rather than TaskFail.
			if ctx.Err() != nil && errors.Is(err, io.ErrClosedPipe) {
				err = ctx.Err()
			}
			return fmt.Errorf("failed to download file in stream mode: %w", err)
		}
		logger.Info("File downloaded successfully in stream mode")
		return nil
	}
	logger.Info("Starting file download")
	localFile, err := fsutil.CreateTempFile(elem.cacheDir, "telegram-*")
	if err != nil {
		return fmt.Errorf("failed to create local file: %w", err)
	}
	defer func() {
		if err := localFile.CloseAndRemove(); err != nil {
			logger.Errorf("Failed to close local file: %v", err)
		}
	}()
	localPath := localFile.Name()
	wrAt := ioutil.NewProgressWriterAt(localFile, func(n int) {
		downloaded := t.downloaded.Add(int64(n))
		if t.Progress != nil {
			t.Progress.OnProgress(ctx, t)
		}
		taskevent.Emit(ctx, taskevent.Event{
			TaskID:          t.ID,
			Phase:           taskevent.PhaseProgress,
			TotalBytes:      t.totalSize,
			DownloadedBytes: downloaded,
		})
	})
	_, err = tdler.NewDownloader(elem.File).Parallel(ctx, wrAt)
	if err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}
	logger.Info("File downloaded successfully")
	if path.Ext(elem.FileName()) == "" {
		ext := fsutil.DetectFileExt(localPath)
		if ext != "" {
			elem.Path = elem.Path + ext
		}
	}
	var fileStat os.FileInfo
	fileStat, err = os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to get file stat: %w", err)
	}
	vctx := context.WithValue(ctx, ctxkey.ContentLength, fileStat.Size())
	err = retryutil.RetrySave(vctx, func() error {
		var file *os.File
		file, err = os.Open(localPath)
		if err != nil {
			return fmt.Errorf("failed to open cache file: %w", err)
		}
		defer file.Close()
		if err = elem.Storage.Save(vctx, file, elem.Path); err != nil {
			logger.Errorf("Failed to save file: %s, retrying...", err)
			return err
		}
		return nil
	}, retry.Context(vctx), retry.RetryTimes(uint(config.C().Retry)))
	return err
}

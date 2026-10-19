package batchtfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sync/atomic"

	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/retry"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/common/utils/ioutil"
	"github.com/krau/SaveAny-Bot/common/utils/retryutil"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/storage"
	"golang.org/x/sync/errgroup"
)

func (t *Task) Execute(ctx context.Context) error {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("batch_file[%s]", t.ID))
	meta := storagetypes.SaveRequestFrom(ctx)
	if meta.TaskID == "" {
		meta.TaskID = t.ID
		meta.Title = t.Title()
	}
	meta.ExpectedFiles = len(t.elems)
	if progress, ok := t.Progress.(interface{ UploadDestination() (int64, int) }); ok {
		meta.ChatID, meta.MessageID = progress.UploadDestination()
	}
	ctx = storagetypes.WithSaveRequest(ctx, meta)
	for _, elem := range t.elems {
		if _, ok := elem.Storage.(storage.MessageStorage); ok {
			ctx = tgutil.WithDeliveryFeedback(ctx, t.ID, t.Progress)
			break
		}
	}
	logger.Info("Starting batch file task")
	t.results.Reset(t.resultElements())
	if t.Progress != nil {
		t.Progress.OnStart(ctx, t)
	}
	units, err := t.workGroups()
	if err != nil {
		if t.Progress != nil {
			t.Progress.OnDone(ctx, t, err)
		}
		return err
	}
	workers := config.C().Workers
	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(workers)
	var staged atomic.Bool
	for _, indices := range units {
		eg.Go(func() (err error) {
			for _, index := range indices {
				t.results.Start(index)
			}
			var resultErr error
			defer func() {
				if resultErr == nil {
					resultErr = err
				}
				for _, index := range indices {
					t.results.Finish(index, resultErr, ctx.Err())
				}
				if _, ok := t.elems[indices[0]].Storage.(storage.MessageStorage); ok {
					t.emitFileCounts(ctx)
				}
			}()
			t.processingMu.Lock()
			for _, index := range indices {
				if t.processing[t.elems[index].ID] != nil {
					t.processingMu.Unlock()
					return fmt.Errorf("element with ID %s is already being processed", t.elems[index].ID)
				}
			}
			for _, index := range indices {
				elem := t.elems[index]
				t.processing[elem.ID] = &elem
			}
			t.processingMu.Unlock()
			defer func() {
				t.processingMu.Lock()
				defer t.processingMu.Unlock()
				for _, index := range indices {
					delete(t.processing, t.elems[index].ID)
				}
			}()
			err = t.processGroup(gctx, indices)
			if errors.Is(err, storagetypes.ErrSaveQueued) {
				staged.Store(true)
			}
			resultErr = err
			if (errors.Is(err, storagetypes.ErrSaveSkipped) || errors.Is(err, storagetypes.ErrSaveQueued)) && ctx.Err() == nil {
				// Skipping or staging one member must not cancel its siblings.
				return nil
			}
			return err
		})
	}
	err = eg.Wait()
	summary := t.ResultSummary()
	logger.Info("Batch file outcomes", "total", summary.Total, "succeeded", summary.Succeeded,
		"skipped", summary.Skipped, "failed", summary.Failed, "cancelled", summary.Cancelled, "interrupted", summary.Interrupted,
		"pending", summary.Pending, "running", summary.Running)
	if errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled) {
		logger.Info("Batch file task cancelled")
	} else if err != nil {
		logger.Errorf("Error during batch file processing: %v", err)
	} else if staged.Load() {
		logger.Info("Batch files staged for ShardReel upload")
	} else {
		logger.Info("Batch file task completed successfully")
	}
	if staged.Load() {
		err = errors.Join(err, storagetypes.ErrSaveQueued)
	}
	if t.Progress != nil {
		t.Progress.OnDone(ctx, t, err)
	}
	return err
}

func (t *Task) processElement(ctx context.Context, elem TaskElement) error {
	meta := storagetypes.SaveRequestFrom(ctx)
	meta.FileID = elem.ID
	ctx = storagetypes.WithSaveRequest(ctx, meta)
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
				cancelled := errors.Is(uploadCtx.Err(), context.Canceled) && (errors.Is(err, context.Canceled) || errors.Is(err, io.ErrClosedPipe))
				if !cancelled {
					logger.Errorf("Failed to download file: %v", err)
				}
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
	if capacity, ok := elem.Storage.(interface {
		Reserve(context.Context, int64, string) error
	}); ok {
		if err := capacity.Reserve(ctx, elem.File.Size(), elem.Path); err != nil {
			return err
		}
	}
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
			cancelled := errors.Is(vctx.Err(), context.Canceled) && errors.Is(err, context.Canceled)
			if !cancelled && !errors.Is(err, storagetypes.ErrSaveQueued) && !errors.Is(err, storagetypes.ErrSaveSkipped) {
				logger.Errorf("Failed to save file: %s, retrying...", err)
			}
			return err
		}
		return nil
	}, retry.Context(vctx), retry.RetryTimes(uint(config.C().Retry)))
	return err
}

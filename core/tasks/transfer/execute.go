package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/storage"
	"golang.org/x/sync/errgroup"
)

// Execute implements core.Executable.
func (t *Task) Execute(ctx context.Context) error {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("transfer[%s]", t.ID))
	logger.Info("Starting transfer task")
	t.results.Reset(t.resultElements())
	if t.Progress != nil {
		t.Progress.OnStart(ctx, t)
	}

	workers := config.C().Workers
	eg, gctx := errgroup.WithContext(ctx)
	eg.SetLimit(workers)

	for index, elem := range t.elems {
		eg.Go(func() (runErr error) {
			t.results.Start(index)
			var outcomeErr error
			defer func() {
				if outcomeErr == nil {
					outcomeErr = runErr
				}
				t.results.Finish(index, outcomeErr, ctx.Err())
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

			err := t.processElement(gctx, elem)
			outcomeErr = err
			if err != nil && (!t.IgnoreErrors || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
				return err
			}
			if err != nil {
				t.processingMu.Lock()
				t.failed[elem.ID] = err
				t.processingMu.Unlock()
				logger.Errorf("Failed to process file %s: %v", elem.FileInfo.Name, err)
			}
			return nil
		})
	}

	err := eg.Wait()
	if err == nil {
		err = ctx.Err()
	}
	summary := t.ResultSummary()
	logger.Info("Transfer file outcomes", "total", summary.Total, "succeeded", summary.Succeeded,
		"failed", summary.Failed, "cancelled", summary.Cancelled, "interrupted", summary.Interrupted,
		"pending", summary.Pending, "running", summary.Running)
	if err != nil {
		logger.Errorf("Error during transfer processing: %v", err)
	} else {
		logger.Info("Transfer task completed successfully")
	}

	if t.Progress != nil {
		t.Progress.OnDone(ctx, t, err)
	}
	return err
}

func (t *Task) processElement(ctx context.Context, elem TaskElement) (resultErr error) {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("file[%s]", elem.FileInfo.Name))

	// Check whether the source storage supports reading
	readableStorage, ok := elem.SourceStorage.(storage.StorageReadable)
	if !ok {
		return fmt.Errorf("source storage %s does not support reading", elem.SourceStorage.Name())
	}

	logger.Info("Opening file from source storage")
	reader, size, err := readableStorage.OpenFile(ctx, elem.SourcePath)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	source := &sourceReader{ctx: ctx, reader: reader, expected: size}
	stopClose := context.AfterFunc(ctx, func() {
		if err := source.Close(); err != nil {
			logger.Debug("Failed to close canceled source", "error", err)
		}
	})
	defer func() {
		stopClose()
		if err := source.Close(); err != nil && !errors.Is(resultErr, err) {
			resultErr = errors.Join(resultErr, err)
		}
	}()

	// Build target storage path: /target_path/filename
	storagePath := path.Join(elem.TargetPath, elem.FileInfo.Name)

	// Inject file size into context
	ctx = context.WithValue(ctx, ctxkey.ContentLength, size)

	_, cannotStream := elem.TargetStorage.(storage.StorageCannotStream)
	if config.C().Stream && !cannotStream {
		// The source belongs to this task; an HTTP destination must not close
		// it before the task has checked EOF and the source's final result.
		if err := elem.TargetStorage.Save(ctx, struct{ io.Reader }{source}, storagePath); err != nil {
			return fmt.Errorf("failed to upload file to storage: %w", err)
		}
		if err := source.finish(); err != nil {
			return fmt.Errorf("verify source: %w", err)
		}
	} else {
		logger.Info("Downloading to temporary file for ReadSeeker support")
		tempFile, err := t.downloadToTemp(source, elem.FileInfo.Name)
		if err != nil {
			return fmt.Errorf("failed to download to temp: %w", err)
		}
		defer os.Remove(tempFile.Name())
		defer tempFile.Close()
		if err := source.finish(); err != nil {
			return fmt.Errorf("verify source: %w", err)
		}
		ctx = context.WithValue(ctx, ctxkey.ContentLength, source.read)

		if _, err := tempFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("failed to seek temp file: %w", err)
		}

		logger.Infof("Uploading file to storage (size: %d bytes)", size)
		if err := elem.TargetStorage.Save(ctx, tempFile, storagePath); err != nil {
			return fmt.Errorf("failed to upload file to storage: %w", err)
		}
	}

	t.uploaded.Add(source.read)
	if t.Progress != nil {
		t.Progress.OnProgress(ctx, t)
	}
	taskevent.Emit(ctx, taskevent.Event{
		TaskID:          t.ID,
		Phase:           taskevent.PhaseProgress,
		TotalBytes:      t.totalSize,
		DownloadedBytes: t.uploaded.Load(),
	})

	logger.Info("File uploaded successfully")
	return nil
}

func (t *Task) downloadToTemp(reader io.Reader, filename string) (*os.File, error) {
	tempDir := config.C().Temp.BasePath
	if tempDir == "" {
		tempDir = os.TempDir()
	}

	tempFile, err := os.CreateTemp(tempDir, filepath.Base(filename)+"-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}

	if _, err := io.Copy(tempFile, reader); err != nil {
		tempFile.Close()
		os.Remove(tempFile.Name())
		return nil, fmt.Errorf("failed to copy to temp file: %w", err)
	}

	return tempFile, nil
}

package tfile

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"golang.org/x/sync/errgroup"
)

func executeStream(ctx context.Context, task *Task) error {
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("file[%s]", task.File.Name()))

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
		err := task.Storage.Save(uploadCtx, pr, task.Path)
		// Release the downloader even when storage stops reading early.
		pr.CloseWithError(err)
		return err
	})
	wr := newWriter(ctx, pw, task.Progress, task)
	errg.Go(func() error {
		defer pw.Close()
		logger.Info("Starting file download in stream mode")
		_, err := tdler.NewDownloader(task.File).Stream(uploadCtx, wr)
		if err != nil {
			logger.Errorf("Failed to download file: %v", err)
			pw.CloseWithError(err)
		}
		return err
	})
	var err error
	defer func() {
		if task.Progress != nil {
			task.Progress.OnDone(ctx, task, err)
		}
	}()
	if err = errg.Wait(); err != nil {
		// Closing both pipe ends can report ErrClosedPipe; keep cancellation
		// recognizable so the queue runs TaskCancel rather than TaskFail.
		if ctx.Err() != nil && errors.Is(err, io.ErrClosedPipe) {
			err = ctx.Err()
		}
		return err
	}
	logger.Info("File downloaded successfully in stream mode")
	return nil
}

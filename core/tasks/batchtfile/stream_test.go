package batchtfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type streamTestStorage struct {
	storage.Storage
	save func(context.Context, io.Reader) error
}

func (s streamTestStorage) Save(ctx context.Context, r io.Reader, _ string) error {
	return s.save(ctx, r)
}

type streamTestClient struct {
	downloader.Client
	getFile func(context.Context) (tg.UploadFileClass, error)
}

func (c streamTestClient) UploadGetFile(ctx context.Context, _ *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return c.getFile(ctx)
}

func TestStreamTerminatesAndPreservesErrors(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers=1\nthreads=1\nretry=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	targetErr := errors.New("target save failed")
	downloadErr := errors.New("download failed")
	for _, tc := range []struct {
		name   string
		want   error
		cancel bool
	}{
		{name: "success"},
		{name: "target rejects before reading", want: targetErr},
		{name: "target fails after first byte", want: targetErr},
		{name: "target stops reading without error", want: io.ErrClosedPipe},
		{name: "download failure", want: downloadErr},
		{name: "cancel blocked writer", want: context.Canceled, cancel: true},
		{name: "cancel blocked reader", want: context.Canceled, cancel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{}, 1)
			readers := make(chan io.Reader, 1)
			client := streamTestClient{getFile: func(ctx context.Context) (tg.UploadFileClass, error) {
				if tc.name == "download failure" {
					return nil, downloadErr
				}
				if tc.name == "cancel blocked reader" {
					started <- struct{}{}
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return &tg.UploadFile{Bytes: []byte("data")}, nil
			}}
			stor := streamTestStorage{save: func(ctx context.Context, r io.Reader) error {
				readers <- r
				switch tc.name {
				case "target rejects before reading":
					return targetErr
				case "target stops reading without error":
					return nil
				case "target fails after first byte":
					var b [1]byte
					if _, err := io.ReadFull(r, b[:]); err != nil {
						return err
					}
					return targetErr
				case "cancel blocked writer":
					started <- struct{}{}
					<-ctx.Done()
					return ctx.Err()
				case "success":
					data, err := io.ReadAll(r)
					if err != nil {
						return err
					}
					if string(data) != "data" {
						return fmt.Errorf("saved content = %q", data)
					}
					return nil
				default:
					_, err := io.Copy(io.Discard, r)
					return err
				}
			}}
			file := filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, client, 4, "file.bin")
			elem := TaskElement{ID: "file", Storage: stor, Path: "file.bin", File: file, stream: true}
			task := NewBatchTGFileTask("stream-test", ctx, []TaskElement{elem}, nil)
			run := func() error { return task.Execute(ctx) }
			done := make(chan error, 1)
			go func() { done <- run() }()
			// Closing the reader on a failed assertion keeps a regression from leaking a worker.
			t.Cleanup(func() {
				cancel()
				select {
				case r := <-readers:
					r.(*io.PipeReader).CloseWithError(context.Canceled)
				default:
				}
			})
			if tc.cancel {
				select {
				case <-started:
					cancel()
				case <-time.After(3 * time.Second):
					t.Fatal("stream never reached blocking operation")
				}
			}
			select {
			case err := <-done:
				if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
					t.Fatalf("Execute error = %v, want %v", err, tc.want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stream task did not terminate")
			}
			task.processingMu.RLock()
			remaining := len(task.processing)
			task.processingMu.RUnlock()
			if remaining != 0 {
				t.Fatal("stream task retained processing entries")
			}
		})
	}
}

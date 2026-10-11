package batchtfile

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type downloadClient struct{ downloader.Client }

func (downloadClient) UploadGetFile(context.Context, *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	return &tg.UploadFile{Bytes: []byte("data")}, nil
}

type executionStorage struct{ storage.Storage }

func (*executionStorage) Save(_ context.Context, r io.Reader, _ string) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestExecuteWithoutProgress(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 2\nretry = 1\nthreads = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name              string
		stream, duplicate bool
	}{
		{name: "stream", stream: true},
		{name: "cache"},
		{name: "duplicate releases lock", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elem := TaskElement{ID: "file", Storage: &executionStorage{}, Path: "file.bin", stream: tc.stream,
				cacheDir: t.TempDir(),
				File:     tfile.NewTGFile(&tg.InputDocumentFileLocation{}, downloadClient{}, 4, "file.bin")}
			task := NewBatchTGFileTask("test", t.Context(), []TaskElement{elem}, nil)
			if tc.duplicate {
				task.processing[elem.ID] = &elem
			}
			if err := task.Execute(t.Context()); (err != nil) != tc.duplicate {
				t.Fatalf("unexpected execution error: %v", err)
			}
			if !task.processingMu.TryLock() {
				t.Fatal("execution leaked a processing lock")
			}
			remaining := len(task.processing)
			task.processingMu.Unlock()
			if !tc.duplicate && remaining != 0 {
				t.Fatal("completed file is still tracked")
			}
			if !tc.duplicate && task.downloaded.Load() != 4 {
				t.Fatalf("downloaded = %d, want 4", task.downloaded.Load())
			}
			if entries, err := os.ReadDir(elem.cacheDir); err != nil || len(entries) != 0 {
				t.Fatalf("cache file was not cleaned up: %v %v", entries, err)
			}
		})
	}
}

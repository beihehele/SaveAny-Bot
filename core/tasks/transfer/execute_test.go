package transfer

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/storage"
)

type executionStorage struct {
	storage.Storage
	openError error
	open      func(context.Context)
}

func (*executionStorage) Name() string { return "test" }

func (s *executionStorage) OpenFile(ctx context.Context, _ string) (io.ReadCloser, int64, error) {
	if s.open != nil {
		s.open(ctx)
	}
	if s.openError != nil {
		return nil, 0, s.openError
	}
	return io.NopCloser(strings.NewReader("data")), 4, nil
}

func (*executionStorage) Save(_ context.Context, r io.Reader, _ string) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestExecuteProgressAndCancellation(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 2\nstream = true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		openError error
		wantError error
		duplicate bool
	}{
		{name: "nil progress success"},
		{name: "ignored ordinary error", openError: errors.New("unavailable")},
		{name: "cancel is not ignored", openError: context.Canceled, wantError: context.Canceled},
		{name: "deadline is not ignored", openError: context.DeadlineExceeded, wantError: context.DeadlineExceeded},
		{name: "duplicate releases lock", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := &executionStorage{openError: tc.openError}
			elem := TaskElement{ID: "file", SourceStorage: source, TargetStorage: &executionStorage{}, FileInfo: storagetypes.FileInfo{Name: "file.bin", Size: 4}}
			task := NewTransferTask("test", t.Context(), []TaskElement{elem}, nil, true)
			if tc.duplicate {
				task.processing[elem.ID] = &elem
			}
			err := task.Execute(t.Context())
			if tc.duplicate {
				if err == nil {
					t.Fatal("expected duplicate error")
				}
			} else if !errors.Is(err, tc.wantError) {
				t.Fatalf("got %v, want %v", err, tc.wantError)
			}
			if !task.processingMu.TryLock() {
				t.Fatal("execution leaked a processing lock")
			}
			remaining := len(task.processing)
			task.processingMu.Unlock()
			if !tc.duplicate && remaining != 0 {
				t.Fatal("completed file is still tracked")
			}
		})
	}
	t.Run("cancellation after successful storage call", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		source := &executionStorage{open: func(context.Context) { cancel() }}
		elem := TaskElement{ID: "file", SourceStorage: source, TargetStorage: &executionStorage{}, FileInfo: storagetypes.FileInfo{Name: "file.bin", Size: 4}}
		task := NewTransferTask("test", ctx, []TaskElement{elem}, nil, true)
		if err := task.Execute(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("got %v, want cancellation", err)
		}
	})
}

package transfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/storage/local"
)

type integritySource struct {
	executionStorage
	body     string
	size     int64
	closeErr error
	closed   atomic.Int32
}

type integrityReader struct {
	io.Reader
	source *integritySource
}

func (r *integrityReader) Close() error {
	r.source.closed.Add(1)
	return r.source.closeErr
}

func (s *integritySource) OpenFile(context.Context, string) (io.ReadCloser, int64, error) {
	return &integrityReader{Reader: strings.NewReader(s.body), source: s}, s.size, nil
}

func initTransferConfig(t *testing.T, stream bool) string {
	t.Helper()
	temp := t.TempDir()
	file := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("workers=1\nstream=%v\n[temp]\nbase_path=%q\n", stream, filepath.ToSlash(temp))
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	return temp
}

func TestTransferIntegrityProtectsLocalPublication(t *testing.T) {
	closeFailure := errors.New("source finalization failed")
	for _, stream := range []bool{true, false} {
		for _, tc := range []struct {
			name     string
			size     int64
			closeErr error
			wantErr  bool
		}{
			{name: "complete", size: 5},
			{name: "unknown size", size: -1},
			{name: "short read", size: 100, wantErr: true},
			{name: "long read", size: 2, wantErr: true},
			{name: "close failure", size: 5, closeErr: closeFailure, wantErr: true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				temp := initTransferConfig(t, stream)
				dir := t.TempDir()
				dest := &local.Local{}
				if err := dest.Init(t.Context(), &storconfig.LocalStorageConfig{BasePath: dir}); err != nil {
					t.Fatal(err)
				}
				source := &integritySource{body: "short", size: tc.size, closeErr: tc.closeErr}
				elem := *NewTaskElement(source, storagetypes.FileInfo{Name: "file.bin", Size: 5}, dest, "")
				task := NewTransferTask("integrity", t.Context(), []TaskElement{elem}, nil, false)
				err := task.Execute(t.Context())
				if (err != nil) != tc.wantErr || tc.closeErr != nil && !errors.Is(err, tc.closeErr) {
					t.Fatalf("Execute error = %v, want failure %v", err, tc.wantErr)
				}
				if source.closed.Load() != 1 {
					t.Fatalf("source closed %d times", source.closed.Load())
				}
				files, err := os.ReadDir(dir)
				if err != nil {
					t.Fatal(err)
				}
				if tc.wantErr {
					if len(files) != 0 || task.Uploaded() != 0 || task.ResultSummary().Failed != 1 {
						t.Fatalf("failed source published or counted: files=%v summary=%+v bytes=%d", files, task.ResultSummary(), task.Uploaded())
					}
				} else {
					data, err := os.ReadFile(filepath.Join(dir, "file.bin"))
					if err != nil || string(data) != source.body || task.Uploaded() != 5 || task.ResultSummary().Succeeded != 1 {
						t.Fatalf("save: data=%q err=%v summary=%+v bytes=%d", data, err, task.ResultSummary(), task.Uploaded())
					}
				}
				if files, err := os.ReadDir(temp); err != nil || len(files) != 0 {
					t.Fatalf("temporary files leaked: %v %v", files, err)
				}
			})
		}
	}
}

type seekingDestination struct {
	executionStorage
	seekable bool
}

func (*seekingDestination) CannotStream() string { return "requires seeking" }

func (s *seekingDestination) Save(_ context.Context, r io.Reader, _ string) error {
	_, s.seekable = r.(io.ReadSeeker)
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestTransferHonorsCannotStream(t *testing.T) {
	initTransferConfig(t, true)
	dest := &seekingDestination{}
	elem := *NewTaskElement(&executionStorage{}, storagetypes.FileInfo{Name: "file.bin", Size: 4}, dest, "")
	task := NewTransferTask("seek", t.Context(), []TaskElement{elem}, nil, false)
	if err := task.Execute(t.Context()); err != nil || !dest.seekable {
		t.Fatalf("fallback: err=%v seekable=%v", err, dest.seekable)
	}
}

type earlyDestination struct{ executionStorage }

func (*earlyDestination) Save(_ context.Context, r io.Reader, _ string) error {
	_, err := io.CopyN(io.Discard, r, 2)
	return err
}

func TestTransferRejectsEarlyDestinationSuccess(t *testing.T) {
	initTransferConfig(t, true)
	elem := *NewTaskElement(&executionStorage{}, storagetypes.FileInfo{Name: "file.bin", Size: 4}, &earlyDestination{}, "")
	task := NewTransferTask("early", t.Context(), []TaskElement{elem}, nil, false)
	if err := task.Execute(t.Context()); !errors.Is(err, io.ErrUnexpectedEOF) || task.Uploaded() != 0 || task.ResultSummary().Failed != 1 {
		t.Fatalf("early success: err=%v bytes=%d summary=%+v", err, task.Uploaded(), task.ResultSummary())
	}
}

func TestSourceReaderExactSizeFinalization(t *testing.T) {
	closeFailure := errors.New("source finalization failed")
	for _, closeErr := range []error{nil, closeFailure} {
		source := &integritySource{body: "data", size: 4, closeErr: closeErr}
		raw, _, _ := source.OpenFile(t.Context(), "")
		reader := &sourceReader{ctx: t.Context(), reader: raw, expected: 4}
		if _, err := io.CopyN(io.Discard, reader, 4); err != nil {
			t.Fatal(err)
		}
		if err := reader.finish(); !errors.Is(err, closeErr) {
			t.Fatalf("finalization = %v, want %v", err, closeErr)
		}
		if source.closed.Load() != 1 {
			t.Fatalf("source closed %d times", source.closed.Load())
		}
	}
}

type blockingSource struct {
	executionStorage
	started chan struct{}
	closed  chan struct{}
	closes  atomic.Int32
}

func (s *blockingSource) OpenFile(context.Context, string) (io.ReadCloser, int64, error) {
	return s, -1, nil
}

func (s *blockingSource) Read([]byte) (int, error) {
	close(s.started)
	<-s.closed
	return 0, errors.New("source closed during read")
}

func (s *blockingSource) Close() error {
	if s.closes.Add(1) == 1 {
		close(s.closed)
	}
	return nil
}

func TestTransferCancellationClosesBlockedSource(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			initTransferConfig(t, stream)
			source := &blockingSource{started: make(chan struct{}), closed: make(chan struct{})}
			elem := *NewTaskElement(source, storagetypes.FileInfo{Name: "file.bin"}, &executionStorage{}, "")
			task := NewTransferTask("cancel", t.Context(), []TaskElement{elem}, nil, false)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- task.Execute(ctx) }()
			select {
			case <-source.started:
			case <-time.After(3 * time.Second):
				t.Fatal("source did not begin reading")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || source.closes.Load() != 1 || task.Uploaded() != 0 || task.ResultSummary().Cancelled != 1 {
					t.Fatalf("cancellation: err=%v closes=%d bytes=%d summary=%+v", err, source.closes.Load(), task.Uploaded(), task.ResultSummary())
				}
			case <-time.After(3 * time.Second):
				t.Fatal("source close did not unblock cancellation")
			}
		})
	}
}

type cancelOnCheckContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (c *cancelOnCheckContext) Err() error {
	c.checks++
	if c.checks == 2 {
		c.cancel()
	}
	return c.Context.Err()
}

type cancelDuringRead struct {
	cancel context.CancelFunc
	bytes  int
}

func (r cancelDuringRead) Read(p []byte) (int, error) {
	if r.bytes > 0 {
		p[0] = 'x'
	}
	r.cancel()
	return r.bytes, nil
}
func (cancelDuringRead) Close() error { return nil }

func TestSourceReaderPreservesCancellationDuringFinalization(t *testing.T) {
	for _, tc := range []struct {
		duringRead bool
		bytes      int
	}{{}, {duringRead: true}, {duringRead: true, bytes: 1}} {
		ctx, cancel := context.WithCancel(t.Context())
		reader := &sourceReader{ctx: ctx, reader: io.NopCloser(strings.NewReader("")), expected: 0}
		if tc.duringRead {
			reader.reader = cancelDuringRead{cancel: cancel, bytes: tc.bytes}
		} else {
			reader.ctx = &cancelOnCheckContext{Context: ctx, cancel: cancel}
		}
		if err := reader.finish(); !errors.Is(err, context.Canceled) {
			t.Fatalf("during read=%v bytes=%d: cancellation lost: %v", tc.duringRead, tc.bytes, err)
		}
		cancel()
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

type cancelAfterSaveDestination struct {
	executionStorage
	cancel context.CancelFunc
}

func (s *cancelAfterSaveDestination) Save(_ context.Context, r io.Reader, _ string) error {
	_, err := io.Copy(io.Discard, r)
	s.cancel()
	return err
}

func TestTransferKeepsSuccessfulFileWhenParentCancelsAfterSave(t *testing.T) {
	for _, stream := range []bool{true, false} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			initTransferConfig(t, stream)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			dest := &cancelAfterSaveDestination{cancel: cancel}
			elem := *NewTaskElement(&executionStorage{}, storagetypes.FileInfo{Name: "file.bin", Size: 4}, dest, "")
			task := NewTransferTask("saved-before-cancel", ctx, []TaskElement{elem}, nil, false)
			err := task.Execute(ctx)
			if !errors.Is(err, context.Canceled) || task.Uploaded() != 4 || task.ResultSummary().Succeeded != 1 || task.ResultSummary().Cancelled != 0 {
				t.Fatalf("successful file was reclassified: err=%v bytes=%d summary=%+v", err, task.Uploaded(), task.ResultSummary())
			}
		})
	}
}

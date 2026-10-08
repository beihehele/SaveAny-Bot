package watch

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/storage"
)

type blockingStorage struct {
	storage.Storage
	started chan string
	release chan struct{}
}

func (s *blockingStorage) Name() string                        { return "test" }
func (s *blockingStorage) Exists(context.Context, string) bool { return false }
func (s *blockingStorage) Save(ctx context.Context, reader io.Reader, path string) error {
	s.started <- path
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
	}
	_, err := io.Copy(io.Discard, reader)
	return err
}

func TestUploaderFullQueueReuploadAndClose(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stor := &blockingStorage{started: make(chan string, 10), release: make(chan struct{}, 10)}
	u := NewUploader(ctx, UploaderOptions{Storage: stor, Workers: 1, QueueSize: 1, Retry: 1, Overwrite: true})
	t.Cleanup(func() { cancel(); u.Close() })
	root := t.TempDir()
	jobs := make([]uploadJob, 3)
	for i, name := range []string{"a", "b", "c"} {
		p := filepath.Join(root, name)
		if err := os.WriteFile(p, []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
		jobs[i] = uploadJob{localPath: p, relPath: name}
	}
	if !u.Submit(ctx, jobs[0]) {
		t.Fatal("submit a failed")
	}
	awaitUpload(t, stor.started, "a")
	if !u.Submit(ctx, jobs[1]) || !u.Submit(ctx, jobs[0]) {
		t.Fatal("submit b/dirty a failed")
	}
	blocked := make(chan bool, 1)
	go func() { blocked <- u.Submit(ctx, jobs[2]) }()
	closed := make(chan struct{})
	go func() { u.Close(); close(closed) }()
	select {
	case accepted := <-blocked:
		if accepted {
			t.Fatal("accepted job during close")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked Submit did not wake on Close")
	}
	stor.release <- struct{}{}
	awaitUpload(t, stor.started, "b")
	stor.release <- struct{}{}
	awaitUpload(t, stor.started, "a")
	stor.release <- struct{}{}
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("Close deadlocked while re-uploading")
	}
	if u.Submit(ctx, jobs[0]) {
		t.Fatal("accepted after Close")
	}
	u.Close()
}

func awaitUpload(t *testing.T, started <-chan string, want string) {
	t.Helper()
	select {
	case got := <-started:
		if got != want {
			t.Fatalf("upload=%s want=%s", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("upload %s did not start", want)
	}
}

func TestUploaderCancellationWakesSubmit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	stor := &blockingStorage{started: make(chan string, 10), release: make(chan struct{})}
	u := NewUploader(ctx, UploaderOptions{Storage: stor, Workers: 1, QueueSize: 1, Retry: 1, Overwrite: true})
	t.Cleanup(func() { cancel(); u.Close() })
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	if !u.Submit(ctx, uploadJob{localPath: file, relPath: "a"}) {
		t.Fatal("first submit")
	}
	awaitUpload(t, stor.started, "a")
	if !u.Submit(ctx, uploadJob{localPath: file + "2", relPath: "b"}) {
		t.Fatal("queued submit")
	}
	result := make(chan bool, 1)
	go func() { result <- u.Submit(context.Background(), uploadJob{localPath: file + "3"}) }()
	cancel()
	select {
	case accepted := <-result:
		if accepted {
			t.Fatal("accepted after service cancellation")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Submit ignored service cancellation")
	}
	u.Close()
}

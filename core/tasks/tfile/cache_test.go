package tfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage/local"
)

func configureCache(t *testing.T, dir string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("workers=2\nthreads=1\nretry=1\nstream=false\n[temp]\nbase_path=%q\n", filepath.ToSlash(dir))
	if err := os.WriteFile(p, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentSameIDAndNameOwnDistinctCacheFiles(t *testing.T) {
	dir := t.TempDir()
	configureCache(t, dir)
	ready := make(chan string, 2)
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	var workers sync.WaitGroup
	defer func() { close(release); cancel(); workers.Wait() }()
	results := make(chan error, 2)
	for _, payload := range []string{"one!", "two!"} {
		file := filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, streamTestClient{getFile: func(context.Context) (tg.UploadFileClass, error) {
			return &tg.UploadFile{Bytes: []byte(payload)}, nil
		}}, 4, "same.bin")
		stor := streamTestStorage{save: func(ctx context.Context, r io.Reader) error {
			ready <- r.(*os.File).Name()
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
			data, err := io.ReadAll(r)
			if err == nil && string(data) != payload {
				return fmt.Errorf("cache payload corrupted: %q", data)
			}
			return err
		}}
		task, err := NewTGFileTask("same-id", ctx, file, stor, "same.bin", nil)
		if err != nil {
			t.Fatal(err)
		}
		workers.Add(1)
		go func() { defer workers.Done(); results <- task.Execute(ctx) }()
	}
	paths := make([]string, 0, 2)
	for range 2 {
		select {
		case p := <-ready:
			paths = append(paths, p)
		case <-ctx.Done():
			t.Fatal("downloads did not reach storage")
		}
	}
	if paths[0] == paths[1] {
		t.Fatal("concurrent tasks share a cache path")
	}
	// Release both readers and join executions before checking ownership cleanup.
	release <- struct{}{}
	release <- struct{}{}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cache remains: %v %v", entries, err)
	}
}

func fixtureFile(name string, err error) filepkg.TGFile {
	return filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, streamTestClient{getFile: func(context.Context) (tg.UploadFileClass, error) {
		if err != nil {
			return nil, err
		}
		return &tg.UploadFile{Bytes: []byte("data")}, nil
	}}, 4, name)
}

func TestCacheDoesNotUseResourceNameOrTaskID(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	configureCache(t, cacheDir)
	sentinel := filepath.Join(root, "sentinel.bin")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	stor := &local.Local{}
	if err := stor.Init(t.Context(), &storconfig.LocalStorageConfig{BasePath: filepath.Join(root, "saved")}); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"../../../sentinel.bin", `..\..\..\sentinel.bin`, sentinel, strings.Repeat("a", 400) + ".bin", "normal.bin"} {
		t.Run(fmt.Sprint(index), func(t *testing.T) {
			p := fmt.Sprintf("file-%d.bin", index)
			task, err := NewTGFileTask("../../../task-id", t.Context(), fixtureFile(name, nil), stor, p, nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(cacheDir); err == nil && index == 0 {
				t.Fatal("constructor allocated cache before execution")
			}
			if err := task.Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(root, "saved", p))
			if err != nil || string(data) != "data" {
				t.Fatalf("saved payload changed: %q %v", data, err)
			}
			data, err = os.ReadFile(sentinel)
			if err != nil || string(data) != "untouched" {
				t.Fatalf("outside sentinel changed: %q %v", data, err)
			}
			entries, err := os.ReadDir(cacheDir)
			if err != nil || len(entries) != 0 {
				t.Fatalf("cache not cleaned: %v %v", entries, err)
			}
		})
	}
}

type completionProgress struct {
	starts, done int
	doneErr      error
}

func (p *completionProgress) OnStart(context.Context, TaskInfo)                { p.starts++ }
func (*completionProgress) OnProgress(context.Context, TaskInfo, int64, int64) {}
func (p *completionProgress) OnDone(_ context.Context, _ TaskInfo, err error) {
	p.done++
	p.doneErr = err
}

func TestCompletionNotifiesExactlyOnceOnEveryExit(t *testing.T) {
	configureCache(t, t.TempDir())
	failure := errors.New("synthetic failure")
	for _, name := range []string{"cache creation", "download", "save", "success", "stream failure", "stream success", "cancel"} {
		t.Run(name, func(t *testing.T) {
			progress := &completionProgress{}
			dir := t.TempDir()
			if name == "cache creation" {
				dir = filepath.Join(dir, "regular-file")
				if err := os.WriteFile(dir, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			var downloadErr error
			if name == "download" || name == "stream failure" {
				downloadErr = failure
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "cancel" {
				cancel()
			}
			stor := streamTestStorage{save: func(_ context.Context, r io.Reader) error {
				if name == "save" {
					return failure
				}
				_, err := io.Copy(io.Discard, r)
				return err
			}}
			task := &Task{ID: name, File: fixtureFile("safe.bin", downloadErr), Storage: stor, Path: "safe.bin", cacheDir: dir, Progress: progress, stream: strings.HasPrefix(name, "stream")}
			err := task.Execute(ctx)
			wantFailure := name != "success" && name != "stream success"
			if (err != nil) != wantFailure {
				t.Fatalf("unexpected execution result: %v", err)
			}
			if progress.starts != 1 || progress.done != 1 || progress.doneErr != err {
				t.Fatalf("completion not paired with final error: %+v execute=%v", progress, err)
			}
			if name != "cache creation" {
				entries, err := os.ReadDir(dir)
				if err != nil || len(entries) != 0 {
					t.Fatalf("cache remains: %v %v", entries, err)
				}
			}
		})
	}
}

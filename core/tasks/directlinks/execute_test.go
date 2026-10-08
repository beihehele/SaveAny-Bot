package directlinks

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/storage"
)

type executionStorage struct {
	storage.Storage
	saveError error
}

func TestHEADFallback(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 1\nretry = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	for _, code := range []int{200, 405, 501, 403, 404} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			var gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Length", "4")
					w.WriteHeader(code)
					return
				}
				gets.Add(1)
				if r.Header.Get("Range") != "" {
					w.Header().Set("Content-Range", "bytes 0-0/4")
					w.WriteHeader(206)
					io.WriteString(w, "d")
					return
				}
				io.WriteString(w, "data")
			}))
			defer server.Close()
			task := NewTask(t.Name(), t.Context(), []string{server.URL + "/file.bin"}, &executionStorage{}, "", nil)
			task.stream = true
			task.client = server.Client()
			err := task.Execute(t.Context())
			allowed := code == 200 || code == 405 || code == 501
			if allowed && (err != nil || task.TotalBytes() != 4 || task.downloaded.Load() != 1) {
				t.Fatalf("err=%v total=%d count=%d", err, task.TotalBytes(), task.downloaded.Load())
			}
			if !allowed && (err == nil || gets.Load() != 0) {
				t.Fatalf("unexpected fallback: err=%v GETs=%d", err, gets.Load())
			}
		})
	}
}

func (s *executionStorage) Save(_ context.Context, r io.Reader, _ string) error {
	if s.saveError != nil {
		return s.saveError
	}
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestExecuteLinkTracking(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 2\nretry = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "4")
		if r.Method != http.MethodHead {
			_, _ = io.WriteString(w, "data")
		}
	}))
	defer server.Close()
	for _, tc := range []struct {
		name      string
		duplicate bool
		saveError error
		wantCount int64
	}{
		{name: "success clears tracking", wantCount: 1},
		{name: "failure is not counted", saveError: errors.New("save failed")},
		{name: "duplicate releases lock", duplicate: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			link := server.URL + "/file.bin"
			task := NewTask("test", t.Context(), []string{link}, &executionStorage{saveError: tc.saveError}, "", nil)
			task.stream = true
			task.client = server.Client()
			if tc.duplicate {
				task.processing[link] = task.files[0]
			}
			err := task.Execute(t.Context())
			if (err != nil) != (tc.duplicate || tc.saveError != nil) {
				t.Fatalf("unexpected execution error: %v", err)
			}
			if !task.processingMu.TryLock() {
				t.Fatal("execution leaked a processing lock")
			}
			remaining := len(task.processing)
			task.processingMu.Unlock()
			if !tc.duplicate && remaining != 0 {
				t.Fatalf("completed file is still tracked: %d", remaining)
			}
			if got := task.downloaded.Load(); got != tc.wantCount {
				t.Fatalf("downloaded = %d, want %d", got, tc.wantCount)
			}
		})
	}
}

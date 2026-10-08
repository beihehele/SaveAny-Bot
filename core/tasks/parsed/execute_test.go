package parsed

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/parser"
	"github.com/krau/SaveAny-Bot/storage"
)

type executionStorage struct {
	storage.Storage
	saveError error
}

func (s *executionStorage) Save(_ context.Context, r io.Reader, _ string) error {
	if s.saveError != nil {
		return s.saveError
	}
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestExecuteResourceTracking(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 2\nretry = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "data")
	}))
	defer server.Close()
	resource := parser.Resource{URL: server.URL, Filename: "file.bin", Headers: map[string]string{"X-One": "1", "X-Two": "2"}}
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
			task := NewTask("test", t.Context(), &executionStorage{saveError: tc.saveError}, "", &parser.Item{Resources: []parser.Resource{resource}}, nil)
			task.stream = true
			task.httpClient = server.Client()
			if tc.duplicate {
				task.processing[resource.ID()] = &resource
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
				t.Fatalf("completed resource is still tracked: %d", remaining)
			}
			if got := task.downloaded.Load(); got != tc.wantCount {
				t.Fatalf("downloaded = %d, want %d", got, tc.wantCount)
			}
		})
	}
}

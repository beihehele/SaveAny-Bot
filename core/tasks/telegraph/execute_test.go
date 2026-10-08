package telegraph

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	tphclient "github.com/krau/SaveAny-Bot/pkg/telegraph"
	"github.com/krau/SaveAny-Bot/storage"
)

type executionStorage struct{ storage.Storage }

func (*executionStorage) Save(_ context.Context, r io.Reader, _ string) error {
	_, err := io.Copy(io.Discard, r)
	return err
}

func TestExecuteWithoutProgress(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers = 2\nretry = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "image")
	}))
	defer server.Close()
	task := NewTask("test", t.Context(), "article", []string{server.URL + "/image.jpg"}, &executionStorage{}, "", tphclient.NewClient(), nil)
	if err := task.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	if task.downloaded.Load() != 1 {
		t.Fatalf("downloaded = %d, want 1", task.downloaded.Load())
	}
}

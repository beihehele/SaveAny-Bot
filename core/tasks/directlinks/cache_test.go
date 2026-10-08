package directlinks

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/storage"
)

type concurrentCacheStorage struct {
	storage.Storage
	mu       sync.Mutex
	ready    int
	barrier  chan struct{}
	contents []string
}

func (s *concurrentCacheStorage) Save(ctx context.Context, r io.Reader, _ string) error {
	s.mu.Lock()
	s.ready++
	if s.ready == 2 {
		close(s.barrier)
	}
	s.mu.Unlock()
	select {
	case <-s.barrier:
	case <-ctx.Done():
		return ctx.Err()
	}
	content, err := io.ReadAll(r)
	s.mu.Lock()
	s.contents = append(s.contents, string(content))
	s.mu.Unlock()
	return err
}

func TestConcurrentSameFilenameHasIndependentCache(t *testing.T) {
	dir := t.TempDir()
	cache := filepath.Join(dir, "cache")
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf("workers = 2\nretry = 1\n[temp]\nbase_path = %q\n", cache)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="same.bin"`)
		w.Header().Set("Content-Length", "5")
		if r.Method != http.MethodHead {
			fmt.Fprint(w, r.URL.Path[1:])
		}
	}))
	defer server.Close()
	stor := &concurrentCacheStorage{barrier: make(chan struct{})}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	task := NewTask("same-name", ctx, []string{server.URL + "/alpha", server.URL + "/bravo"}, stor, "", nil)
	task.stream = false
	task.client = server.Client()
	if err := task.Execute(ctx); err != nil {
		t.Fatal(err)
	}
	slices.Sort(stor.contents)
	if !slices.Equal(stor.contents, []string{"alpha", "bravo"}) {
		t.Fatalf("cache contents crossed: %q", stor.contents)
	}
	entries, err := os.ReadDir(cache)
	if err != nil || len(entries) != 0 {
		t.Fatalf("cache leftovers=%v err=%v", entries, err)
	}
}

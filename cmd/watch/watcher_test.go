package watch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoppedWatcherDoesNotRearmTimer(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := NewWatcher(t.Context(), WatcherOptions{Root: root, Debounce: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	w.scheduleUpload(t.Context(), file)
	w.cleanup()
	// Model a timer already firing when cleanup stopped the remaining timers.
	w.maybeUpload(t.Context(), file)
	w.scheduleUpload(t.Context(), file)
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) != 0 || len(w.lastSize) != 0 {
		t.Fatal("stopped watcher rearmed work")
	}
}

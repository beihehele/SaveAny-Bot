package handlers

import (
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestWatchLocalAlbumBufferSeparateGroupedIDs(t *testing.T) {
	mgr := &watchMediaGroupHandler{
		groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup),
		timers: make(map[watchLocalAlbumKey]*time.Timer),
	}
	var mu sync.Mutex
	flushes := 0
	var sizes []int

	onFlush := func(files []tfile.TGFileMessage) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
		sizes = append(sizes, len(files))
	}

	// Two albums for same chat/user overlapping in time.
	mgr.addFile(-1001, 1, 10, nil, true, 20*time.Millisecond, onFlush)
	mgr.addFile(-1001, 1, 10, nil, false, 20*time.Millisecond, onFlush)
	mgr.addFile(-1001, 1, 20, nil, false, 20*time.Millisecond, onFlush)
	mgr.addFile(-1001, 1, 20, nil, true, 20*time.Millisecond, onFlush)

	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 2 {
		t.Fatalf("flushes=%d want 2 (separate grouped IDs)", flushes)
	}
	for _, n := range sizes {
		if n != 2 {
			t.Fatalf("album size=%d want 2: %v", n, sizes)
		}
	}
}

func TestWatchLocalAlbumBufferDropsUnmatched(t *testing.T) {
	mgr := &watchMediaGroupHandler{
		groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup),
		timers: make(map[watchLocalAlbumKey]*time.Timer),
	}
	var mu sync.Mutex
	flushes := 0
	mgr.addFile(-1001, 1, 11, nil, false, 20*time.Millisecond, func([]tfile.TGFileMessage) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 0 {
		t.Fatalf("flushes=%d want 0", flushes)
	}
}

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
		timers: make(map[watchLocalAlbumKey]*mediaTimer),
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
		timers: make(map[watchLocalAlbumKey]*mediaTimer),
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

func TestWatchLocalObsoleteTimerPreservesAlbumAndFilterMatch(t *testing.T) {
	mgr := &watchMediaGroupHandler{groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup),
		timers: make(map[watchLocalAlbumKey]*mediaTimer)}
	key := watchLocalAlbumKey{ChatID: -1001, UserID: 1, GroupedID: 10}
	onFlush := func([]tfile.TGFileMessage) { t.Error("unexpected timer callback") }
	mgr.addFile(key.ChatID, key.UserID, key.GroupedID, nil, false, time.Hour, onFlush)
	oldTimer := mgr.timers[key]
	mgr.addFile(key.ChatID, key.UserID, key.GroupedID, nil, true, time.Hour, onFlush)
	newTimer := mgr.timers[key]
	defer newTimer.Stop()
	if group := mgr.takeGroup(key, oldTimer); group != nil {
		t.Fatal("obsolete callback took the reset album")
	}
	if mgr.timers[key] != newTimer {
		t.Fatal("obsolete callback deleted current timer")
	}
	group := mgr.takeGroup(key, newTimer)
	if group == nil || len(group.files) != 2 || !group.matched {
		t.Fatalf("reset album or whole-album filter match lost: %+v", group)
	}
	mgr.addFile(key.ChatID, key.UserID, key.GroupedID, nil, false, time.Hour, onFlush)
	reusedTimer := mgr.timers[key]
	defer reusedTimer.Stop()
	if mgr.takeGroup(key, newTimer) != nil {
		t.Fatal("old callback took a new album with the same key")
	}
	if group := mgr.takeGroup(key, reusedTimer); group == nil || len(group.files) != 1 || group.matched {
		t.Fatal("new album inherited old messages or filter match")
	}
}

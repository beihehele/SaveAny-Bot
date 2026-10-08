package handlers

import (
	"reflect"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestMediaGroupBufferIsolatesSourcesAndPreservesMessages(t *testing.T) {
	mgr := &MediaGroupHandler{
		groups:  make(map[mediaGroupKey][]tfile.TGFileMessage),
		timers:  make(map[mediaGroupKey]*mediaTimer),
		timeout: 30 * time.Millisecond,
	}
	t.Cleanup(func() {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		for _, timer := range mgr.timers {
			timer.Stop()
		}
	})
	type result struct {
		key   mediaGroupKey
		files []tfile.TGFileMessage
	}
	results := make(chan result, 12)
	keys := []mediaGroupKey{
		{chatID: 100, userID: 41, groupID: 7},
		{chatID: 200, userID: 41, groupID: 7}, // Same sender and album ID, different chat.
		{chatID: 100, userID: 42, groupID: 7}, // Same chat and album ID, different sender.
		{chatID: 100, userID: 41, groupID: 8}, // Overlapping albums for the same sender.
	}
	expected := make(map[mediaGroupKey][]tfile.TGFileMessage)
	// Interleave the albums. Keep arrival order, message pointers, and captions.
	for _, id := range []int{20, 10} {
		for _, key := range keys {
			msg := &tg.Message{ID: id, GroupedID: key.groupID, Message: "album caption"}
			file := tfile.NewTGFile(nil, nil, 1, "photo.jpg", tfile.WithMessage(msg)).(tfile.TGFileMessage)
			expected[key] = append(expected[key], file)
			mgr.addFile(key, file, func(files []tfile.TGFileMessage) {
				results <- result{key: key, files: files}
			})
		}
	}
	seen := make(map[mediaGroupKey]bool)
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for range keys {
		select {
		case got := <-results:
			if seen[got.key] {
				t.Fatalf("album flushed twice: %+v", got.key)
			}
			seen[got.key] = true
			if !reflect.DeepEqual(got.files, expected[got.key]) {
				t.Fatalf("album %+v changed or mixed messages: got %v want %v", got.key, got.files, expected[got.key])
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for albums: received %d of %d", len(seen), len(keys))
		}
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if len(mgr.groups) != 0 || len(mgr.timers) != 0 {
		t.Fatal("completed albums were not removed from the buffer")
	}
}

func TestMediaGroupTimeoutConfiguration(t *testing.T) {
	for _, seconds := range []int{-1, 0, 3} {
		mgr := &MediaGroupHandler{}
		mgr.SetupTimeout(seconds)
		want := time.Duration(max(seconds, 1)) * time.Second
		if mgr.timeout != want {
			t.Fatalf("timeout=%v want %v", mgr.timeout, want)
		}
		mgr.SetupTimeout(20)
		if mgr.timeout != want {
			t.Fatal("timeout changed after initialization")
		}
	}
}

func TestMediaGroupObsoleteTimerCannotTakeResetOrReusedAlbum(t *testing.T) {
	mgr := &MediaGroupHandler{groups: make(map[mediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[mediaGroupKey]*mediaTimer), timeout: time.Hour}
	key := mediaGroupKey{chatID: 100, userID: 41, groupID: 7}
	onFlush := func([]tfile.TGFileMessage) { t.Error("unexpected timer callback") }
	mgr.addFile(key, nil, onFlush)
	oldTimer := mgr.timers[key]
	mgr.addFile(key, nil, onFlush)
	newTimer := mgr.timers[key]
	defer newTimer.Stop()
	if files := mgr.takeGroup(key, oldTimer); len(files) != 0 {
		t.Fatal("obsolete callback took the reset album")
	}
	if mgr.timers[key] != newTimer || len(mgr.groups[key]) != 2 {
		t.Fatal("obsolete callback changed the current timer or messages")
	}
	if files := mgr.takeGroup(key, newTimer); len(files) != 2 {
		t.Fatalf("current callback took %d files, want 2", len(files))
	}
	mgr.addFile(key, nil, onFlush)
	reusedTimer := mgr.timers[key]
	defer reusedTimer.Stop()
	if files := mgr.takeGroup(key, newTimer); len(files) != 0 {
		t.Fatal("previous album callback consumed a reused group key")
	}
	if files := mgr.takeGroup(key, reusedTimer); len(files) != 1 {
		t.Fatal("new album was lost")
	}
}

func TestMediaGroupResetUsesLatestCallback(t *testing.T) {
	mgr := &MediaGroupHandler{groups: make(map[mediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[mediaGroupKey]*mediaTimer), timeout: time.Hour}
	key := mediaGroupKey{chatID: 100, userID: 41, groupID: 7}
	results := make(chan int, 2)
	mgr.addFile(key, nil, func([]tfile.TGFileMessage) { results <- -1 })
	mgr.mu.Lock()
	mgr.timeout = time.Millisecond
	mgr.mu.Unlock()
	mgr.addFile(key, nil, func(files []tfile.TGFileMessage) { results <- len(files) })
	select {
	case n := <-results:
		if n != 2 {
			t.Fatalf("flush result = %d, want latest callback with 2 files", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("reset album was not flushed")
	}
}

func TestMediaGroupImmediateTimerSeesPublishedOwner(t *testing.T) {
	mgr := &MediaGroupHandler{groups: make(map[mediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[mediaGroupKey]*mediaTimer)}
	results := make(chan int64, 100)
	for id := range int64(100) {
		key := mediaGroupKey{chatID: 100, userID: 41, groupID: id}
		mgr.addFile(key, nil, func(files []tfile.TGFileMessage) {
			if len(files) == 1 {
				results <- key.groupID
			}
		})
	}
	seen := make(map[int64]bool)
	for range 100 {
		select {
		case id := <-results:
			if seen[id] {
				t.Fatal("album flushed twice")
			}
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("immediate callback lost timer ownership")
		}
	}
}

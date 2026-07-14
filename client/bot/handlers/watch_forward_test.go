package handlers

import (
	"sync"
	"testing"
	"time"
)

func TestForwardAlbumBufferFlushesSortedIDs(t *testing.T) {
	var mu sync.Mutex
	var got []int
	buf := newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		got = append([]int(nil), ids...)
	})
	buf.add(-1001, -1002, 0, 99, 3, true, 20*time.Millisecond)
	buf.add(-1001, -1002, 0, 99, 1, true, 20*time.Millisecond)
	buf.add(-1001, -1002, 0, 99, 2, true, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("got len %d want 3: %v", len(got), got)
	}
	want := map[int]bool{1: true, 2: true, 3: true}
	for _, id := range got {
		if !want[id] {
			t.Fatalf("unexpected id %d in %v", id, got)
		}
		delete(want, id)
	}
}

func TestForwardAlbumBufferSeparateTargets(t *testing.T) {
	var mu sync.Mutex
	flushes := 0
	buf := newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(-1001, -1002, 0, 1, 10, true, 20*time.Millisecond)
	buf.add(-1001, -1003, 0, 1, 11, true, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 2 {
		t.Fatalf("flushes=%d want 2", flushes)
	}
}

func TestForwardAlbumBufferSeparateTopics(t *testing.T) {
	var mu sync.Mutex
	flushes := 0
	buf := newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(-1001, -1002, 1, 1, 10, true, 20*time.Millisecond)
	buf.add(-1001, -1002, 2, 1, 11, true, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 2 {
		t.Fatalf("flushes=%d want 2", flushes)
	}
}

func TestForwardAlbumBufferFilterDefersToAlbum(t *testing.T) {
	var mu sync.Mutex
	var got []int
	buf := newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		got = append([]int(nil), ids...)
	})
	// Only first part matches caption filter; rest have empty caption.
	buf.add(-1001, -1002, 0, 99, 1, true, 20*time.Millisecond)
	buf.add(-1001, -1002, 0, 99, 2, false, 20*time.Millisecond)
	buf.add(-1001, -1002, 0, 99, 3, false, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 {
		t.Fatalf("got len %d want 3 (whole album): %v", len(got), got)
	}
}

func TestForwardAlbumBufferFilterDropsUnmatchedAlbum(t *testing.T) {
	var mu sync.Mutex
	flushes := 0
	buf := newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(-1001, -1002, 0, 99, 1, false, 20*time.Millisecond)
	buf.add(-1001, -1002, 0, 99, 2, false, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 0 {
		t.Fatalf("flushes=%d want 0", flushes)
	}
}

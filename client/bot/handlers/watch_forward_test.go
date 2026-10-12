package handlers

import (
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/database"
)

func TestForwardAlbumTimerOwnership(t *testing.T) {
	for _, reuse := range []bool{false, true} {
		t.Run(map[bool]string{false: "reset", true: "reused key"}[reuse], func(t *testing.T) {
			buf := newForwardAlbumBuffer(func(database.WatchChat, []int) { t.Error("unexpected timer flush") })
			key := forwardAlbumKey{SourceID: 1, TargetID: 2, TargetTopicID: 3, GroupedID: 4}
			buf.add(database.WatchChat{ChatID: 1, TargetID: 2, TargetTopicID: 3}, 4, 10, true, time.Hour)
			old := buf.timers[key]
			defer old.Stop()
			want := []int{10, 11}
			if reuse {
				buf.takeGroup(key, old)
				want = []int{11}
			}
			buf.add(database.WatchChat{ChatID: 1, TargetID: 2, TargetTopicID: 3}, 4, 11, false, time.Hour)
			current := buf.timers[key]
			defer current.Stop()
			if got := buf.takeGroup(key, old); got != nil {
				t.Fatal("obsolete callback consumed the current album")
			}
			if buf.timers[key] != current {
				t.Fatal("obsolete callback removed the current timer")
			}
			got := buf.takeGroup(key, current)
			if got == nil || !reflect.DeepEqual(got.ids, want) || got.matched != !reuse {
				t.Fatalf("album = %+v, want ids %v and matched %v", got, want, !reuse)
			}
			if buf.takeGroup(key, current) != nil {
				t.Fatal("callback consumed an album twice")
			}
		})
	}
}

func TestForwardAlbumZeroDelayTimer(t *testing.T) {
	var flushed atomic.Int32
	done := make(chan struct{}, 100)
	buf := newForwardAlbumBuffer(func(database.WatchChat, []int) {
		flushed.Add(1)
		done <- struct{}{}
	})
	for i := range 100 {
		buf.add(database.WatchChat{ChatID: 1, TargetID: 2, TargetTopicID: 3}, int64(i), i, true, 0)
	}
	for range 100 {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("zero-delay timer failed to flush")
		}
	}
	buf.mu.Lock()
	defer buf.mu.Unlock()
	if len(buf.groups) != 0 || len(buf.timers) != 0 || flushed.Load() != 100 {
		t.Fatalf("groups=%d timers=%d flushes=%d", len(buf.groups), len(buf.timers), flushed.Load())
	}
}

func TestForwardAlbumBufferPreservesArrivalOrder(t *testing.T) {
	flushed := make(chan []int, 1)
	buf := newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
		flushed <- append([]int(nil), ids...)
	})
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 3, true, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 1, true, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 2, true, 20*time.Millisecond)
	select {
	case got := <-flushed:
		// The buffer preserves arrival order; the forwarding RPC sorts member IDs.
		if !reflect.DeepEqual(got, []int{3, 1, 2}) {
			t.Fatalf("arrival order changed: %v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("album did not flush")
	}
}

func TestForwardAlbumBufferSeparateTargets(t *testing.T) {
	var mu sync.Mutex
	flushes := 0
	buf := newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 1, 10, true, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1003, TargetTopicID: 0}, 1, 11, true, 20*time.Millisecond)
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
	buf := newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 1}, 1, 10, true, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 2}, 1, 11, true, 20*time.Millisecond)
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
	buf := newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		got = append([]int(nil), ids...)
	})
	// Only first part matches caption filter; rest have empty caption.
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 1, true, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 2, false, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 3, false, 20*time.Millisecond)
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
	buf := newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
		mu.Lock()
		defer mu.Unlock()
		flushes++
	})
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 1, false, 20*time.Millisecond)
	buf.add(database.WatchChat{ChatID: -1001, TargetID: -1002, TargetTopicID: 0}, 99, 2, false, 20*time.Millisecond)
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if flushes != 0 {
		t.Fatalf("flushes=%d want 0", flushes)
	}
}

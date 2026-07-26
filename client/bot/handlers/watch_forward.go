package handlers

import (
	"sync"
	"time"

	"github.com/charmbracelet/log"
	userclient "github.com/krau/SaveAny-Bot/client/user"
)

type forwardAlbumKey struct {
	SourceID      int64
	TargetID      int64
	TargetTopicID int
	GroupedID     int64
}

type forwardAlbumGroup struct {
	ids     []int
	matched bool
}

type forwardAlbumBuffer struct {
	mu      sync.Mutex
	groups  map[forwardAlbumKey]*forwardAlbumGroup
	timers  map[forwardAlbumKey]*time.Timer
	onFlush func(sourceID, targetID int64, targetTopicID int, ids []int)
}

func newForwardAlbumBuffer(onFlush func(sourceID, targetID int64, targetTopicID int, ids []int)) *forwardAlbumBuffer {
	return &forwardAlbumBuffer{
		groups:  make(map[forwardAlbumKey]*forwardAlbumGroup),
		timers:  make(map[forwardAlbumKey]*time.Timer),
		onFlush: onFlush,
	}
}

// add buffers one album part. The album is forwarded only if any part matched the filter.
func (b *forwardAlbumBuffer) add(sourceID, targetID int64, targetTopicID int, groupedID int64, messageID int, filterMatched bool, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := forwardAlbumKey{SourceID: sourceID, TargetID: targetID, TargetTopicID: targetTopicID, GroupedID: groupedID}
	if t, ok := b.timers[key]; ok {
		t.Stop()
	}
	g := b.groups[key]
	if g == nil {
		g = &forwardAlbumGroup{}
		b.groups[key] = g
	}
	g.ids = append(g.ids, messageID)
	if filterMatched {
		g.matched = true
	}
	b.timers[key] = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		g := b.groups[key]
		delete(b.groups, key)
		delete(b.timers, key)
		b.mu.Unlock()
		if g == nil || len(g.ids) == 0 || !g.matched {
			return
		}
		b.onFlush(sourceID, targetID, targetTopicID, g.ids)
	})
}

var watchForwardAlbumBuf = newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
	uctx := userclient.GetCtx()
	if uctx == nil {
		return
	}
	logger := log.FromContext(uctx)
	go func() {
		// DropAuthor copy; [转自] appended by edit after forward (link defaults to ids[0]).
		if err := userclient.ForwardMessagesDropAuthor(uctx, sourceID, targetID, ids, targetTopicID, 0); err != nil {
			logger.Errorf("forward album failed source=%d target=%d topic=%d ids=%v: %v", sourceID, targetID, targetTopicID, ids, err)
		}
	}()
})

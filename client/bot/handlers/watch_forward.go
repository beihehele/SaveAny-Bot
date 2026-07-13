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

type forwardAlbumBuffer struct {
	mu      sync.Mutex
	groups  map[forwardAlbumKey][]int
	timers  map[forwardAlbumKey]*time.Timer
	onFlush func(sourceID, targetID int64, targetTopicID int, ids []int)
}

func newForwardAlbumBuffer(onFlush func(sourceID, targetID int64, targetTopicID int, ids []int)) *forwardAlbumBuffer {
	return &forwardAlbumBuffer{
		groups:  make(map[forwardAlbumKey][]int),
		timers:  make(map[forwardAlbumKey]*time.Timer),
		onFlush: onFlush,
	}
}

func (b *forwardAlbumBuffer) add(sourceID, targetID int64, targetTopicID int, groupedID int64, messageID int, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := forwardAlbumKey{SourceID: sourceID, TargetID: targetID, TargetTopicID: targetTopicID, GroupedID: groupedID}
	if t, ok := b.timers[key]; ok {
		t.Stop()
	}
	b.groups[key] = append(b.groups[key], messageID)
	b.timers[key] = time.AfterFunc(timeout, func() {
		b.mu.Lock()
		ids := b.groups[key]
		delete(b.groups, key)
		delete(b.timers, key)
		b.mu.Unlock()
		if len(ids) == 0 {
			return
		}
		b.onFlush(sourceID, targetID, targetTopicID, ids)
	})
}

var watchForwardAlbumBuf = newForwardAlbumBuffer(func(sourceID, targetID int64, targetTopicID int, ids []int) {
	uctx := userclient.GetCtx()
	if uctx == nil {
		return
	}
	logger := log.FromContext(uctx)
	if err := userclient.ForwardMessagesDropAuthor(uctx, sourceID, targetID, ids, targetTopicID); err != nil {
		logger.Errorf("forward album failed source=%d target=%d topic=%d ids=%v: %v", sourceID, targetID, targetTopicID, ids, err)
	}
})

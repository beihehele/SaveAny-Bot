package handlers

import (
	"sync"
	"time"

	"github.com/charmbracelet/log"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/database"
)

type forwardAlbumKey struct {
	RouteID       uint
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
	timers  map[forwardAlbumKey]*mediaTimer
	onFlush func(route database.WatchChat, ids []int)
}

func newForwardAlbumBuffer(onFlush func(route database.WatchChat, ids []int)) *forwardAlbumBuffer {
	return &forwardAlbumBuffer{
		groups:  make(map[forwardAlbumKey]*forwardAlbumGroup),
		timers:  make(map[forwardAlbumKey]*mediaTimer),
		onFlush: onFlush,
	}
}

// add buffers one album part. The album is forwarded only if any part matched the filter.
func (b *forwardAlbumBuffer) add(route database.WatchChat, groupedID int64, messageID int, filterMatched bool, timeout time.Duration) {
	b.mu.Lock()
	defer b.mu.Unlock()
	key := forwardAlbumKey{RouteID: route.ID, SourceID: route.ChatID, TargetID: route.TargetID, TargetTopicID: route.TargetTopicID, GroupedID: groupedID}
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
	timer := &mediaTimer{}
	timer.Timer = time.AfterFunc(timeout, func() {
		g := b.takeGroup(key, timer)
		if g == nil || len(g.ids) == 0 || !g.matched {
			return
		}
		b.onFlush(route, g.ids)
	})
	b.timers[key] = timer
}

func (b *forwardAlbumBuffer) takeGroup(key forwardAlbumKey, timer *mediaTimer) *forwardAlbumGroup {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Stop cannot revoke a callback that has started. Only the current timer
	// may consume an album, including when its key is reused after a flush.
	if b.timers[key] != timer {
		return nil
	}
	g := b.groups[key]
	delete(b.groups, key)
	delete(b.timers, key)
	return g
}

func (b *forwardAlbumBuffer) dropRoute(id uint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for key, timer := range b.timers {
		if key.RouteID == id {
			timer.Stop()
			delete(b.timers, key)
			delete(b.groups, key)
		}
	}
}

var watchForwardAlbumBuf = newForwardAlbumBuffer(func(route database.WatchChat, ids []int) {
	uctx := userclient.GetCtx()
	if uctx == nil {
		return
	}
	logger := log.FromContext(uctx)
	err := watchRoutes.run(uctx, route, func() error {
		go func() {
			// Same as /copy: ForwardMessage expands grouped_id and links [转] to a caption-bearing part.
			if err := userclient.ForwardMessage(uctx, uctx, route.ChatID, route.TargetID, ids[0], route.TargetTopicID); err != nil {
				logger.Errorf("forward album failed source=%d target=%d topic=%d ids=%v: %v", route.ChatID, route.TargetID, route.TargetTopicID, ids, err)
			}
		}()
		return nil
	})
	logWatchAdmissionError(uctx, route, err)
})

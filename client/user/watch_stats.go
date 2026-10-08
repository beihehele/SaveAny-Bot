package user

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
)

// MediaMessageStats is a best-effort snapshot, not an atomic ledger of delivery.
// Counters cover channel submissions since process start, not successful saves
// or forwards. They are not persisted across restarts.
type MediaMessageStats struct {
	Pending  int
	Buffered int
	Capacity int
	Enqueued uint64
	Dropped  uint64
}

type mediaDeliveryStats struct {
	enqueued atomic.Uint64
	dropped  atomic.Uint64
}

var mediaMessageDelivery mediaDeliveryStats

func (s *mediaDeliveryStats) send(event MediaMessageEvent, ch chan<- MediaMessageEvent) {
	// Preserve the existing non-blocking delivery and full-channel drop policy.
	select {
	case ch <- event:
		s.enqueued.Add(1)
	default:
		dropped := s.dropped.Add(1)
		logger := log.Default()
		if event.Ctx != nil {
			logger = log.FromContext(event.Ctx)
		}
		logger.Warn("media message channel full, dropping event", "chat_id", event.ChatID,
			"message_id", event.MessageID, "buffered", len(ch), "capacity", cap(ch), "dropped_total", dropped)
	}
}

func (s *mediaDeliveryStats) snapshot(m *MediaMessageHandler, ch <-chan MediaMessageEvent) MediaMessageStats {
	m.mu.Lock()
	pending := len(m.events)
	m.mu.Unlock()
	return MediaMessageStats{Pending: pending, Buffered: len(ch), Capacity: cap(ch),
		Enqueued: s.enqueued.Load(), Dropped: s.dropped.Load()}
}

// GetMediaMessageStats reports debounce backlog, channel occupancy and delivery
// counters. Sampling does not consume messages or change timer ownership.
func GetMediaMessageStats() MediaMessageStats {
	return mediaMessageDelivery.snapshot(mediaMessageHandler, mediaMessageCh)
}

func logMediaMessageStats(logger *log.Logger, stats MediaMessageStats, previousDrops uint64) {
	fields := []any{"pending", stats.Pending, "buffered", stats.Buffered, "capacity", stats.Capacity,
		"enqueued_total", stats.Enqueued, "dropped_total", stats.Dropped}
	if stats.Dropped > previousDrops {
		logger.Warn("Media watch events dropped since last observation", append(fields, "dropped_since_last", stats.Dropped-previousDrops)...)
	} else {
		logger.Debug("Media watch delivery snapshot", fields...)
	}
}

func observeMediaMessages(ctx context.Context) {
	logger := log.FromContext(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	var previousDrops uint64
	for {
		select {
		case <-ctx.Done():
			logMediaMessageStats(logger, GetMediaMessageStats(), previousDrops)
			return
		case <-ticker.C:
			stats := GetMediaMessageStats()
			logMediaMessageStats(logger, stats, previousDrops)
			previousDrops = stats.Dropped
		}
	}
}

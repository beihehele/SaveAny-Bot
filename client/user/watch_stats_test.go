package user

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/log"
)

func TestMediaDeliveryStatsPreserveFullChannelAndRetry(t *testing.T) {
	m := &MediaMessageHandler{events: make(map[messageKey]MediaMessageEvent),
		timers: make(map[messageKey]*mediaMessageTimer), debounce: time.Hour}
	event := MediaMessageEvent{ChatID: 100, MessageID: 7}
	m.addEvent(event, func(MediaMessageEvent) { t.Error("unexpected timer callback") })
	defer m.timers[messageKey{ChatID: 100, MessageID: 7}].Stop()
	ch := make(chan MediaMessageEvent, 1)
	var stats mediaDeliveryStats
	stats.send(event, ch)
	stats.send(MediaMessageEvent{ChatID: 200, MessageID: 8}, ch)
	first := stats.snapshot(m, ch)
	if first != (MediaMessageStats{Pending: 1, Buffered: 1, Capacity: 1, Enqueued: 1, Dropped: 1}) {
		t.Fatalf("unexpected full-channel snapshot: %+v", first)
	}
	if got := <-ch; got.ChatID != event.ChatID || got.MessageID != event.MessageID {
		t.Fatal("observation replaced or consumed the accepted event")
	}
	stats.send(event, ch)
	if got := stats.snapshot(m, ch); got.Enqueued != 2 || got.Dropped != 1 || got.Buffered != 1 || got.Pending != 1 {
		t.Fatalf("channel did not recover after drain: %+v", got)
	}
}

func TestMediaDeliveryStatsConcurrentProducersAndSnapshots(t *testing.T) {
	const count = 100
	m := &MediaMessageHandler{events: make(map[messageKey]MediaMessageEvent), timers: make(map[messageKey]*mediaMessageTimer), debounce: time.Hour}
	ch := make(chan MediaMessageEvent, count)
	var stats mediaDeliveryStats
	var wg sync.WaitGroup
	for id := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			event := MediaMessageEvent{ChatID: 100, MessageID: id}
			m.addEvent(event, func(MediaMessageEvent) { t.Error("unexpected timer callback") })
			stats.send(event, ch)
			stats.snapshot(m, ch)
		}()
	}
	wg.Wait()
	defer func() {
		for _, timer := range m.timers {
			timer.Stop()
		}
	}()
	if got := stats.snapshot(m, ch); got != (MediaMessageStats{Pending: count, Buffered: count, Capacity: count, Enqueued: count}) {
		t.Fatalf("concurrent accounting lost events: %+v", got)
	}
	seen := make(map[int]bool)
	for range count {
		event := <-ch
		if seen[event.MessageID] {
			t.Fatal("duplicate event")
		}
		seen[event.MessageID] = true
	}
}

func TestMediaDeliveryObservationReportsDropDeltaAndStops(t *testing.T) {
	var output bytes.Buffer
	logger := log.New(&output)
	logger.SetLevel(log.DebugLevel)
	stats := MediaMessageStats{Pending: 2, Buffered: 100, Capacity: 100, Enqueued: 150, Dropped: 4}
	logMediaMessageStats(logger, stats, 1)
	if got := output.String(); !strings.Contains(got, "dropped_since_last=3") || !strings.Contains(got, "dropped_total=4") {
		t.Fatalf("drop summary missing recovery evidence: %s", got)
	}
	output.Reset()
	logMediaMessageStats(logger, stats, 4)
	if strings.Contains(output.String(), "dropped_since_last") || !strings.Contains(output.String(), "Media watch delivery snapshot") {
		t.Fatal("unchanged total was reported as a new loss")
	}
	ctx, cancel := context.WithCancel(log.WithContext(t.Context(), logger))
	cancel()
	done := make(chan struct{})
	go func() { observeMediaMessages(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("observer did not stop with service context")
	}
}

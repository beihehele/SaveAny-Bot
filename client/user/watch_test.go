package user

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestMediaMessageObsoleteTimerPreservesLatestEvent(t *testing.T) {
	m := &MediaMessageHandler{events: make(map[messageKey]MediaMessageEvent),
		timers: make(map[messageKey]*mediaMessageTimer), debounce: time.Hour}
	key := messageKey{ChatID: 100, MessageID: 7}
	onFlush := func(MediaMessageEvent) { t.Error("unexpected timer callback") }
	m.addEvent(MediaMessageEvent{ChatID: key.ChatID, MessageID: key.MessageID}, onFlush)
	oldTimer := m.timers[key]
	message := &tg.Message{ID: key.MessageID, Message: "edited caption"}
	file := tfile.NewTGFile(nil, nil, 1, "photo.jpg", tfile.WithMessage(message)).(tfile.TGFileMessage)
	m.addEvent(MediaMessageEvent{ChatID: key.ChatID, MessageID: key.MessageID, File: file}, onFlush)
	newTimer := m.timers[key]
	defer newTimer.Stop()
	if _, ok := m.takeEvent(key, oldTimer); ok {
		t.Fatal("obsolete callback consumed edited event")
	}
	if m.timers[key] != newTimer {
		t.Fatal("obsolete callback deleted current timer")
	}
	event, ok := m.takeEvent(key, newTimer)
	if !ok || event.File != file || event.File.Message() != message {
		t.Fatal("latest file or caption pointer lost")
	}
	m.addEvent(MediaMessageEvent{ChatID: key.ChatID, MessageID: key.MessageID}, onFlush)
	reusedTimer := m.timers[key]
	defer reusedTimer.Stop()
	if _, ok := m.takeEvent(key, newTimer); ok {
		t.Fatal("old callback consumed reused message key")
	}
	if _, ok := m.takeEvent(key, reusedTimer); !ok {
		t.Fatal("reused event lost")
	}
}

func TestMediaMessageDebounceIsolatesChatsAndKeepsLatestCaption(t *testing.T) {
	m := &MediaMessageHandler{events: make(map[messageKey]MediaMessageEvent),
		timers: make(map[messageKey]*mediaMessageTimer), debounce: time.Hour}
	results := make(chan MediaMessageEvent, 2)
	onFlush := func(event MediaMessageEvent) { results <- event }
	for _, chatID := range []int64{100, 200} {
		m.addEvent(MediaMessageEvent{ChatID: chatID, MessageID: 7}, onFlush)
	}
	m.mu.Lock()
	m.debounce = time.Millisecond
	m.mu.Unlock()
	expected := make(map[int64]tfile.TGFileMessage)
	for _, chatID := range []int64{100, 200} {
		message := &tg.Message{ID: 7, Message: "latest caption"}
		file := tfile.NewTGFile(nil, nil, 1, "photo.jpg", tfile.WithMessage(message)).(tfile.TGFileMessage)
		expected[chatID] = file
		m.addEvent(MediaMessageEvent{ChatID: chatID, MessageID: 7, File: file}, onFlush)
	}
	for range 2 {
		select {
		case event := <-results:
			if event.File != expected[event.ChatID] {
				t.Fatal("chat mixed or stale caption emitted")
			}
			delete(expected, event.ChatID)
		case <-time.After(3 * time.Second):
			t.Fatal("debounced event was not emitted")
		}
	}
	if len(expected) != 0 {
		t.Fatal("duplicate or missing event")
	}
}

func TestMediaMessageImmediateTimerSeesPublishedOwner(t *testing.T) {
	m := &MediaMessageHandler{events: make(map[messageKey]MediaMessageEvent),
		timers: make(map[messageKey]*mediaMessageTimer)}
	results := make(chan int, 100)
	for id := range 100 {
		m.addEvent(MediaMessageEvent{ChatID: 100, MessageID: id}, func(event MediaMessageEvent) {
			results <- event.MessageID
		})
	}
	seen := make(map[int]bool)
	for range 100 {
		select {
		case id := <-results:
			if seen[id] {
				t.Fatal("event emitted twice")
			}
			seen[id] = true
		case <-time.After(3 * time.Second):
			t.Fatal("immediate callback lost timer ownership")
		}
	}
}

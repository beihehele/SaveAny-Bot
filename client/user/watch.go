package user

import (
	"sync"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type MediaMessageEvent struct {
	Ctx       *ext.Context
	ChatID    int64 // from witch the media message was sent
	MessageID int
	File      tfile.TGFileMessage
}

type messageKey struct {
	ChatID    int64
	MessageID int
}

// mediaMessageTimer has a fixed identity before AfterFunc starts its callback.
// Its Timer field is assigned and read only while the handler mutex is held.
type mediaMessageTimer struct{ *time.Timer }

type MediaMessageHandler struct {
	events   map[messageKey]MediaMessageEvent
	timers   map[messageKey]*mediaMessageTimer
	mu       sync.Mutex
	debounce time.Duration
}

var (
	mediaMessageCh      = make(chan MediaMessageEvent, 100)
	mediaMessageHandler = &MediaMessageHandler{
		events:   make(map[messageKey]MediaMessageEvent),
		timers:   make(map[messageKey]*mediaMessageTimer),
		debounce: 5 * time.Second,
	}
)

func GetMediaMessageCh() chan MediaMessageEvent {
	return mediaMessageCh
}

func MediaMessageDebounce() time.Duration {
	return mediaMessageHandler.debounce
}

func sendMediaMessageEvent(event MediaMessageEvent) {
	mediaMessageHandler.addEvent(event, func(ev MediaMessageEvent) {
		mediaMessageDelivery.send(ev, mediaMessageCh)
	})
}

func (m *MediaMessageHandler) addEvent(event MediaMessageEvent, onFlush func(MediaMessageEvent)) {
	key := messageKey{ChatID: event.ChatID, MessageID: event.MessageID}

	m.mu.Lock()
	defer m.mu.Unlock()
	if timer, exists := m.timers[key]; exists {
		timer.Stop()
	}
	// Always refresh the payload so caption edits after the first update are kept.
	m.events[key] = event
	timer := &mediaMessageTimer{}
	timer.Timer = time.AfterFunc(m.debounce, func() {
		ev, ok := m.takeEvent(key, timer)
		if !ok {
			return
		}
		onFlush(ev)
	})
	m.timers[key] = timer
}

func (m *MediaMessageHandler) takeEvent(key messageKey, timer *mediaMessageTimer) (MediaMessageEvent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Caption edits can reset a timer whose old callback has already started.
	if m.timers[key] != timer {
		return MediaMessageEvent{}, false
	}
	event, ok := m.events[key]
	delete(m.events, key)
	delete(m.timers, key)
	return event, ok
}

func handleMediaMessage(ctx *ext.Context, update *ext.Update) error {
	message := update.EffectiveMessage
	media, ok := message.GetMedia()
	if !ok || media == nil {
		return dispatcher.EndGroups
	}
	support := func() bool {
		switch media.(type) {
		case *tg.MessageMediaDocument, *tg.MessageMediaPhoto:
			return true
		default:
			return false
		}
	}()
	if !support {
		return dispatcher.EndGroups
	}
	file, err := tfile.FromMediaMessage(media, ctx.Raw, message.Message, tfile.WithNameIfEmpty(
		tgutil.GenFileNameFromMessage(*message.Message),
	))
	if err != nil {
		return err
	}
	chatId := update.EffectiveChat().GetID()
	sendMediaMessageEvent(MediaMessageEvent{
		Ctx:       ctx,
		ChatID:    chatId,
		MessageID: message.ID,
		File:      file,
	})
	return dispatcher.EndGroups
}

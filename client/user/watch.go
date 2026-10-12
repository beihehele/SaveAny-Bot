package user

import (
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type MediaMessageEvent struct {
	Ctx       *ext.Context
	ChatID    int64 // from witch the media message was sent
	MessageID int
	File      tfile.TGFileMessage
	// Routes captures recipients before debounce/channel waiting. Do not rebind
	// an old event to routes created while it waits for consumption.
	Routes []database.WatchChat
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
	if previous, exists := m.events[key]; exists {
		// Refresh media/caption without assigning the pending message to newly
		// created routes. The initial event owns its recipient snapshot.
		event.Routes = previous.Routes
	} else {
		event.Routes = slices.Clone(event.Routes)
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
	event, err := captureMediaMessageEvent(ctx, chatId, message.ID, file)
	if err != nil {
		return err
	}
	if len(event.Routes) != 0 {
		sendMediaMessageEvent(event)
	}
	return dispatcher.EndGroups
}

func captureMediaMessageEvent(ctx *ext.Context, chatID int64, messageID int, file tfile.TGFileMessage) (MediaMessageEvent, error) {
	routes, err := database.GetWatchChatsByChatID(ctx, chatID)
	if err != nil {
		return MediaMessageEvent{}, fmt.Errorf("capture watch routes: %w", err)
	}
	event := MediaMessageEvent{Ctx: ctx, ChatID: chatID, MessageID: messageID, File: file,
		Routes: make([]database.WatchChat, 0, len(routes))}
	for _, route := range routes {
		event.Routes = append(event.Routes, *route)
	}
	return event, nil
}

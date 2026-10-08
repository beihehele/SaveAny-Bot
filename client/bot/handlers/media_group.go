package handlers

import (
	"sync"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

// mediaGroupKey isolates albums by chat, sender, and Telegram group ID.
type mediaGroupKey struct {
	chatID  int64
	userID  int64
	groupID int64
}

// mediaTimer has a fixed identity before AfterFunc starts its callback.
// Its Timer field is assigned and read only while the handler mutex is held.
type mediaTimer struct{ *time.Timer }

type MediaGroupHandler struct {
	groups    map[mediaGroupKey][]tfile.TGFileMessage
	timers    map[mediaGroupKey]*mediaTimer
	mu        sync.Mutex
	timeout   time.Duration
	setupOnce sync.Once
}

func (m *MediaGroupHandler) SetupTimeout(timeoutSec int) {
	m.setupOnce.Do(func() {
		if timeoutSec < 1 {
			timeoutSec = 1
		}
		m.timeout = time.Duration(timeoutSec) * time.Second
	})
}

var (
	mediaGroupHandler = &MediaGroupHandler{
		groups: make(map[mediaGroupKey][]tfile.TGFileMessage),
		timers: make(map[mediaGroupKey]*mediaTimer),
		mu:     sync.Mutex{},
	}
)

func handleGroupMediaMessage(ctx *ext.Context, update *ext.Update, message *tg.Message, groupID int64) error {
	mediaGroupHandler.SetupTimeout(max(config.C().Telegram.MediaGroupTimeout, 1))
	logger := log.FromContext(ctx)
	media := message.Media
	supported := mediautil.IsSupported(media)
	if !supported {
		return dispatcher.EndGroups
	}
	userId := update.GetUserChat().GetID()
	userDB, err := database.GetUserByChatID(ctx, userId)
	if err != nil {
		return err
	}
	tfOpts := mediautil.TfileOptions(ctx, userDB, message)
	file, err := tfile.FromMediaMessage(media, ctx.Raw, message, tfOpts...)
	if err != nil {
		logger.Errorf("Failed to get file from media: %s", err)
		return dispatcher.EndGroups
	}
	key := mediaGroupKey{
		chatID:  update.EffectiveChat().GetID(),
		userID:  userId,
		groupID: groupID,
	}
	mediaGroupHandler.addFile(key, file, func(items []tfile.TGFileMessage) {
		processMediaGroup(ctx, update, key, items)
	})
	return dispatcher.EndGroups
}

func (m *MediaGroupHandler) addFile(key mediaGroupKey, file tfile.TGFileMessage, onFlush func([]tfile.TGFileMessage)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.groups[key] = append(m.groups[key], file)
	if timer, exists := m.timers[key]; exists {
		timer.Stop()
	}
	timer := &mediaTimer{}
	timer.Timer = time.AfterFunc(m.timeout, func() {
		if items := m.takeGroup(key, timer); len(items) != 0 {
			onFlush(items)
		}
	})
	m.timers[key] = timer
}

func (m *MediaGroupHandler) takeGroup(key mediaGroupKey, timer *mediaTimer) []tfile.TGFileMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Stop cannot revoke a callback that has already started. Validate ownership
	// while taking the messages, so an obsolete callback cannot consume a reset group.
	if m.timers[key] != timer {
		return nil
	}
	items := m.groups[key]
	delete(m.groups, key)
	delete(m.timers, key)
	return items
}

func processMediaGroup(ctx *ext.Context, update *ext.Update, key mediaGroupKey, items []tfile.TGFileMessage) {
	logger := log.FromContext(ctx)
	if len(items) == 0 {
		logger.Warn("No media items to process for group", "groupID", key.groupID)
		return
	}
	logger.Debugf("Processing media group %d with %d items", key.groupID, len(items))

	userId := update.GetUserChat().GetID()
	msg, err := ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgMediaGroupInfoSavingFiles, nil)), nil)
	if err != nil {
		logger.Errorf("Failed to reply: %s", err)
		return
	}
	stor := storage.FromContext(ctx)
	if stor != nil {
		// In silent mode
		if len(items) == 1 {
			shortcut.CreateAndAddTGFileTaskWithEdit(ctx, userId, stor, "", items[0], msg.ID)
			return
		}
		shortcut.CreateAndAddBatchTGFileTaskWithEdit(ctx, userId, stor, "", items, msg.ID)
		return
	}

	stors := storage.GetUserStorages(ctx, userId)
	markup, err := msgelem.BuildAddSelectStorageKeyboard(stors, tcbdata.Add{
		Files:   items,
		AsBatch: len(items) > 1,
	})
	if err != nil {
		logger.Errorf("Failed to build storage selection keyboard: %s", err)
		ctx.EditMessage(userId, &tg.MessagesEditMessageRequest{
			ID: msg.ID,
			Message: i18n.T(i18nk.BotMsgMediaGroupErrorBuildStorageSelectKeyboardFailed, map[string]any{
				"Error": err.Error(),
			}),
		})
		return
	}
	ctx.EditMessage(userId, &tg.MessagesEditMessageRequest{
		ID: msg.ID,
		Message: i18n.T(i18nk.BotMsgMediaGroupInfoGroupFoundFilesSelectStorage, map[string]any{
			"Count": len(items),
		}),
		ReplyMarkup: markup,
	})
}

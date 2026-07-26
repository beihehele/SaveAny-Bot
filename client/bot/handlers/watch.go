package handlers

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func handleWatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	parts := strings.Fields(update.EffectiveMessage.Text)
	if len(parts) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchHelpText)), nil)
		return dispatcher.EndGroups
	}
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	parsed, err := parseWatchArgs(parts[1:])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchHelpText)), nil)
		return dispatcher.EndGroups
	}
	sourceID, err := tgutil.ParseChatID(ctx, parsed.SourceArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	targetID := int64(0)
	targetTopicID := 0
	if !parsed.TargetOmitted {
		pt, err := parseTargetWithTopic(parsed.TargetArg)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTopicInvalid)), nil)
			return dispatcher.EndGroups
		}
		if pt.ChatIDArg == "0" {
			targetID = 0
		} else {
			targetID, err = tgutil.ParseChatID(ctx, pt.ChatIDArg)
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
				return dispatcher.EndGroups
			}
		}
		if targetID == 0 {
			targetTopicID = 0
		} else {
			targetTopicID = pt.TopicID
		}
	}
	if targetID == 0 && user.DefaultStorage == "" {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorDefaultStorageNotSet)), nil)
		return dispatcher.EndGroups
	}
	uctx := userclient.GetCtx()
	if uctx == nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUserbotUnavailable)), nil)
		return dispatcher.EndGroups
	}
	sourceName, err := resolveWatchChatTitle(uctx, sourceID)
	if err != nil {
		logger.Errorf("UserBot cannot access source chat %d: %s", sourceID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorSourceUnreachable, map[string]any{
			"Source": sourceID,
			"Error":  err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}
	var targetName string
	var targetTopicName string
	if targetID == 0 {
		targetName = i18n.T(i18nk.BotMsgWatchTargetLocal)
	} else {
		targetName, err = resolveWatchChatTitle(uctx, targetID)
		if err != nil {
			logger.Errorf("UserBot cannot access target chat %d: %s", targetID, err)
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTargetUnreachable, map[string]any{
				"Target": targetID,
				"Error":  err.Error(),
			})), nil)
			return dispatcher.EndGroups
		}
		if targetTopicID > 0 {
			targetTopicName, err = resolveForumTopicByID(uctx, targetID, targetTopicID)
			if err != nil {
				switch {
				case errors.Is(err, errNotForumGroup):
					ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTargetNotForum, map[string]any{"Target": targetName})), nil)
				case errors.Is(err, errTopicNotFound):
					ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTopicNotFound, map[string]any{
						"Target":  targetName,
						"TopicID": targetTopicID,
					})), nil)
				default:
					logger.Errorf("Failed to resolve forum topic %d in %d: %s", targetTopicID, targetID, err)
					ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTopicResolveFailed, map[string]any{"Error": err.Error()})), nil)
				}
				return dispatcher.EndGroups
			}
		}
	}
	filter, err := validateAndNormalizeFilter(parsed.FilterArg)
	if err != nil {
		switch {
		case errors.Is(err, errWatchFilterFormatInvalid):
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorFilterFormatInvalid)), nil)
		case errors.Is(err, errWatchFilterTypeUnsupported):
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorFilterTypeUnsupported)), nil)
		default:
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidRegex, map[string]any{"Error": err.Error()})), nil)
		}
		return dispatcher.EndGroups
	}
	watching, err := user.WatchingRoute(ctx, sourceID, targetID, targetTopicID)
	if err != nil {
		logger.Errorf("Failed to check if user is watching route %d -> %d (topic %d): %s", sourceID, targetID, targetTopicID, err)
		return dispatcher.EndGroups
	}
	if watching {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoAlreadyWatchingRoute)), nil)
		return dispatcher.EndGroups
	}
	if err := user.WatchChat(ctx, database.WatchChat{
		UserID:          user.ID,
		ChatID:          sourceID,
		SourceName:      sourceName,
		TargetID:        targetID,
		TargetName:      targetName,
		TargetTopicID:   targetTopicID,
		TargetTopicName: targetTopicName,
		Filter:          filter,
	}); err != nil {
		logger.Errorf("Failed to watch route %d -> %d (topic %d): %s", sourceID, targetID, targetTopicID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorWatchChatFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchRouteStarted, map[string]any{
		"Source": sourceName,
		"Target": formatWatchTargetDisplay(targetName, targetTopicName),
	})), nil)
	return dispatcher.EndGroups
}

func handleLswatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	chats := user.WatchChats
	if len(chats) == 0 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchListEmpty)), nil)
		return dispatcher.EndGroups
	}
	var sb strings.Builder
	sb.WriteString(i18n.T(i18nk.BotMsgWatchInfoWatchListHeader))
	for _, chat := range chats {
		targetDisplay := formatWatchTargetDisplay(chat.TargetName, chat.TargetTopicName)
		sb.WriteString(formatWatchListLine(chat.ID, chat.SourceName, targetDisplay, chat.Filter))
		sb.WriteString("\n")
	}
	ctx.Reply(update, ext.ReplyTextString(sb.String()), nil)
	return dispatcher.EndGroups
}

func handleUnwatchCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	parts := strings.Fields(update.EffectiveMessage.Text)
	if len(parts) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUnwatchNoIdProvided)), nil)
		return dispatcher.EndGroups
	}
	userChatID := update.GetUserChat().GetID()
	user, err := database.GetUserByChatID(ctx, userChatID)
	if err != nil {
		logger.Errorf("Failed to get user: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserFailed)), nil)
		return dispatcher.EndGroups
	}
	id, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUnwatchInvalidId)), nil)
		return dispatcher.EndGroups
	}
	if err := user.UnwatchByID(ctx, uint(id)); err != nil {
		logger.Errorf("Failed to unwatch id %d: %s", id, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUnwatchChatFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchInfoWatchRouteStopped, map[string]any{"ID": id})), nil)
	return dispatcher.EndGroups
}

func resolveWatchChatTitle(ctx *ext.Context, chatID int64) (string, error) {
	inputPeer, err := ctx.ResolveInputPeerById(chatID)
	if err != nil {
		return "", err
	}
	switch p := inputPeer.(type) {
	case *tg.InputPeerChannel:
		result, err := ctx.Raw.ChannelsGetChannels(ctx, []tg.InputChannelClass{
			&tg.InputChannel{ChannelID: p.ChannelID, AccessHash: p.AccessHash},
		})
		if err != nil {
			return "", err
		}
		for _, ch := range result.GetChats() {
			if c, ok := ch.(*tg.Channel); ok && c.ID == p.ChannelID {
				return c.GetTitle(), nil
			}
		}
		return "", fmt.Errorf("channel not found")
	case *tg.InputPeerChat:
		result, err := ctx.Raw.MessagesGetChats(ctx, []int64{p.ChatID})
		if err != nil {
			return "", err
		}
		for _, ch := range result.GetChats() {
			if c, ok := ch.(*tg.Chat); ok && c.ID == p.ChatID {
				return c.GetTitle(), nil
			}
		}
		return "", fmt.Errorf("chat not found")
	default:
		return "", fmt.Errorf("not a group or channel")
	}
}

func listenMediaMessageEvent(ch chan userclient.MediaMessageEvent) {
	if userclient.GetCtx() == nil {
		return
	}
	logger := log.FromContext(userclient.GetCtx())
	for event := range ch {
		logger.Debug("Received media message event", "chat_id", event.ChatID, "file_name", event.File.Name())
		ctx := event.Ctx
		file := event.File
		chats, err := database.GetWatchChatsByChatID(ctx, event.ChatID)
		if err != nil {
			logger.Errorf("Failed to get watch chats for chat ID %d: %v", event.ChatID, err)
			continue
		}
		msgText := event.File.Message().GetMessage()
		// Album parts can arrive staggered by media-group debounce; keep buffers open at least that long.
		timeout := time.Duration(max(config.C().Telegram.MediaGroupTimeout, 1)) * time.Second
		if d := userclient.MediaMessageDebounce(); d > timeout {
			timeout = d
		}
		for _, chat := range chats {
			filterMatched := watchFilterMatches(chat.Filter, msgText)
			groupID, isGroup := file.Message().GetGroupedID()
			isAlbum := isGroup && groupID != 0

			if chat.TargetID != 0 {
				if isAlbum {
					// Defer filter to album flush; forward whole group with DropAuthor + [转].
					watchForwardAlbumBuf.add(event.ChatID, chat.TargetID, chat.TargetTopicID, groupID, event.MessageID, filterMatched, timeout)
				} else if filterMatched {
					sourceID, targetID, topicID, msgID := event.ChatID, chat.TargetID, chat.TargetTopicID, event.MessageID
					go func() {
						uctx := userclient.GetCtx()
						if uctx == nil {
							return
						}
						if err := userclient.ForwardMessage(uctx, sourceID, targetID, msgID, topicID); err != nil {
							logger.Errorf("forward failed source=%d target=%d topic=%d msg=%d: %v", sourceID, targetID, topicID, msgID, err)
						}
					}()
				}
				continue
			}

			if isAlbum {
				uid := chat.UserID
				watchMediaGroupMgr.addFile(event.ChatID, uid, groupID, file, filterMatched, timeout, func(files []tfile.TGFileMessage) {
					go processWatchLocalAlbum(ctx, uid, files)
				})
				continue
			}
			if !filterMatched {
				continue
			}
			user, stor, defaultDirPath, err := resolveWatchLocalStorage(ctx, chat.UserID)
			if err != nil {
				logger.Warnf("skip local watch: %v", err)
				continue
			}
			applyWatchFilename(ctx, user, file, "")
			createWatchLocalTask(ctx, user, stor, defaultDirPath, file)
		}
	}
}

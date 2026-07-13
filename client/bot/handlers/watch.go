package handlers

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/ruleutil"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	coretfile "github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/fnamest"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/rs/xid"
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
			targetTopicName, err = resolveForumTopicByTopMsgID(uctx, targetID, targetTopicID)
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

type watchMediaGroupHandler struct {
	groups map[int64]map[uint][]tfile.TGFileMessage // chatID -> userID -> files
	timers map[int64]map[uint]*time.Timer
	mu     sync.Mutex
}

var watchMediaGroupMgr = &watchMediaGroupHandler{
	groups: make(map[int64]map[uint][]tfile.TGFileMessage),
	timers: make(map[int64]map[uint]*time.Timer),
}

func (w *watchMediaGroupHandler) addFile(chatID int64, userID uint, file tfile.TGFileMessage, timeout time.Duration, callback func([]tfile.TGFileMessage)) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.groups[chatID] == nil {
		w.groups[chatID] = make(map[uint][]tfile.TGFileMessage)
	}
	if w.timers[chatID] == nil {
		w.timers[chatID] = make(map[uint]*time.Timer)
	}

	if timer, exists := w.timers[chatID][userID]; exists {
		timer.Stop()
	}

	w.groups[chatID][userID] = append(w.groups[chatID][userID], file)

	w.timers[chatID][userID] = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		files := w.groups[chatID][userID]
		delete(w.groups[chatID], userID)
		delete(w.timers[chatID], userID)
		if len(w.groups[chatID]) == 0 {
			delete(w.groups, chatID)
		}
		if len(w.timers[chatID]) == 0 {
			delete(w.timers, chatID)
		}
		w.mu.Unlock()

		if len(files) > 0 {
			callback(files)
		}
	})
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
		for _, chat := range chats {
			if chat.Filter != "" {
				filter := strings.Split(chat.Filter, ":")
				if len(filter) != 2 {
					logger.Warnf("Invalid filter format in chat %d, skipping", chat.ChatID)
					continue
				}
				filterType := filter[0]
				filterData := filter[1]
				switch filterType {
				case "msgre": // [TODO] enums for filter types
					if ok, err := regexp.MatchString(filterData, msgText); err != nil {
						continue
					} else if !ok {
						continue
					}
				default:
					logger.Warnf("Unsupported filter type %s in chat %d, skipping", filterType, chat.ChatID)
					continue
				}
			}
			if chat.TargetID != 0 {
				groupID, isGroup := file.Message().GetGroupedID()
				timeout := time.Duration(max(config.C().Telegram.MediaGroupTimeout, 1)) * time.Second
				if isGroup && groupID != 0 {
					watchForwardAlbumBuf.add(event.ChatID, chat.TargetID, chat.TargetTopicID, groupID, event.MessageID, timeout)
				} else if err := userclient.ForwardMessagesDropAuthor(event.Ctx, event.ChatID, chat.TargetID, []int{event.MessageID}, chat.TargetTopicID); err != nil {
					logger.Errorf("forward failed source=%d target=%d topic=%d msg=%d: %v", event.ChatID, chat.TargetID, chat.TargetTopicID, event.MessageID, err)
				}
				continue
			}
			user, err := database.GetUserByID(ctx, chat.UserID)
			if err != nil {
				logger.Errorf("Failed to get user by ID %d: %v", chat.UserID, err)
				continue
			}
			if user.DefaultStorage == "" {
				logger.Warnf("User %d has no default storage set, skipping media message handling", chat.UserID)
				continue
			}
			stor, err := storage.GetStorageByUserIDAndName(ctx, user.ChatID, user.DefaultStorage)
			if err != nil {
				logger.Errorf("Failed to get storage by user ID %d and name %s: %v", user.ChatID, user.DefaultStorage, err)
				continue
			}
			// Resolve the default directory path from user.DefaultDir
			var defaultDirPath string
			if user.DefaultDir != 0 {
				dir, err := database.GetDirByID(ctx, user.DefaultDir)
				if err != nil {
					logger.Warnf("Failed to get default dir for user %d: %v, using root", user.ChatID, err)
				} else {
					defaultDirPath = dir.Path
				}
			}
			switch user.FilenameStrategy {
			case fnamest.Message.String():
				file.SetName(tgutil.GenFileNameFromMessage(*file.Message()))
			case fnamest.Template.String():
				if user.FilenameTemplate == "" {
					logger.Warnf("Empty filename template for user %d, using default filename", user.ChatID)
					break
				}
				message := file.Message()
				tmpl, err := template.New("filename").Parse(user.FilenameTemplate)
				if err != nil {
					logger.Errorf("Failed to parse filename template for user %d: %s", user.ChatID, err)
					break
				}
				data := mediautil.BuildFilenameTemplateData(message)
				var sb strings.Builder
				err = tmpl.Execute(&sb, data)
				if err != nil {
					log.FromContext(ctx).Errorf("failed to execute filename template: %s", err)
					break
				}
				file.SetName(sb.String())
			}

			// Check if this is a media group and if rules specify NEW-FOR-ALBUM
			groupID, isGroup := file.Message().GetGroupedID()
			needAlbumHandling := false
			if isGroup && groupID != 0 && user.ApplyRule && user.Rules != nil {
				_, _, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
				needAlbumHandling = matchedDirPath.NeedNewForAlbum()
			}

			if needAlbumHandling {
				// For media groups with NEW-FOR-ALBUM rule, collect all files of the same group
				watchMediaGroupMgr.addFile(event.ChatID, user.ID, file, time.Duration(max(config.C().Telegram.MediaGroupTimeout, 1))*time.Second, func(files []tfile.TGFileMessage) {
					processWatchMediaGroup(ctx, user, stor, defaultDirPath, files)
				})
				continue
			}

			// Process single file or media group without album folder creation
			dirPath := defaultDirPath
			if user.ApplyRule && user.Rules != nil {
				matched, matchedStorageName, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
				if !matched {
					goto startCreateTask
				}
				dirPath = matchedDirPath.String()
				if matchedStorageName.Usable() {
					stor, err = storage.GetStorageByUserIDAndName(ctx, user.ChatID, matchedStorageName.String())
					if err != nil {
						logger.Errorf("Failed to get storage by user ID and name: %s", err)
						continue
					}
				}
			}
		startCreateTask:
			storagePath := path.Join(dirPath, file.Name())
			injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
			taskid := xid.New().String()
			task, err := coretfile.NewTGFileTask(taskid, injectCtx, file, stor, storagePath, nil)
			if err != nil {
				logger.Errorf("create task failed: %s", err)
				continue
			}
			if err := core.AddTask(injectCtx, task); err != nil {
				logger.Errorf("add task failed: %s", err)
				continue
			}
			logger.Infof("Added media message task for user %d in chat %d: %s", chat.UserID, event.ChatID, file.Name())
		}
	}
}

func processWatchMediaGroup(ctx *ext.Context, user *database.User, stor storage.Storage, dirPath string, files []tfile.TGFileMessage) {
	logger := log.FromContext(ctx)
	if len(files) == 0 {
		return
	}

	useRule := user.ApplyRule && user.Rules != nil

	applyRule := func(file tfile.TGFileMessage) (string, ruleutil.MatchedDirPath) {
		if !useRule {
			return stor.Name(), ruleutil.MatchedDirPath(dirPath)
		}
		matched, storName, dirP := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
		if !matched {
			return stor.Name(), ruleutil.MatchedDirPath(dirPath)
		}
		storname := storName.String()
		if !storName.Usable() {
			storname = stor.Name()
		}
		return storname, dirP
	}

	type albumFile struct {
		file    tfile.TGFileMessage
		storage storage.Storage
		dirPath string
	}
	albumFiles := make(map[int64][]albumFile)

	// Collect files by group ID
	for _, file := range files {
		storName, ruleDirPath := applyRule(file)
		fileStor := stor
		if storName != stor.Name() && storName != "" {
			var err error
			fileStor, err = storage.GetStorageByUserIDAndName(ctx, user.ChatID, storName)
			if err != nil {
				logger.Errorf("Failed to get storage by user ID and name: %s", err)
				continue
			}
		}

		groupId, isGroup := file.Message().GetGroupedID()
		if !isGroup || groupId == 0 {
			logger.Warnf("File %s is not in a group, skipping", file.Name())
			continue
		}

		// Use the effective dirPath: if rule returns NEW-FOR-ALBUM sentinel, fall back to the
		// base dirPath passed in (which is defaultDirPath from the caller).
		effectiveDirPath := string(ruleDirPath)
		if ruleDirPath.NeedNewForAlbum() {
			effectiveDirPath = dirPath
		}

		if _, ok := albumFiles[groupId]; !ok {
			albumFiles[groupId] = make([]albumFile, 0)
		}
		albumFiles[groupId] = append(albumFiles[groupId], albumFile{
			file:    file,
			storage: fileStor,
			dirPath: effectiveDirPath,
		})
	}

	// Process album files with folder creation
	injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
	totalTasks := 0
	for groupID, afiles := range albumFiles {
		if len(afiles) <= 1 {
			continue
		}

		// Use first file's name (without extension) as album folder name
		albumDir := strings.TrimSuffix(path.Base(afiles[0].file.Name()), path.Ext(afiles[0].file.Name()))
		albumStor := afiles[0].storage

		logger.Infof("Creating album folder for group %d: %s with %d files", groupID, albumDir, len(afiles))

		for _, af := range afiles {
			afstorPath := path.Join(af.dirPath, albumDir, af.file.Name())
			taskid := xid.New().String()
			task, err := coretfile.NewTGFileTask(taskid, injectCtx, af.file, albumStor, afstorPath, nil)
			if err != nil {
				logger.Errorf("create task failed for album file: %s", err)
				continue
			}
			if err := core.AddTask(injectCtx, task); err != nil {
				logger.Errorf("add task failed: %s", err)
				continue
			}
			totalTasks++
		}
	}
	logger.Infof("Added %d watch media tasks for user %d", totalTasks, user.ChatID)
}

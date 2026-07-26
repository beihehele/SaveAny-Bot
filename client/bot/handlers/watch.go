package handlers

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
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

type watchLocalAlbumKey struct {
	ChatID    int64
	UserID    uint
	GroupedID int64
}

type watchLocalAlbumGroup struct {
	files   []tfile.TGFileMessage
	matched bool
}

type watchMediaGroupHandler struct {
	groups  map[watchLocalAlbumKey]*watchLocalAlbumGroup
	timers  map[watchLocalAlbumKey]*time.Timer
	mu      sync.Mutex
}

var watchMediaGroupMgr = &watchMediaGroupHandler{
	groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup),
	timers: make(map[watchLocalAlbumKey]*time.Timer),
}

func (w *watchMediaGroupHandler) addFile(chatID int64, userID uint, groupedID int64, file tfile.TGFileMessage, filterMatched bool, timeout time.Duration, callback func([]tfile.TGFileMessage)) {
	w.mu.Lock()
	defer w.mu.Unlock()

	key := watchLocalAlbumKey{ChatID: chatID, UserID: userID, GroupedID: groupedID}
	if timer, exists := w.timers[key]; exists {
		timer.Stop()
	}

	g := w.groups[key]
	if g == nil {
		g = &watchLocalAlbumGroup{}
		w.groups[key] = g
	}
	g.files = append(g.files, file)
	if filterMatched {
		g.matched = true
	}

	w.timers[key] = time.AfterFunc(timeout, func() {
		w.mu.Lock()
		g := w.groups[key]
		delete(w.groups, key)
		delete(w.timers, key)
		w.mu.Unlock()

		if g == nil || len(g.files) == 0 || !g.matched {
			return
		}
		callback(g.files)
	})
}

func albumCaptionText(files []tfile.TGFileMessage) string {
	for _, f := range files {
		if msg := f.Message(); msg != nil {
			if text := strings.TrimSpace(msg.GetMessage()); text != "" {
				return text
			}
		}
	}
	return ""
}

func applyWatchFilename(ctx context.Context, user *database.User, file tfile.TGFileMessage, albumCaption string) {
	msg := file.Message()
	if msg == nil {
		return
	}
	namingMsg := *msg
	usedAlbumCaption := false
	if strings.TrimSpace(namingMsg.GetMessage()) == "" && albumCaption != "" {
		namingMsg.Message = albumCaption
		usedAlbumCaption = true
	}
	switch user.FilenameStrategy {
	case fnamest.Message.String():
		name := tgutil.GenFileNameFromMessage(namingMsg)
		// Plain-text captions omit msg id; album siblings would collide without it.
		if usedAlbumCaption {
			name = uniqueAlbumFileName(name, namingMsg.GetID())
		}
		file.SetName(name)
	case fnamest.Template.String():
		if user.FilenameTemplate == "" {
			log.FromContext(ctx).Warnf("Empty filename template for user %d, using default filename", user.ChatID)
			return
		}
		tmpl, err := template.New("filename").Parse(user.FilenameTemplate)
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to parse filename template for user %d: %s", user.ChatID, err)
			return
		}
		data := mediautil.BuildFilenameTemplateData(&namingMsg)
		var sb strings.Builder
		if err := tmpl.Execute(&sb, data); err != nil {
			log.FromContext(ctx).Errorf("failed to execute filename template: %s", err)
			return
		}
		name := sb.String()
		if usedAlbumCaption {
			name = uniqueAlbumFileName(name, namingMsg.GetID())
		}
		file.SetName(name)
	default:
		// Default keeps document filenames; for captionless album parts still prefer
		// shared caption naming over opaque originals when a caption exists.
		if usedAlbumCaption {
			file.SetName(uniqueAlbumFileName(tgutil.GenFileNameFromMessage(namingMsg), namingMsg.GetID()))
		}
	}
}

func uniqueAlbumFileName(name string, msgID int) string {
	if msgID <= 0 || name == "" {
		return name
	}
	ext := path.Ext(name)
	base := strings.TrimSuffix(name, ext)
	idSuffix := fmt.Sprintf("_%d", msgID)
	if strings.HasSuffix(base, idSuffix) {
		return name
	}
	return base + idSuffix + ext
}

func resolveWatchLocalStorage(ctx context.Context, userID uint) (*database.User, storage.Storage, string, error) {
	user, err := database.GetUserByID(ctx, userID)
	if err != nil {
		return nil, nil, "", fmt.Errorf("get user: %w", err)
	}
	if user.DefaultStorage == "" {
		return nil, nil, "", fmt.Errorf("user %d has no default storage", user.ChatID)
	}
	stor, err := storage.GetStorageByUserIDAndName(ctx, user.ChatID, user.DefaultStorage)
	if err != nil {
		return nil, nil, "", err
	}
	var defaultDirPath string
	if user.DefaultDir != 0 {
		dir, err := database.GetDirByID(ctx, user.DefaultDir)
		if err != nil {
			log.FromContext(ctx).Warnf("Failed to get default dir for user %d: %v, using root", user.ChatID, err)
		} else {
			defaultDirPath = dir.Path
		}
	}
	return user, stor, defaultDirPath, nil
}

func createWatchLocalTask(ctx *ext.Context, user *database.User, stor storage.Storage, defaultDirPath string, file tfile.TGFileMessage) {
	logger := log.FromContext(ctx)
	dirPath := defaultDirPath
	fileStor := stor
	if user.ApplyRule && user.Rules != nil {
		matched, matchedStorageName, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(file))
		if matched {
			dirPath = matchedDirPath.String()
			if matchedStorageName.Usable() {
				var err error
				fileStor, err = storage.GetStorageByUserIDAndName(ctx, user.ChatID, matchedStorageName.String())
				if err != nil {
					logger.Errorf("Failed to get storage by user ID and name: %s", err)
					return
				}
			}
		}
	}
	storagePath := path.Join(dirPath, file.Name())
	injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
	taskid := xid.New().String()
	task, err := coretfile.NewTGFileTask(taskid, injectCtx, file, fileStor, storagePath, nil)
	if err != nil {
		logger.Errorf("create task failed: %s", err)
		return
	}
	if err := core.AddTask(injectCtx, task); err != nil {
		logger.Errorf("add task failed: %s", err)
		return
	}
	logger.Infof("Added media message task for user %d: %s", user.ChatID, file.Name())
}

func processWatchLocalAlbum(ctx *ext.Context, userID uint, files []tfile.TGFileMessage) {
	logger := log.FromContext(ctx)
	user, stor, defaultDirPath, err := resolveWatchLocalStorage(ctx, userID)
	if err != nil {
		logger.Warnf("skip local album: %v", err)
		return
	}
	caption := albumCaptionText(files)
	for _, f := range files {
		applyWatchFilename(ctx, user, f, caption)
	}
	needAlbumHandling := false
	if user.ApplyRule && user.Rules != nil && len(files) > 0 {
		_, _, matchedDirPath := ruleutil.ApplyRule(ctx, user.Rules, ruleutil.NewInput(files[0]))
		needAlbumHandling = matchedDirPath.NeedNewForAlbum()
	}
	if needAlbumHandling {
		processWatchMediaGroup(ctx, user, stor, defaultDirPath, files)
		return
	}
	for _, f := range files {
		createWatchLocalTask(ctx, user, stor, defaultDirPath, f)
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
					// Defer filter to album flush; forward whole group with DropAuthor + [转自].
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

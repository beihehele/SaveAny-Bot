package handlers

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/ruleutil"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core"
	coretfile "github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/fnamest"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/rs/xid"
)

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
	groups map[watchLocalAlbumKey]*watchLocalAlbumGroup
	timers map[watchLocalAlbumKey]*time.Timer
	mu     sync.Mutex
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

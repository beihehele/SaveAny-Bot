package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/shortcut"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/storage"
	"gorm.io/gorm"
)

func handleAddCallback(ctx *ext.Context, update *ext.Update) error {
	dataParts := strings.Split(string(update.CallbackQuery.Data), " ")
	if len(dataParts) < 2 || dataParts[1] == "" {
		return fmt.Errorf("invalid callback data: %q", update.CallbackQuery.Data)
	}
	dataid := dataParts[1]
	data, err := shortcut.GetCallbackDataWithAnswer[tcbdata.Add](ctx, update, dataid)
	if err != nil {
		return err
	}
	queryID := update.CallbackQuery.GetQueryID()
	msgID := update.CallbackQuery.GetMsgID()
	userID := update.CallbackQuery.GetUserID()

	selectedStorage, err := storage.GetStorageByUserIDAndName(ctx, userID, data.SelectedStorName)
	if err != nil {
		log.FromContext(ctx).Errorf("Failed to get storage: %s", err)
		ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetStorageFailed, map[string]any{
			"Error": err.Error(),
		})))
		return dispatcher.EndGroups
	}
	dirs, err := database.GetDirsByUserChatIDAndStorageName(ctx, userID, data.SelectedStorName)
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("failed to get user directories: %w", err)
	}

	if !data.SettedDir && len(dirs) != 0 {
		// ask for directory selection
		markup, err := msgelem.BuildSetDirMarkupForAdd(dirs, dataid)
		if err != nil {
			log.FromContext(ctx).Errorf("Failed to build directory keyboard: %s", err)
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorBuildStorageSelectKeyboardFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		ctx.EditMessage(userID, &tg.MessagesEditMessageRequest{
			ID:          update.CallbackQuery.GetMsgID(),
			Message:     i18n.T(i18nk.BotMsgCommonPromptSelectDir, nil),
			ReplyMarkup: markup,
		})
		return dispatcher.EndGroups
	}

	dirPath := ""
	if data.DirID != 0 {
		dir, err := database.GetDirByID(ctx, data.DirID)
		if err != nil {
			ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorGetDirFailed, map[string]any{
				"Error": err.Error(),
			})))
			return dispatcher.EndGroups
		}
		dirPath = dir.Path
	} else if data.SelectedDirPath != "" {
		dirPath = data.SelectedDirPath
	}

	switch data.TaskType {
	case tasktype.TaskTypeTgfiles:
		if data.AsBatch {
			return shortcut.CreateAndAddBatchTGFileTaskWithEdit(ctx, userID, selectedStorage, dirPath, data.Files, msgID, data.ConflictStrategy)
		}
		return shortcut.CreateAndAddTGFileTaskWithEdit(ctx, userID, selectedStorage, dirPath, data.Files[0], msgID, data.ConflictStrategy)
	default:
		return fmt.Errorf("unexcept task type: %s", data.TaskType)
	}
}

package handlers

import (
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
)

func handleCancelCallback(ctx *ext.Context, update *ext.Update) error {
	taskid := strings.Split(string(update.CallbackQuery.Data), " ")[1]
	if err := core.CancelTask(ctx, taskid); err != nil {
		log.FromContext(ctx).Errorf("Failed to cancel task %s: %v", taskid, err)
		ctx.AnswerCallback(msgelem.AlertCallbackAnswer(update.CallbackQuery.GetQueryID(), i18n.T(i18nk.BotMsgCancelErrorCancelFailed, map[string]any{
			"Error": err.Error(),
		})))
		return dispatcher.EndGroups
	}
	endCopySlotAfterCancel(taskid)

	ctx.EditMessage(update.CallbackQuery.GetUserID(), &tg.MessagesEditMessageRequest{
		ID:      update.CallbackQuery.GetMsgID(),
		Message: i18n.T(i18nk.BotMsgCancelInfoCancellingTask, nil),
	})

	return dispatcher.EndGroups
}

func handleCancelCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	args := strings.Fields(update.EffectiveMessage.Text)
	if len(args) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCancelUsage, nil)), nil)
		return dispatcher.EndGroups
	}
	taskID := args[1]
	if err := core.CancelTask(ctx, taskID); err != nil {
		logger.Errorf("failed to cancel task %s: %v", taskID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCancelErrorCancelFailed, map[string]any{
			"Error": err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}
	endCopySlotAfterCancel(taskID)
	ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCancelInfoCancelRequested, map[string]any{
		"TaskID": taskID,
	})), nil)
	return dispatcher.EndGroups
}

// endCopySlotAfterCancel releases the per-user /copy slot only when the task
// never entered Execute (still queued). Running tasks keep the slot until
// Execute's defer End, so a second /copy cannot overlap in-flight work.
func endCopySlotAfterCancel(taskID string) {
	if core.IsTaskExecuting(taskID) {
		return
	}
	copyfwd.EndByTaskID(taskID)
}

package handlers

import (
	"errors"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
	"github.com/rs/xid"
)

func handleCopyCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	parts := strings.Fields(update.EffectiveMessage.Text)
	if len(parts) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCopyHelpText)), nil)
		return dispatcher.EndGroups
	}

	parsed, err := parseCopyArgs(parts[1:])
	if err != nil {
		switch {
		case errors.Is(err, errCopyTargetLocalForbidden):
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCopyErrorTargetForbidden)), nil)
		default:
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCopyHelpText)), nil)
		}
		return dispatcher.EndGroups
	}

	sourceID, err := tgutil.ParseChatID(ctx, parsed.SourceArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}

	pt, err := parseTargetWithTopic(parsed.TargetArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTopicInvalid)), nil)
		return dispatcher.EndGroups
	}
	targetID, err := tgutil.ParseChatID(ctx, pt.ChatIDArg)
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	if targetID == 0 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCopyErrorTargetForbidden)), nil)
		return dispatcher.EndGroups
	}
	targetTopicID := pt.TopicID

	uctx := userclient.GetCtx()
	if uctx == nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUserbotUnavailable)), nil)
		return dispatcher.EndGroups
	}

	if _, err := resolveWatchChatTitle(uctx, sourceID); err != nil {
		logger.Errorf("UserBot cannot access source chat %d: %s", sourceID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorSourceUnreachable, map[string]any{
			"Source": sourceID,
			"Error":  err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}

	targetName, err := resolveWatchChatTitle(uctx, targetID)
	if err != nil {
		logger.Errorf("UserBot cannot access target chat %d: %s", targetID, err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorTargetUnreachable, map[string]any{
			"Target": targetID,
			"Error":  err.Error(),
		})), nil)
		return dispatcher.EndGroups
	}
	if targetTopicID > 0 {
		if _, err := resolveForumTopicByID(uctx, targetID, targetTopicID); err != nil {
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

	userChatID := update.GetUserChat().GetID()
	taskID := xid.New().String()
	if !copyfwd.TryBegin(userChatID, taskID) {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCopyErrorAlreadyRunning)), nil)
		return dispatcher.EndGroups
	}

	msg, err := ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgProgressCopyScanning, map[string]any{
		"Matched": 0,
		"Count":   parsed.Count,
		"At":      0,
	})), &ext.ReplyOpts{
		Markup: &tg.ReplyInlineMarkup{
			Rows: []tg.KeyboardButtonRow{
				{
					Buttons: []tg.KeyboardButtonClass{
						tgutil.BuildCancelButton(taskID),
					},
				},
			},
		},
	})
	if err != nil {
		copyfwd.End(userChatID, taskID)
		logger.Errorf("Failed to send copy progress message: %s", err)
		return dispatcher.EndGroups
	}

	progress := copyfwd.NewProgressTracker(msg.ID, userChatID, taskID)
	task := copyfwd.NewTask(
		taskID,
		userChatID,
		sourceID,
		targetID,
		targetTopicID,
		filter,
		parsed.Count,
		userChatID,
		msg.ID,
		progress,
	)
	injectCtx := tgutil.ExtWithContext(ctx.Context, ctx)
	if err := core.AddTask(injectCtx, task); err != nil {
		copyfwd.End(userChatID, taskID)
		logger.Errorf("Failed to add copy task: %s", err)
		ctx.EditMessage(userChatID, &tg.MessagesEditMessageRequest{
			ID: msg.ID,
			Message: i18n.T(i18nk.BotMsgCommonErrorTaskAddFailed, map[string]any{
				"Error": err.Error(),
			}),
		})
		return dispatcher.EndGroups
	}
	return dispatcher.EndGroups
}

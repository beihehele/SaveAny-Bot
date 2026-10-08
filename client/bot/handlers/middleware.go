package handlers

import (
	"errors"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/duke-git/lancet/v2/slice"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/dirutil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
)

func responsibleUserID(update *ext.Update) int64 {
	if update.CallbackQuery != nil {
		return update.CallbackQuery.GetUserID()
	}
	return update.GetUserChat().GetID()
}

func checkPermission(ctx *ext.Context, update *ext.Update) error {
	userID := responsibleUserID(update)
	if !slice.Contain(config.C().GetUsersID(), userID) {
		if cbq := update.CallbackQuery; cbq != nil {
			if _, err := ctx.AnswerCallback(msgelem.AlertCallbackAnswer(cbq.GetQueryID(), i18n.T(i18nk.BotMsgCommonErrorNoPermission, nil))); err != nil {
				log.FromContext(ctx).Warnf("Failed to answer unauthorized callback: %v", err)
			}
		} else {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorNoPermission, nil)), nil)
		}
		return dispatcher.EndGroups
	}

	return dispatcher.ContinueGroups
}

// ContinueGroups is the dispatcher's success sentinel; it must allow the
// wrapped handler to run even though it is represented as an error.
func withPermission(handler func(*ext.Context, *ext.Update) error) func(*ext.Context, *ext.Update) error {
	return func(ctx *ext.Context, update *ext.Update) error {
		if err := checkPermission(ctx, update); err != nil && !errors.Is(err, dispatcher.ContinueGroups) {
			return err
		}
		return handler(ctx, update)
	}
}

func handleSilentMode(next func(*ext.Context, *ext.Update) error, handler func(*ext.Context, *ext.Update) error) func(*ext.Context, *ext.Update) error {
	return func(ctx *ext.Context, update *ext.Update) error {
		userID := update.GetUserChat().GetID()
		user, err := database.GetUserByChatID(ctx, userID)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetUserInfoFailed, map[string]any{
				"Error": err.Error(),
			})), nil)
			return dispatcher.EndGroups
		}
		if !user.Silent {
			return next(ctx, update)
		}
		if user.DefaultStorage == "" {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorDefaultStorageNotSet, nil)), nil)
			return next(ctx, update)
		}
		stor, err := storage.GetStorageByUserIDAndName(ctx, userID, user.DefaultStorage)
		if err != nil {
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetStorageFailed, map[string]any{
				"Error": err.Error(),
			})), nil)
			return dispatcher.EndGroups
		}
		if user.DefaultDir != 0 {
			dir, err := database.GetDirByID(ctx, user.DefaultDir)
			if err != nil {
				ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetDirFailed, map[string]any{
					"Error": err.Error(),
				})), nil)
				return next(ctx, update)
			}
			ctx.Context = dirutil.WithContext(ctx.Context, dir)
		}
		ctx.Context = storage.WithContext(ctx.Context, stor)
		return handler(ctx, update)
	}
}

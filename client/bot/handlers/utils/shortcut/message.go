// Some shortcuts for duplicate code in handlers, they should return dispatcher errors
package shortcut

import (
	"context"
	"net/url"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/types"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/msgelem"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/re"
	uc "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

// 获取消息中的文件并回复等待消息, 返回等待消息, 获取到的文件
func GetFileFromMessageWithReply(ctx *ext.Context, update *ext.Update, message *tg.Message, tfileopts ...tfile.TGFileOption) (replied *types.Message,
	file tfile.TGFileMessage, err error,
) {
	logger := log.FromContext(ctx)
	media := message.Media
	supported := mediautil.IsSupported(media)
	if !supported {
		return nil, nil, dispatcher.ContinueGroups
	}

	replied, err = ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonInfoFetchingFileInfo, nil)), nil)
	if err != nil {
		logger.Errorf("Failed to reply: %s", err)
		return nil, nil, dispatcher.EndGroups
	}
	file, err = tfile.FromMediaMessage(media, ctx.Raw, message, tfileopts...)
	if err != nil {
		logger.Errorf("Failed to get file from media: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorGetFileFailed, map[string]any{
			"Error": err.Error(),
		})), nil)
		return nil, nil, dispatcher.EndGroups
	}
	return replied, file, nil
}

type EditMessageFunc func(text string, markup tg.ReplyMarkupClass)

// 获取链接中的文件并回复等待消息
func GetFilesFromUpdateLinkMessageWithReplyEdit(ctx *ext.Context, update *ext.Update) (replied *types.Message, files []tfile.TGFileMessage, editReplied EditMessageFunc, err error) {
	logger := log.FromContext(ctx)
	msgLinks := re.TgMessageLinkRegexp.FindAllString(tgutil.ExtractMessageEntityUrlsText(update.EffectiveMessage.Message), -1)
	if len(msgLinks) == 0 {
		logger.Warn("no matched message links but called handleMessageLink")
		return nil, nil, nil, dispatcher.EndGroups
	}
	replied, err = ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonInfoFetchingMessages, nil)), nil)
	if err != nil {
		logger.Errorf("failed to reply: %s", err)
		return nil, nil, nil, dispatcher.EndGroups
	}
	editReplied = func(text string, markup tg.ReplyMarkupClass) {
		if _, err := ctx.EditMessage(update.EffectiveChat().GetID(), &tg.MessagesEditMessageRequest{
			ID:          replied.ID,
			Message:     text,
			ReplyMarkup: markup,
		}); err != nil {
			logger.Errorf("failed to edit message: %s", err)
		}
	}
	user, err := database.GetUserByChatID(ctx, update.GetUserChat().GetID())
	if err != nil {
		logger.Errorf("failed to get user from db: %s", err)
		editReplied(i18n.T(i18nk.BotMsgCommonErrorGetUserInfoFailed, map[string]any{
			"Error": err.Error(),
		}), nil)
		return nil, nil, nil, dispatcher.EndGroups
	}
	var selected tfile.LinkedFiles
	var report inputresult.Report
	addFile := func(chatID int64, client downloader.Client, msg *tg.Message) string {
		if msg == nil || msg.Media == nil {
			logger.Warn("message is nil, skipping")
			return "no_media"
		}
		opts := mediautil.TfileOptions(ctx, user, msg)
		err := selected.Add(chatID, client, msg, opts...)
		if err != nil {
			logger.Errorf("failed to create file from media: %s", err)
			return "unsupported_media"
		}
		return "ready"
	}

	tctx := ctx
	if config.C().Telegram.Userbot.Enable {
		if uc.GetCtx() != nil {
			tctx = uc.GetCtx()
		}
	}

	for index, link := range msgLinks {
		linkUrl, err := url.Parse(link)
		if err != nil {
			logger.Errorf("failed to parse message link %s: %s", link, err)
			report.Add(index+1, "resolution_failed", 0)
			continue
		}
		chatId, msgId, err := tgutil.ParseMessageLink(tctx, link)
		if err != nil {
			logger.Errorf("failed to parse message link %s: %s", link, err)
			report.Add(index+1, "resolution_failed", 0)
			continue
		}
		msg, err := tgutil.GetMessageByID(tctx, chatId, msgId)
		if err != nil || msg == nil {
			logger.Error(err)
			report.Add(index+1, "message_unavailable", 0)
			continue
		}
		before := len(selected.Files())
		code := "ready"
		groupID, isGroup := msg.GetGroupedID()
		if isGroup && groupID != 0 && !linkUrl.Query().Has("single") {
			gmsgs, err := tgutil.GetGroupedMessages(tctx, chatId, msg)
			if err != nil {
				logger.Errorf("failed to get grouped messages: %s", err)
				code = "album_unavailable"
			} else {
				if len(gmsgs) == 0 {
					code = "no_media"
				}
				for _, gmsg := range gmsgs {
					if result := addFile(chatId, tctx.Raw, gmsg); result != "ready" {
						code = result
					}
				}
			}
		} else {
			code = addFile(chatId, tctx.Raw, msg)
		}
		report.Add(index+1, code, len(selected.Files())-before)
	}
	// This separate message leaves selection keyboards, entities and captions untouched.
	if text := inputWarnings(report); text != "" {
		notifyCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		_, notifyErr := tgutil.ClientWithContext(notifyCtx, ctx).Reply(update, ext.ReplyTextString(text), nil)
		stop()
		if notifyErr != nil {
			logger.Warn("Failed to report link selection warnings", "error", notifyErr)
		}
	}
	files = selected.Files()
	if len(files) == 0 {
		editReplied(i18n.T(i18nk.BotMsgCommonErrorNoSavableFilesFound, nil), nil)
		return nil, nil, nil, dispatcher.EndGroups
	}
	return replied, files, editReplied, nil
}

func GetCallbackDataWithAnswer[DataType any](ctx *ext.Context, update *ext.Update, dataid string) (DataType, error) {
	data, ok := cache.Get[DataType](dataid)
	if !ok {
		log.FromContext(ctx).Warnf("Invalid data ID: %s", dataid)
		queryID := update.CallbackQuery.GetQueryID()
		ctx.AnswerCallback(msgelem.AlertCallbackAnswer(queryID, i18n.T(i18nk.BotMsgCommonErrorDataExpired, nil)))
		var zero DataType
		return zero, dispatcher.EndGroups
	}
	return data, nil
}

package handlers

import (
	"errors"
	"fmt"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
)

func handleLstopicCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	parts := strings.Fields(update.EffectiveMessage.Text)
	if len(parts) < 2 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLstopicHelpText)), nil)
		return dispatcher.EndGroups
	}
	uctx, ok := requireUserbot(ctx, update)
	if !ok {
		return dispatcher.EndGroups
	}
	groupID, err := tgutil.ParseChatID(ctx, parts[1])
	if err != nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgCommonErrorInvalidIdOrUsername, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	entries, err := collectForumTopics(uctx, groupID)
	if err != nil {
		switch {
		case errors.Is(err, errNotForumGroup):
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLstopicErrorNotForum, map[string]any{"Group": groupID})), nil)
		default:
			logger.Errorf("Failed to list forum topics for %d: %s", groupID, err)
			ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLstopicErrorListFailed, map[string]any{"Error": err.Error()})), nil)
		}
		return dispatcher.EndGroups
	}
	if len(entries) == 0 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLstopicInfoListEmpty)), nil)
		return dispatcher.EndGroups
	}
	var sb strings.Builder
	sb.WriteString(i18n.T(i18nk.BotMsgLstopicInfoListHeader))
	for _, entry := range entries {
		fmt.Fprintf(&sb, "%s -> %d\n", entry.title, entry.topMsgID)
	}
	ctx.Reply(update, ext.ReplyTextString(strings.TrimRight(sb.String(), "\n")), nil)
	return dispatcher.EndGroups
}

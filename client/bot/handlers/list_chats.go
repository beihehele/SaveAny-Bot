package handlers

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/telegram/query/dialogs"
	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/config"
)

const botAPIChannelPrefix = -1000000000000

type dialogListEntry struct {
	title    string
	botAPIID int64
}

func requireUserbot(ctx *ext.Context, update *ext.Update) (*ext.Context, bool) {
	if !config.C().Telegram.Userbot.Enable {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUserbotUnavailable)), nil)
		return nil, false
	}
	uctx := userclient.GetCtx()
	if uctx == nil {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgWatchErrorUserbotUnavailable)), nil)
		return nil, false
	}
	return uctx, true
}

func collectDialogEntries(ctx context.Context, uctx *ext.Context, listChannels bool) ([]dialogListEntry, error) {
	seen := make(map[int64]struct{})
	var entries []dialogListEntry
	err := dialogs.NewQueryBuilder(uctx.Raw).GetDialogs().BatchSize(100).ForEach(ctx, func(ctx context.Context, e dialogs.Elem) error {
		peer, ok := e.Dialog.GetPeer().(tg.PeerClass)
		if !ok {
			return nil
		}
		switch p := peer.(type) {
		case *tg.PeerChannel:
			ch, ok := e.Entities.Channel(p.ChannelID)
			if !ok {
				return nil
			}
			isBroadcast := ch.GetBroadcast() && !ch.GetMegagroup()
			isGroup := ch.GetMegagroup()
			if listChannels && !isBroadcast {
				return nil
			}
			if !listChannels && !isGroup {
				return nil
			}
			botAPIID := botAPIChannelPrefix - p.ChannelID
			if _, dup := seen[botAPIID]; dup {
				return nil
			}
			seen[botAPIID] = struct{}{}
			entries = append(entries, dialogListEntry{title: ch.GetTitle(), botAPIID: botAPIID})
		case *tg.PeerChat:
			if listChannels {
				return nil
			}
			chat, ok := e.Entities.Chat(p.ChatID)
			if !ok {
				return nil
			}
			botAPIID := -p.ChatID
			if _, dup := seen[botAPIID]; dup {
				return nil
			}
			seen[botAPIID] = struct{}{}
			entries = append(entries, dialogListEntry{title: chat.GetTitle(), botAPIID: botAPIID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].title == entries[j].title {
			return entries[i].botAPIID < entries[j].botAPIID
		}
		return entries[i].title < entries[j].title
	})
	return entries, nil
}

func replyDialogList(ctx *ext.Context, update *ext.Update, entries []dialogListEntry, emptyKey, headerKey i18nk.Key) error {
	if len(entries) == 0 {
		ctx.Reply(update, ext.ReplyTextString(i18n.T(emptyKey)), nil)
		return dispatcher.EndGroups
	}
	var sb strings.Builder
	sb.WriteString(i18n.T(headerKey))
	for _, entry := range entries {
		fmt.Fprintf(&sb, "%s -> %d\n", entry.title, entry.botAPIID)
	}
	ctx.Reply(update, ext.ReplyTextString(strings.TrimRight(sb.String(), "\n")), nil)
	return dispatcher.EndGroups
}

func handleLschannelCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	uctx, ok := requireUserbot(ctx, update)
	if !ok {
		return dispatcher.EndGroups
	}
	entries, err := collectDialogEntries(ctx, uctx, true)
	if err != nil {
		logger.Errorf("Failed to list channels: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLschannelErrorListFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	return replyDialogList(ctx, update, entries, i18nk.BotMsgLschannelInfoListEmpty, i18nk.BotMsgLschannelInfoListHeader)
}

func handleLsgroupCmd(ctx *ext.Context, update *ext.Update) error {
	logger := log.FromContext(ctx)
	uctx, ok := requireUserbot(ctx, update)
	if !ok {
		return dispatcher.EndGroups
	}
	entries, err := collectDialogEntries(ctx, uctx, false)
	if err != nil {
		logger.Errorf("Failed to list groups: %s", err)
		ctx.Reply(update, ext.ReplyTextString(i18n.T(i18nk.BotMsgLsgroupErrorListFailed, map[string]any{"Error": err.Error()})), nil)
		return dispatcher.EndGroups
	}
	return replyDialogList(ctx, update, entries, i18nk.BotMsgLsgroupInfoListEmpty, i18nk.BotMsgLsgroupInfoListHeader)
}

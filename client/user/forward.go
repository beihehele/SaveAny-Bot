package user

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"unicode/utf16"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/message/entity"
	"github.com/gotd/td/telegram/message/styling"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
)

const attributionLabel = "[转]"

// ForwardMessagesDropAuthor copies messages with DropAuthor (independent of source deletion),
// then prepends a clickable [转] link on the caption/text message pointing to sourceLinkMsgID.
// rpcCtx is used for Telegram RPCs so callers (e.g. /copy) can cancel FLOOD_WAIT sleeps.
func ForwardMessagesDropAuthor(rpcCtx context.Context, ectx *ext.Context, fromChatID, toChatID int64, messageIDs []int, topMsgID, sourceLinkMsgID int) error {
	_, err := CopyMessages(rpcCtx, ectx, fromChatID, toChatID, messageIDs, topMsgID, sourceLinkMsgID)
	return err
}

// CopyMessages is the shared DropAuthor implementation and returns confirmed target messages.
func CopyMessages(rpcCtx context.Context, ectx *ext.Context, fromChatID, toChatID int64, messageIDs []int, topMsgID, sourceLinkMsgID int) ([]*tg.Message, error) {
	if ectx == nil {
		return nil, fmt.Errorf("user context is nil")
	}
	if rpcCtx == nil {
		rpcCtx = ectx
	}
	if err := rpcCtx.Err(); err != nil {
		return nil, err
	}
	ectx = tgutil.ClientWithContext(rpcCtx, ectx)
	if len(messageIDs) == 0 {
		return nil, fmt.Errorf("empty message ids")
	}
	ids := append([]int(nil), messageIDs...)
	sort.Ints(ids)
	if sourceLinkMsgID == 0 {
		sourceLinkMsgID = ids[0]
	}

	fromPeer, err := resolveInputPeer(ectx, fromChatID)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve source peer %d: %w", fromChatID, err)
	}
	toPeer, err := resolveInputPeer(ectx, toChatID)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve target peer %d: %w", toChatID, err)
	}

	randomIDs := make([]int64, len(ids))
	for i := range randomIDs {
		randomIDs[i] = randomID()
	}

	req := &tg.MessagesForwardMessagesRequest{
		DropAuthor: true,
		FromPeer:   fromPeer,
		ID:         ids,
		RandomID:   randomIDs,
		ToPeer:     toPeer,
	}
	if topMsgID > 0 {
		req.SetTopMsgID(topMsgID)
	}

	updates, err := ectx.Raw.MessagesForwardMessages(rpcCtx, req)
	if err != nil {
		return nil, fmt.Errorf("forward messages: %w", err)
	}

	link, err := tgutil.BuildMessageLink(ectx, fromChatID, sourceLinkMsgID)
	if err != nil {
		log.FromContext(rpcCtx).Warnf("build source link for attribution: %v", err)
		return MessagesFromUpdates(updates), nil
	}
	if err := AppendAttribution(rpcCtx, ectx, toPeer, updates, link); err != nil {
		// Content already copied independently; attribution is best-effort.
		log.FromContext(rpcCtx).Warnf("prepend [转] after forward: %v", err)
	}
	return MessagesFromUpdates(updates), nil
}

// ForwardMessage copies one message (or its whole album) with DropAuthor and [转] link.
func ForwardMessage(rpcCtx context.Context, ectx *ext.Context, fromChatID, toChatID int64, msgID, topMsgID int) error {
	if ectx == nil {
		return fmt.Errorf("user context is nil")
	}
	if rpcCtx == nil {
		rpcCtx = ectx
	}
	if err := rpcCtx.Err(); err != nil {
		return err
	}
	ectx = tgutil.ClientWithContext(rpcCtx, ectx)
	msg, err := tgutil.GetMessageByID(ectx, fromChatID, msgID)
	if err != nil {
		if ctxErr := rpcCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("get source message: %w", err)
	}
	ids := []int{msgID}
	linkID := msgID
	if gid, ok := msg.GetGroupedID(); ok && gid != 0 {
		group, err := tgutil.GetGroupedMessages(ectx, fromChatID, msg)
		if ctxErr := rpcCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err == nil && len(group) > 0 {
			ids = make([]int, 0, len(group))
			for _, m := range group {
				ids = append(ids, m.GetID())
				if strings.TrimSpace(m.GetMessage()) != "" {
					linkID = m.GetID()
				}
			}
			sort.Ints(ids)
		} else {
			// Preserve the existing fallback, but make its reduced scope visible.
			reason := "empty_result"
			if err != nil {
				reason = "query_error"
			}
			log.FromContext(rpcCtx).Warn("Media group lookup failed; forwarding only the selected message",
				"source_chat_id", fromChatID, "target_chat_id", toChatID, "topic_id", topMsgID,
				"message_id", msgID, "group_id", gid, "reason", reason, "error", err)
		}
	}
	return ForwardMessagesDropAuthor(rpcCtx, ectx, fromChatID, toChatID, ids, topMsgID, linkID)
}

// AppendAttribution edits an already confirmed send; failures must never trigger resending.
func AppendAttribution(rpcCtx context.Context, ectx *ext.Context, toPeer tg.InputPeerClass, updates tg.UpdatesClass, link string) error {
	if _, short := updates.(*tg.UpdateShortSentMessage); short {
		return fmt.Errorf("no message body in short receipt")
	}
	msgs := MessagesFromUpdates(updates)
	if len(msgs) == 0 {
		return fmt.Errorf("no messages in forward updates")
	}
	target := pickAttributionMessage(msgs)
	if target == nil {
		return fmt.Errorf("no editable message in forward result")
	}
	newText, entities, err := PrependAttributionLink(target.GetMessage(), target.Entities, link)
	if err != nil {
		return err
	}
	edit := &tg.MessagesEditMessageRequest{
		Peer: toPeer,
		ID:   target.GetID(),
	}
	edit.SetMessage(newText)
	if len(entities) > 0 {
		edit.SetEntities(entities)
	}
	if _, err := ectx.Raw.MessagesEditMessage(rpcCtx, edit); err != nil {
		return fmt.Errorf("edit message %d: %w", target.GetID(), err)
	}
	return nil
}

func pickAttributionMessage(msgs []*tg.Message) *tg.Message {
	for _, m := range msgs {
		if m != nil && strings.TrimSpace(m.GetMessage()) != "" {
			return m
		}
	}
	for _, m := range msgs {
		if m != nil {
			return m
		}
	}
	return nil
}

// MessagesFromUpdates extracts newly created messages from a send receipt.
func MessagesFromUpdates(u tg.UpdatesClass) []*tg.Message {
	var out []*tg.Message
	add := func(mc tg.MessageClass) {
		if m, ok := mc.(*tg.Message); ok && m != nil {
			out = append(out, m)
		}
	}
	switch v := u.(type) {
	case *tg.Updates:
		for _, up := range v.Updates {
			switch x := up.(type) {
			case *tg.UpdateNewMessage:
				add(x.Message)
			case *tg.UpdateNewChannelMessage:
				add(x.Message)
			}
		}
	case *tg.UpdatesCombined:
		for _, up := range v.Updates {
			switch x := up.(type) {
			case *tg.UpdateNewMessage:
				add(x.Message)
			case *tg.UpdateNewChannelMessage:
				add(x.Message)
			}
		}
	case *tg.UpdateShortSentMessage:
		out = append(out, &tg.Message{ID: v.ID})
	case *tg.UpdateShort:
		switch x := v.Update.(type) {
		case *tg.UpdateNewMessage:
			add(x.Message)
		case *tg.UpdateNewChannelMessage:
			add(x.Message)
		}
	}
	return out
}

// PrependAttributionLink prepends a clickable [转] and a space before text, preserving existing entities.
func PrependAttributionLink(text string, entities []tg.MessageEntityClass, link string) (string, []tg.MessageEntityClass, error) {
	if text == "" {
		return buildAttributionOnly(link)
	}
	prefix := attributionLabel + " "
	prefixLen := utf16Len(prefix)
	newText := prefix + text
	out := make([]tg.MessageEntityClass, 0, len(entities)+1)
	out = append(out, &tg.MessageEntityTextURL{
		Offset: 0,
		Length: utf16Len(attributionLabel),
		URL:    link,
	})
	out = append(out, shiftEntityOffsets(entities, prefixLen)...)
	return newText, out, nil
}

func shiftEntityOffsets(entities []tg.MessageEntityClass, delta int) []tg.MessageEntityClass {
	if delta == 0 || len(entities) == 0 {
		return entities
	}
	out := make([]tg.MessageEntityClass, 0, len(entities))
	for _, e := range entities {
		if e == nil {
			continue
		}
		shifted, ok := cloneEntityWithOffset(e, e.GetOffset()+delta)
		if !ok {
			continue
		}
		out = append(out, shifted)
	}
	return out
}

func cloneEntityWithOffset(e tg.MessageEntityClass, newOffset int) (tg.MessageEntityClass, bool) {
	v := reflect.ValueOf(e)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil, false
	}
	cp := reflect.New(v.Elem().Type())
	cp.Elem().Set(v.Elem())
	f := cp.Elem().FieldByName("Offset")
	if !f.IsValid() || !f.CanSet() {
		return nil, false
	}
	f.SetInt(int64(newOffset))
	shifted, ok := cp.Interface().(tg.MessageEntityClass)
	return shifted, ok
}

func buildAttributionOnly(link string) (string, []tg.MessageEntityClass, error) {
	eb := entity.Builder{}
	if err := styling.Perform(&eb, styling.TextURL(attributionLabel, link)); err != nil {
		return "", nil, fmt.Errorf("build attribution: %w", err)
	}
	text, entities := eb.Complete()
	return text, entities, nil
}

func utf16Len(s string) int {
	return len(utf16.Encode([]rune(s)))
}

func resolveInputPeer(ctx *ext.Context, chatID int64) (tg.InputPeerClass, error) {
	if peer := lookupCachedInputPeer(ctx, chatID); peer != nil && !peer.Zero() {
		return peer, nil
	}
	if _, err := ctx.GetChat(chatID); err != nil {
		return nil, fmt.Errorf("get chat: %w", err)
	}
	if peer := lookupCachedInputPeer(ctx, chatID); peer != nil && !peer.Zero() {
		return peer, nil
	}
	return nil, fmt.Errorf("peer not in storage after get chat")
}

func lookupCachedInputPeer(ctx *ext.Context, chatID int64) tg.InputPeerClass {
	peer := ctx.PeerStorage.GetInputPeerById(chatID)
	if peer != nil && !peer.Zero() {
		return peer
	}
	id := constant.TDLibPeerID(chatID)
	plain := id.ToPlain()
	var channel constant.TDLibPeerID
	channel.Channel(plain)
	peer = ctx.PeerStorage.GetInputPeerById(int64(channel))
	if peer != nil && !peer.Zero() {
		return peer
	}
	var chat constant.TDLibPeerID
	chat.Chat(plain)
	peer = ctx.PeerStorage.GetInputPeerById(int64(chat))
	if peer != nil && !peer.Zero() {
		return peer
	}
	var userID constant.TDLibPeerID
	userID.User(plain)
	return ctx.PeerStorage.GetInputPeerById(int64(userID))
}

func randomID() int64 {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0
	}
	return int64(binary.LittleEndian.Uint64(b[:]))
}

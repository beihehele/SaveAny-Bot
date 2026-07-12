package user

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
)

func ForwardMessagesDropAuthor(ctx *ext.Context, fromChatID, toChatID int64, messageIDs []int) error {
	if ctx == nil {
		return fmt.Errorf("user context is nil")
	}
	if len(messageIDs) == 0 {
		return fmt.Errorf("empty message ids")
	}
	ids := append([]int(nil), messageIDs...)
	sort.Ints(ids)

	fromPeer, err := resolveInputPeer(ctx, fromChatID)
	if err != nil {
		return fmt.Errorf("cannot resolve source peer %d: %w", fromChatID, err)
	}
	toPeer, err := resolveInputPeer(ctx, toChatID)
	if err != nil {
		return fmt.Errorf("cannot resolve target peer %d: %w", toChatID, err)
	}

	randomIDs := make([]int64, len(ids))
	for i := range randomIDs {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return fmt.Errorf("random id: %w", err)
		}
		randomIDs[i] = int64(binary.LittleEndian.Uint64(b[:]))
	}

	_, err = ctx.Raw.MessagesForwardMessages(ctx, &tg.MessagesForwardMessagesRequest{
		DropAuthor: true,
		FromPeer:   fromPeer,
		ID:         ids,
		RandomID:   randomIDs,
		ToPeer:     toPeer,
	})
	if err != nil {
		return fmt.Errorf("forward messages: %w", err)
	}
	return nil
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
	var user constant.TDLibPeerID
	user.User(plain)
	return ctx.PeerStorage.GetInputPeerById(int64(user))
}

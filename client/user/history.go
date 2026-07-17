package user

import (
	"fmt"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

func GetLatestMessageID(ctx *ext.Context, chatID int64) (int, error) {
	if ctx == nil {
		return 0, fmt.Errorf("user context is nil")
	}
	peer, err := resolveInputPeer(ctx, chatID)
	if err != nil {
		return 0, err
	}
	res, err := ctx.Raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:  peer,
		Limit: 1,
	})
	if err != nil {
		return 0, fmt.Errorf("getHistory: %w", err)
	}
	var list []tg.MessageClass
	switch v := res.(type) {
	case *tg.MessagesMessages:
		list = v.Messages
	case *tg.MessagesMessagesSlice:
		list = v.Messages
	case *tg.MessagesChannelMessages:
		list = v.Messages
	default:
		return 0, fmt.Errorf("unexpected history type %T", res)
	}
	for _, mc := range list {
		if msg, ok := mc.(*tg.Message); ok && msg != nil {
			return msg.GetID(), nil
		}
	}
	return 0, fmt.Errorf("no messages in chat %d", chatID)
}

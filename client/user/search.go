package user

import (
	"fmt"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

const searchPageSize = 100

// SearchPageSize returns the page size used for search and history pagination.
func SearchPageSize() int {
	return searchPageSize
}

// SearchMessages searches chat history for keyword q, paging older with offsetID.
// Returns messages in API order (typically newest first within the page).
func SearchMessages(ctx *ext.Context, chatID int64, q string, offsetID int) ([]*tg.Message, error) {
	if ctx == nil {
		return nil, fmt.Errorf("user context is nil")
	}
	peer, err := resolveInputPeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	res, err := ctx.Raw.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:     peer,
		Q:        q,
		Filter:   &tg.InputMessagesFilterEmpty{},
		OffsetID: offsetID,
		Limit:    searchPageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("messages.search: %w", err)
	}
	return extractMessages(res), nil
}

// HistoryMessages fetches a page of messages newer than offsetID (offset 0 = latest).
func HistoryMessages(ctx *ext.Context, chatID int64, offsetID int) ([]*tg.Message, error) {
	if ctx == nil {
		return nil, fmt.Errorf("user context is nil")
	}
	peer, err := resolveInputPeer(ctx, chatID)
	if err != nil {
		return nil, err
	}
	res, err := ctx.Raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: offsetID,
		Limit:    searchPageSize,
	})
	if err != nil {
		return nil, fmt.Errorf("messages.getHistory: %w", err)
	}
	return extractMessages(res), nil
}

func extractMessages(res tg.MessagesMessagesClass) []*tg.Message {
	var classes []tg.MessageClass
	switch v := res.(type) {
	case *tg.MessagesMessages:
		classes = v.Messages
	case *tg.MessagesMessagesSlice:
		classes = v.Messages
	case *tg.MessagesChannelMessages:
		classes = v.Messages
	default:
		return nil
	}
	out := make([]*tg.Message, 0, len(classes))
	for _, mc := range classes {
		msg, ok := mc.(*tg.Message)
		if !ok || msg == nil {
			continue
		}
		out = append(out, msg)
	}
	return out
}

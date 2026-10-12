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

// MessagePage retains pagination metadata before filtering service/empty messages.
type MessagePage struct {
	Messages     []*tg.Message
	RawCount     int
	NextOffsetID int
}

// SearchMessages searches chat history for keyword q, paging older with offsetID.
// Returns messages in API order (typically newest first within the page).
func SearchMessages(ctx *ext.Context, chatID int64, q string, offsetID int) (MessagePage, error) {
	if ctx == nil {
		return MessagePage{}, fmt.Errorf("user context is nil")
	}
	peer, err := resolveInputPeer(ctx, chatID)
	if err != nil {
		return MessagePage{}, err
	}
	res, err := ctx.Raw.MessagesSearch(ctx, &tg.MessagesSearchRequest{
		Peer:     peer,
		Q:        q,
		Filter:   &tg.InputMessagesFilterEmpty{},
		OffsetID: offsetID,
		Limit:    searchPageSize,
	})
	if err != nil {
		return MessagePage{}, fmt.Errorf("messages.search: %w", err)
	}
	return extractMessages(res)
}

// HistoryMessages fetches older messages below offsetID (offset 0 = latest).
func HistoryMessages(ctx *ext.Context, chatID int64, offsetID int) (MessagePage, error) {
	if ctx == nil {
		return MessagePage{}, fmt.Errorf("user context is nil")
	}
	peer, err := resolveInputPeer(ctx, chatID)
	if err != nil {
		return MessagePage{}, err
	}
	res, err := ctx.Raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
		Peer:     peer,
		OffsetID: offsetID,
		Limit:    searchPageSize,
	})
	if err != nil {
		return MessagePage{}, fmt.Errorf("messages.getHistory: %w", err)
	}
	return extractMessages(res)
}

func extractMessages(res tg.MessagesMessagesClass) (MessagePage, error) {
	var classes []tg.MessageClass
	switch v := res.(type) {
	case *tg.MessagesMessages:
		classes = v.Messages
	case *tg.MessagesMessagesSlice:
		classes = v.Messages
	case *tg.MessagesChannelMessages:
		classes = v.Messages
	default:
		return MessagePage{}, fmt.Errorf("unexpected messages response: %T", res)
	}
	page := MessagePage{RawCount: len(classes), Messages: make([]*tg.Message, 0, len(classes))}
	for _, mc := range classes {
		id := 0
		switch message := mc.(type) {
		case *tg.Message:
			if message == nil {
				continue
			}
			id = message.ID
			page.Messages = append(page.Messages, message)
		case *tg.MessageService:
			if message == nil {
				continue
			}
			id = message.ID
		case *tg.MessageEmpty:
			if message == nil {
				continue
			}
			id = message.ID
		}
		if id > 0 && (page.NextOffsetID == 0 || id < page.NextOffsetID) {
			page.NextOffsetID = id
		}
	}
	return page, nil
}

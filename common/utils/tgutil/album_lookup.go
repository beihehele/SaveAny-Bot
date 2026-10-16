package tgutil

import (
	"fmt"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
)

// AlbumLookup reuses successful album selections within one link submission.
// Use a new lookup for each submission; messages are never cached across requests.
type AlbumLookup struct {
	members map[albumLookupKey][]*tg.Message
}

type albumLookupKey struct {
	client    *tg.Client
	peer      constant.TDLibPeerID
	groupID   int64
	messageID int
}

// Get retains the querying client and only reuses results for observed members.
// A bounded query does not establish completeness: an unseen member must be queried.
func (a *AlbumLookup) Get(ctx *ext.Context, chatID int64, msg *tg.Message) ([]*tg.Message, error) {
	if ctx == nil || msg == nil {
		return nil, fmt.Errorf("invalid grouped message")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	peer := constant.TDLibPeerID(chatID)
	switch p := msg.PeerID.(type) {
	case *tg.PeerChannel:
		peer.Channel(p.ChannelID)
	case *tg.PeerChat:
		peer.Chat(p.ChatID)
	case *tg.PeerUser:
		peer.User(p.UserID)
	}
	key := albumLookupKey{client: ctx.Raw, peer: peer, groupID: msg.GroupedID, messageID: msg.ID}
	if members, ok := a.members[key]; ok {
		return members, nil
	}
	members, err := GetGroupedMessages(ctx, chatID, msg)
	if err != nil || len(members) == 0 {
		return members, err
	}
	if a.members == nil {
		a.members = make(map[albumLookupKey][]*tg.Message)
	}
	for _, member := range members {
		key.messageID = member.ID
		a.members[key] = members
	}
	return members, nil
}

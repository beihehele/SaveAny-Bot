package tfile

import (
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
)

// LinkedFiles collects media selected by links within one save request. Identity
// is the resolved chat and message, so overlapping albums do not duplicate work
// and equal message/group IDs in different chats remain independent.
type LinkedFiles struct {
	seen  map[[2]int64]struct{}
	files []TGFileMessage
}

// Add keeps the first successfully converted occurrence and its client/options.
// Unsupported media do not consume identity, allowing a later valid occurrence.
func (c *LinkedFiles) Add(chatID int64, client downloader.Client, msg *tg.Message, opts ...TGFileOption) error {
	if msg == nil || msg.Media == nil {
		return nil
	}
	// A public username and a private /c/ link may resolve to different ID
	// representations. Use the actual peer when available to identify aliases.
	var peer constant.TDLibPeerID
	switch p := msg.PeerID.(type) {
	case *tg.PeerChannel:
		peer.Channel(p.ChannelID)
	case *tg.PeerChat:
		peer.Chat(p.ChatID)
	case *tg.PeerUser:
		peer.User(p.UserID)
	}
	if peer != 0 {
		chatID = int64(peer)
	}
	key := [2]int64{chatID, int64(msg.ID)}
	if _, exists := c.seen[key]; exists {
		return nil
	}
	file, err := FromMediaMessage(msg.Media, client, msg, opts...)
	if err != nil {
		return err
	}
	if c.seen == nil {
		c.seen = make(map[[2]int64]struct{})
	}
	c.seen[key] = struct{}{}
	c.files = append(c.files, file)
	return nil
}

// Files returns the selected files in first-occurrence order.
func (c *LinkedFiles) Files() []TGFileMessage { return c.files }

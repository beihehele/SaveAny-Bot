package tgutil

import (
	"fmt"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"
)

// BuildMessageLink builds a public t.me link to a message in chatID.
func BuildMessageLink(ctx *ext.Context, chatID int64, msgID int) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("context is nil")
	}
	peer := ctx.PeerStorage.GetPeerById(chatID)
	if peer != nil && peer.Username != "" {
		return fmt.Sprintf("https://t.me/%s/%d", peer.Username, msgID), nil
	}
	id := constant.TDLibPeerID(chatID)
	plain := id.ToPlain()
	if id.IsChannel() || id.IsChat() {
		return fmt.Sprintf("https://t.me/c/%d/%d", plain, msgID), nil
	}
	if plain > 0 {
		return fmt.Sprintf("https://t.me/c/%d/%d", plain, msgID), nil
	}
	return "", fmt.Errorf("cannot build link for chat %d", chatID)
}

package handlers

import (
	"context"
	"fmt"

	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
)

// ChatChoice omits credentials and access hashes from console responses.
type ChatChoice struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Managed bool   `json:"managed"`
	Forum   bool   `json:"forum"`
}

type chatCursor struct {
	Folder, Date, Message int
	Peer                  int64
}

// chatPage retains its offset even when every dialog was filtered out.
type chatPage struct {
	Chats []ChatChoice
	Next  *chatCursor
}

// chatsPage pages both normal and archived dialogs of the current Userbot account.
func (m ManagementService) chatsPage(ctx context.Context, owner int64, cursor chatCursor) (chatPage, error) {
	page := chatPage{Chats: make([]ChatChoice, 0)}
	if _, err := ManagementUser(ctx, owner); err != nil {
		return page, err
	}
	client, err := m.client(ctx)
	if err != nil {
		return page, err
	}
	var offset tg.InputPeerClass = &tg.InputPeerEmpty{}
	if cursor.Peer != 0 {
		offset = client.PeerStorage.GetInputPeerById(cursor.Peer)
		if offset == nil || offset.Zero() {
			return page, fmt.Errorf("chat list expired; reload from the beginning")
		}
	}
	req := &tg.MessagesGetDialogsRequest{OffsetPeer: offset, OffsetID: cursor.Message, OffsetDate: cursor.Date, Limit: 100, ExcludePinned: cursor.Message != 0}
	req.SetFolderID(cursor.Folder)
	box, err := client.Raw.MessagesGetDialogs(ctx, req)
	if err != nil {
		return page, fmt.Errorf("load chats: %w", err)
	}
	var dialogs []tg.DialogClass
	var messages []tg.MessageClass
	var chats []tg.ChatClass
	var users []tg.UserClass
	complete := false
	switch b := box.(type) {
	case *tg.MessagesDialogs:
		dialogs, messages, chats, users = b.Dialogs, b.Messages, b.Chats, b.Users
		complete = true
	case *tg.MessagesDialogsSlice:
		dialogs, messages, chats, users = b.Dialogs, b.Messages, b.Chats, b.Users
	default:
		return page, fmt.Errorf("unsupported dialogs response")
	}
	choices := make(map[int64]ChatChoice)
	for _, raw := range chats {
		switch ch := raw.(type) {
		case *tg.Channel:
			if ch.Min {
				continue
			}
			if _, ok := ch.GetAccessHash(); !ok {
				continue
			}
			client.PeerStorage.AddPeer(ch.ID, ch.AccessHash, peerstore.TypeChannel, ch.Username)
			// Excluded chats can still be the pagination offset peer.
			if ch.Left || (!ch.Broadcast && !ch.Megagroup) {
				continue
			}
			_, admin := ch.GetAdminRights()
			choices[botAPIChannelPrefix-ch.ID] = ChatChoice{ID: botAPIChannelPrefix - ch.ID, Title: ch.Title, Managed: ch.Creator || admin, Forum: ch.Forum}
		case *tg.Chat:
			client.PeerStorage.AddPeer(ch.ID, peerstore.DefaultAccessHash, peerstore.TypeChat, peerstore.DefaultUsername)
			if ch.Left || ch.Deactivated {
				continue
			}
			if _, ok := ch.GetMigratedTo(); ok {
				continue
			}
			_, admin := ch.GetAdminRights()
			choices[-ch.ID] = ChatChoice{ID: -ch.ID, Title: ch.Title, Managed: ch.Creator || admin}
		case *tg.ChannelForbidden:
			client.PeerStorage.AddPeer(ch.ID, ch.AccessHash, peerstore.TypeChannel, peerstore.DefaultUsername)
		case *tg.ChatForbidden:
			client.PeerStorage.AddPeer(ch.ID, peerstore.DefaultAccessHash, peerstore.TypeChat, peerstore.DefaultUsername)
		}
	}
	for _, raw := range users {
		if u, ok := raw.(*tg.User); ok && !u.Min {
			client.PeerStorage.AddPeer(u.ID, u.AccessHash, peerstore.TypeUser, u.Username)
		}
	}
	seen := make(map[int64]bool)
	for _, dialog := range dialogs {
		var entry ChatChoice
		var ok bool
		switch p := dialog.GetPeer().(type) {
		case *tg.PeerChannel:
			entry, ok = choices[botAPIChannelPrefix-p.ChannelID]
		case *tg.PeerChat:
			entry, ok = choices[-p.ChatID]
		}
		if ok && !seen[entry.ID] {
			page.Chats = append(page.Chats, entry)
			seen[entry.ID] = true
		}
	}
	if complete || len(dialogs) == 0 {
		if cursor.Folder == 0 {
			cursor = chatCursor{Folder: 1}
		} else {
			return page, nil
		}
	} else {
		last := dialogs[len(dialogs)-1]
		var peerID int64
		switch p := last.GetPeer().(type) {
		case *tg.PeerChannel:
			peerID = botAPIChannelPrefix - p.ChannelID
		case *tg.PeerChat:
			peerID = -p.ChatID
		case *tg.PeerUser:
			peerID = p.UserID
		default:
			return page, fmt.Errorf("unsupported dialog cursor")
		}
		var date int
		for _, message := range messages {
			if msg, ok := message.AsNotEmpty(); ok && msg.GetID() == last.GetTopMessage() && msg.GetPeerID().String() == last.GetPeer().String() {
				date = msg.GetDate()
				break
			}
		}
		next := chatCursor{Folder: cursor.Folder, Date: date, Message: last.GetTopMessage(), Peer: peerID}
		if next.Message <= 0 || next.Date <= 0 || next == cursor {
			return page, fmt.Errorf("chat cursor did not advance")
		}
		cursor = next
	}
	page.Next = &cursor
	return page, nil
}

package telegram

import (
	"reflect"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

// forwardedOrigin only substitutes a complete, unchanged channel album for
// Bot-private attachments. Private message IDs never belong to the Userbot.
func forwardedOrigin(client *ext.Context, files []tfile.TGFileMessage) ([]*tg.Message, int64, error) {
	ids := make([]int, len(files))
	var channelID int64
	for i, file := range files {
		header, ok := file.Message().GetFwdFrom()
		if !ok || header.ChannelPost <= 0 {
			return nil, 0, nil
		}
		channel, ok := header.FromID.(*tg.PeerChannel)
		if !ok || channel.ChannelID <= 0 {
			return nil, 0, nil
		}
		if i == 0 {
			channelID = channel.ChannelID
		}
		if channel.ChannelID != channelID || (i > 0 && header.ChannelPost <= ids[i-1]) {
			return nil, 0, nil
		}
		ids[i] = header.ChannelPost
	}
	var source constant.TDLibPeerID
	source.Channel(channelID)
	first, err := tgutil.GetMessageByID(client, int64(source), ids[0])
	if err != nil {
		return nil, 0, err
	}
	if (first.GroupedID != 0) != (files[0].Message().GroupedID != 0) {
		return nil, 0, nil
	}
	messages := []*tg.Message{first}
	if first.GroupedID != 0 {
		messages, err = tgutil.GetGroupedMessages(client, int64(source), first)
		if err != nil {
			return nil, 0, err
		}
	}
	if len(messages) != len(files) {
		return nil, 0, nil
	}
	for i, message := range messages {
		peer, ok := message.PeerID.(*tg.PeerChannel)
		if !ok || peer.ChannelID != channelID || message.ID != ids[i] || !sameForwardedContent(message, files[i].Message()) {
			return nil, 0, nil
		}
	}
	return messages, int64(source), nil
}

func sameForwardedContent(a, b *tg.Message) bool {
	if a.Message != b.Message || a.InvertMedia != b.InvertMedia || !reflect.DeepEqual(a.Entities, b.Entities) {
		return false
	}
	switch media := a.Media.(type) {
	case *tg.MessageMediaPhoto:
		other, ok := b.Media.(*tg.MessageMediaPhoto)
		photo, valid := media.Photo.(*tg.Photo)
		if !ok || !valid || photo.ID == 0 {
			return false
		}
		original, valid := other.Photo.(*tg.Photo)
		return valid && original.ID == photo.ID && media.Spoiler == other.Spoiler
	case *tg.MessageMediaDocument:
		other, ok := b.Media.(*tg.MessageMediaDocument)
		doc, valid := media.Document.(*tg.Document)
		if !ok || !valid || doc.ID == 0 {
			return false
		}
		original, valid := other.Document.(*tg.Document)
		return valid && original.ID == doc.ID && media.Spoiler == other.Spoiler
	}
	return false
}

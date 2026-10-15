package tfile

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
)

// AlbumKey identifies a media group within its Telegram peer, including peer kind.
func AlbumKey(msg *tg.Message) [2]int64 {
	if msg == nil {
		return [2]int64{}
	}
	var peer constant.TDLibPeerID
	switch p := msg.PeerID.(type) {
	case *tg.PeerChannel:
		peer.Channel(p.ChannelID)
	case *tg.PeerChat:
		peer.Chat(p.ChatID)
	case *tg.PeerUser:
		peer.User(p.UserID)
	}
	return [2]int64{int64(peer), msg.GroupedID}
}

// AlbumDirectories returns a stable caption directory for each grouped message.
// Grouping includes the peer; the earliest usable caption wins regardless of
// arrival order. Names omit message IDs so equal captions share a directory.
func AlbumDirectories(files []TGFileMessage) map[*tg.Message]string {
	type album struct {
		messages []*tg.Message
		caption  string
		id       int
	}
	groups := make(map[[2]int64]*album)
	for _, file := range files {
		if file == nil {
			continue
		}
		msg := file.Message()
		if msg == nil || msg.GroupedID == 0 {
			continue
		}
		key := AlbumKey(msg)
		group := groups[key]
		if group == nil {
			group = &album{}
			groups[key] = group
		}
		group.messages = append(group.messages, msg)
		if name := captionDirectory(msg.Message); name != "" && (group.caption == "" || msg.ID < group.id) {
			group.caption, group.id = name, msg.ID
		}
	}
	result := make(map[*tg.Message]string)
	for key, group := range groups {
		name := group.caption
		if name == "" {
			name = fmt.Sprintf("album_%d_%d", key[0], key[1])
		}
		for _, msg := range group.messages {
			result[msg] = name
		}
	}
	return result
}

func captionDirectory(caption string) string {
	name := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(caption))
	name = strings.Join(strings.FieldsFunc(name, func(r rune) bool { return r == '_' }), "_")
	name = strings.Trim(name, " ._")
	// Windows directory components reserve these names even with an extension.
	stem := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" ||
		len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9' {
		name = "_" + name
	}
	var length, count int
	for _, r := range name {
		width := utf8.RuneLen(r)
		if count == 64 || length+width > 180 {
			break
		}
		length += width
		count++
	}
	return strings.TrimRight(name[:length], " ._")
}

package tfile

import (
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
)

func linkedMessage(id int, group int64) *tg.Message {
	msg := &tg.Message{ID: id, Media: &tg.MessageMediaDocument{Document: &tg.Document{
		ID: int64(id), Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "file.bin"}},
	}}}
	if group != 0 {
		msg.SetGroupedID(group)
	}
	return msg
}

func TestLinkedFilesDeduplicateMessagesWithoutCombiningChats(t *testing.T) {
	var selected LinkedFiles
	first, second := &tg.Client{}, &tg.Client{}
	for _, selection := range []struct {
		chat int64
		ids  []int
	}{
		{100, []int{42}},         // ?single selects only this member.
		{100, []int{40, 42, 44}}, // Another link expands the album.
		{100, []int{40, 42, 44}}, // An overlapping link must not duplicate it.
		{200, []int{40, 42, 44}}, // Same IDs/group in a different chat.
		{100, []int{90}},
	} {
		for _, id := range selection.ids {
			client := second
			if id == 42 && selection.chat == 100 {
				client = first
			}
			if err := selected.Add(selection.chat, client, linkedMessage(id, 700), WithName("first-name.bin")); err != nil {
				t.Fatal(err)
			}
		}
	}
	var ids []int
	for _, file := range selected.Files() {
		ids = append(ids, file.Message().ID)
	}
	if !reflect.DeepEqual(ids, []int{42, 40, 44, 40, 42, 44, 90}) {
		t.Fatalf("selection changed: %v", ids)
	}
	if selected.Files()[0].Dler() != first || selected.Files()[0].Name() != "first-name.bin" {
		t.Fatal("first client/options lost")
	}
}

func TestFailedLinkedConversionDoesNotHideLaterValidMedia(t *testing.T) {
	var selected LinkedFiles
	if err := selected.Add(100, nil, &tg.Message{ID: 42, Media: &tg.MessageMediaContact{}}); err == nil {
		t.Fatal("unsupported media accepted")
	}
	if err := selected.Add(100, nil, linkedMessage(42, 0)); err != nil {
		t.Fatal(err)
	}
	if err := selected.Add(100, nil, linkedMessage(42, 0), WithName("replacement.bin")); err != nil {
		t.Fatal(err)
	}
	if len(selected.Files()) != 1 || selected.Files()[0].Name() != "file.bin" {
		t.Fatal("failed/duplicate conversion consumed or replaced identity")
	}
}

func TestLinkedFilesNormalizeChatAliasesButKeepPeerTypes(t *testing.T) {
	var selected LinkedFiles
	channel := linkedMessage(42, 700)
	channel.PeerID = &tg.PeerChannel{ChannelID: 111}
	for _, alias := range []int64{111, -1000000000111} {
		if err := selected.Add(alias, nil, channel); err != nil {
			t.Fatal(err)
		}
	}
	chat := linkedMessage(42, 700)
	chat.PeerID = &tg.PeerChat{ChatID: 111}
	if err := selected.Add(111, nil, chat); err != nil {
		t.Fatal(err)
	}
	if len(selected.Files()) != 2 {
		t.Fatalf("aliases or peer types combined incorrectly: %d", len(selected.Files()))
	}
}

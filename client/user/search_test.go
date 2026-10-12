package user

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestMessagePageKeepsServiceAndEmptyCursorWithoutSelectingThem(t *testing.T) {
	var missing *tg.Message
	classes := []tg.MessageClass{&tg.Message{ID: 100}, &tg.MessageService{ID: 99}, &tg.MessageEmpty{ID: 98}, nil, missing}
	for _, response := range []tg.MessagesMessagesClass{
		&tg.MessagesMessages{Messages: classes},
		&tg.MessagesMessagesSlice{Messages: classes},
		&tg.MessagesChannelMessages{Messages: classes},
	} {
		page, err := extractMessages(response)
		if err != nil || page.RawCount != 5 || page.NextOffsetID != 98 || len(page.Messages) != 1 || page.Messages[0].ID != 100 {
			t.Fatalf("incorrect pagination metadata: %+v %v", page, err)
		}
	}
	if _, err := extractMessages(&tg.MessagesMessagesNotModified{}); err == nil {
		t.Fatal("unsupported response silently ended scan")
	}
}

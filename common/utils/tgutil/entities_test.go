package tgutil

import (
	"reflect"
	"testing"

	"github.com/gotd/td/tg"
)

func TestMessageEntityURLsRejectInvalidUTF16Bounds(t *testing.T) {
	const link = "https://t.me/test/1"
	text := "😀 " + link
	for _, entity := range []*tg.MessageEntityURL{
		{Offset: -1, Length: 1},
		{Offset: 3, Length: -1},
		{Offset: 100, Length: 1},
		{Offset: 3, Length: 2147483647},
		{Offset: 2147483647, Length: 2147483647},
	} {
		message := &tg.Message{Message: text, Entities: []tg.MessageEntityClass{entity}}
		if urls := ExtractMessageEntityUrls(message); len(urls) != 0 {
			t.Fatalf("invalid entity selected URLs: %v", urls)
		}
	}
	message := &tg.Message{Message: text, Entities: []tg.MessageEntityClass{&tg.MessageEntityURL{Offset: 3, Length: len(link)}}}
	if urls := ExtractMessageEntityUrls(message); !reflect.DeepEqual(urls, []string{link}) {
		t.Fatalf("valid UTF-16 URL changed: %v", urls)
	}
}

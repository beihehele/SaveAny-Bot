package user

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestPrependAttributionLinkPreservesEntities(t *testing.T) {
	orig := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 5},
	}
	text, entities, err := PrependAttributionLink("hello", orig, "https://t.me/test/1")
	if err != nil {
		t.Fatal(err)
	}
	if text != "[转] hello" {
		t.Fatalf("text=%q", text)
	}
	if len(entities) != 2 {
		t.Fatalf("entities=%d", len(entities))
	}
	tu, ok := entities[0].(*tg.MessageEntityTextURL)
	if !ok || tu.URL != "https://t.me/test/1" || tu.Offset != 0 || tu.Length != utf16Len(attributionLabel) {
		t.Fatalf("first entity=%#v", entities[0])
	}
	bold, ok := entities[1].(*tg.MessageEntityBold)
	if !ok || bold.Offset != utf16Len("[转] ") || bold.Length != 5 {
		t.Fatalf("bold entity=%#v want offset=%d", entities[1], utf16Len("[转] "))
	}
}

func TestPrependAttributionLinkEmpty(t *testing.T) {
	text, entities, err := PrependAttributionLink("", nil, "https://t.me/test/2")
	if err != nil {
		t.Fatal(err)
	}
	if text != "[转]" || len(entities) == 0 {
		t.Fatalf("text=%q entities=%v", text, entities)
	}
}

package user

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestAppendAttributionLinkPreservesEntities(t *testing.T) {
	orig := []tg.MessageEntityClass{
		&tg.MessageEntityBold{Offset: 0, Length: 5},
	}
	text, entities, err := AppendAttributionLink("hello", orig, "https://t.me/test/1")
	if err != nil {
		t.Fatal(err)
	}
	if text != "hello\n[转自]" {
		t.Fatalf("text=%q", text)
	}
	if len(entities) != 2 {
		t.Fatalf("entities=%d", len(entities))
	}
	if _, ok := entities[0].(*tg.MessageEntityBold); !ok {
		t.Fatalf("first entity should remain bold: %T", entities[0])
	}
	tu, ok := entities[1].(*tg.MessageEntityTextURL)
	if !ok || tu.URL != "https://t.me/test/1" {
		t.Fatalf("second entity=%#v", entities[1])
	}
}

func TestAppendAttributionLinkEmpty(t *testing.T) {
	text, entities, err := AppendAttributionLink("", nil, "https://t.me/test/2")
	if err != nil {
		t.Fatal(err)
	}
	if text != "[转自]" || len(entities) == 0 {
		t.Fatalf("text=%q entities=%v", text, entities)
	}
}

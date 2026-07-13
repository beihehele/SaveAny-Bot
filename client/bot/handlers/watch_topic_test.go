package handlers

import (
	"testing"

	"github.com/gotd/td/tg"
)

func TestNextForumTopicsOffset(t *testing.T) {
	msgDates := map[int]int{42: 1700000000}
	topics := []tg.ForumTopicClass{
		&tg.ForumTopic{ID: 1, TopMessage: 10, Date: 1699999999},
		&tg.ForumTopic{ID: 2, TopMessage: 42, Date: 1699999998},
	}
	date, id, topic, ok := nextForumTopicsOffset(topics, msgDates)
	if !ok {
		t.Fatal("expected ok")
	}
	if topic != 2 || id != 42 || date != 1700000000 {
		t.Fatalf("got date=%d id=%d topic=%d", date, id, topic)
	}
}

func TestNextForumTopicsOffsetSkipsDeletedAtEnd(t *testing.T) {
	topics := []tg.ForumTopicClass{
		&tg.ForumTopic{ID: 1, TopMessage: 10, Date: 1699999999},
		&tg.ForumTopicDeleted{ID: 99},
	}
	date, id, topic, ok := nextForumTopicsOffset(topics, nil)
	if !ok {
		t.Fatal("expected ok")
	}
	if topic != 1 || id != 10 || date != 1699999999 {
		t.Fatalf("got date=%d id=%d topic=%d", date, id, topic)
	}
}

func TestNextForumTopicsOffsetDeletedOnlyPage(t *testing.T) {
	topics := []tg.ForumTopicClass{
		&tg.ForumTopicDeleted{ID: 7},
		&tg.ForumTopicDeleted{ID: 8},
	}
	_, _, topic, ok := nextForumTopicsOffset(topics, nil)
	if !ok {
		t.Fatal("expected ok")
	}
	if topic != 8 {
		t.Fatalf("got topic=%d want 8", topic)
	}
}

func TestNextForumTopicsOffsetEmpty(t *testing.T) {
	_, _, _, ok := nextForumTopicsOffset(nil, nil)
	if ok {
		t.Fatal("expected not ok")
	}
}

package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestWatchRouteIdentityAndStaleUserSnapshot(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	user := &User{Model: gorm.Model{ID: 1}}
	if err := user.WatchChat(t.Context(), WatchChat{ChatID: 10, TargetID: 20, TargetTopicID: 2}); err != nil {
		t.Fatal(err)
	}
	old := user.WatchChats[0]
	if old.ID == 0 || old.UserID != user.ID {
		t.Fatalf("new route identity was not populated: %+v", old)
	}
	if active, err := WatchRouteActive(t.Context(), old); err != nil || !active {
		t.Fatalf("active route rejected: %v %v", active, err)
	}
	loaded, err := GetWatchChatsByChatID(t.Context(), old.ChatID)
	if err != nil || len(loaded) != 1 {
		t.Fatalf("reload route: %v %v", loaded, err)
	}
	loaded[0].CreatedAt = loaded[0].CreatedAt.In(time.FixedZone("other timezone", -5*60*60))
	if active, err := WatchRouteActive(t.Context(), *loaded[0]); err != nil || !active {
		t.Fatalf("reloaded/equivalent timestamp rejected: %v %v", active, err)
	}
	for _, change := range []func(*WatchChat){
		func(r *WatchChat) { r.UserID++ },
		func(r *WatchChat) { r.CreatedAt = r.CreatedAt.Add(time.Second) },
	} {
		other := old
		change(&other)
		if active, err := WatchRouteActive(t.Context(), other); err != nil || active {
			t.Fatalf("wrong owner/generation accepted: %v %v", active, err)
		}
	}
	if err := (&User{Model: gorm.Model{ID: 2}}).UnwatchByID(t.Context(), old.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("foreign deletion: %v", err)
	}
	if err := user.UnwatchByID(t.Context(), old.ID); err != nil {
		t.Fatal(err)
	}
	// user.WatchChats still contains the deleted route. A new command must not
	// write it back or update unrelated route fields from that stale snapshot.
	if err := user.WatchChat(t.Context(), WatchChat{ChatID: 11}); err != nil {
		t.Fatal(err)
	}
	if active, err := WatchRouteActive(t.Context(), old); err != nil || active {
		t.Fatalf("new watch resurrected removed route: %v %v", active, err)
	}
	if err := user.WatchChat(t.Context(), WatchChat{ChatID: 10, TargetID: 20, TargetTopicID: 2}); err != nil {
		t.Fatal(err)
	}
	replacement := user.WatchChats[len(user.WatchChats)-1]
	if replacement.ID <= old.ID {
		t.Fatal("replacement reused the removed route ID")
	}
	if active, err := WatchRouteActive(t.Context(), replacement); err != nil || !active {
		t.Fatalf("replacement rejected: %v %v", active, err)
	}
	// Even explicit imported ID reuse must not revive an old callback.
	if err := user.UnwatchByID(t.Context(), replacement.ID); err != nil {
		t.Fatal(err)
	}
	imported := old
	imported.CreatedAt = old.CreatedAt.Add(time.Second)
	if err := db.Create(&imported).Error; err != nil {
		t.Fatal(err)
	}
	if active, err := WatchRouteActive(t.Context(), old); err != nil || active {
		t.Fatalf("old generation accepted after imported ID reuse: %v %v", active, err)
	}
	if active, err := WatchRouteActive(t.Context(), imported); err != nil || !active {
		t.Fatalf("imported route rejected: %v %v", active, err)
	}
	if err := db.Delete(&imported).Error; err != nil {
		t.Fatal(err)
	}
	if active, err := WatchRouteActive(t.Context(), imported); err != nil || active {
		t.Fatalf("soft-deleted route accepted: %v %v", active, err)
	}
}

func TestWatchRouteFailuresPreserveState(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	user := &User{Model: gorm.Model{ID: 1}}
	if err := user.WatchChat(t.Context(), WatchChat{ChatID: 10}); err != nil {
		t.Fatal(err)
	}
	route := user.WatchChats[0]
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := user.UnwatchByID(cancelled, route.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled removal: %v", err)
	}
	if active, err := WatchRouteActive(t.Context(), route); err != nil || !active {
		t.Fatalf("failed removal lost route: %v %v", active, err)
	}
	if _, err := WatchRouteActive(cancelled, route); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled lookup: %v", err)
	}
	for _, ctx := range []context.Context{t.Context(), cancelled} {
		if err := user.WatchChat(ctx, WatchChat{ChatID: 10}); err == nil {
			t.Fatal("duplicate/cancelled insertion accepted")
		}
		if len(user.WatchChats) != 1 {
			t.Fatal("failed insertion changed user snapshot")
		}
	}
	if err := db.Migrator().DropTable(&WatchChat{}); err != nil {
		t.Fatal(err)
	}
	if _, err := WatchRouteActive(t.Context(), route); err == nil {
		t.Fatal("database error was hidden as a stopped route")
	}
}

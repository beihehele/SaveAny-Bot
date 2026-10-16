package database

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestCreateUserRespectsContextAndLookupErrors(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&User{}, &Dir{}, &Rule{}, &WatchChat{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := CreateUser(ctx, 123); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create: %v", err)
	}
	var count int64
	if err := db.Model(&User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("cancelled create wrote rows: count=%d err=%v", count, err)
	}
	writeCtx, cancelWrite := context.WithCancel(t.Context())
	defer cancelWrite()
	if err := db.Callback().Query().After("gorm:after_query").Register("test:cancel_user_create", func(tx *gorm.DB) {
		if errors.Is(tx.Error, gorm.ErrRecordNotFound) {
			cancelWrite()
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(writeCtx, 234); !errors.Is(err, context.Canceled) {
		t.Fatalf("write ignored cancellation after lookup: %v", err)
	}
	if err := db.Callback().Query().Remove("test:cancel_user_create"); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&User{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("cancelled write inserted rows: count=%d err=%v", count, err)
	}
	for range 2 {
		if err := CreateUser(t.Context(), 345); err != nil {
			t.Fatalf("normal/idempotent create: %v", err)
		}
	}
	if err := db.Model(&User{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("normal create duplicated or lost user: count=%d err=%v", count, err)
	}
	// A read failure must not be mistaken for a missing user and trigger a write.
	if err := db.Migrator().DropTable(&User{}); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(t.Context(), 456); err == nil {
		t.Fatal("lookup failure was discarded")
	}
}

func TestUpdateUserPreferencesDoesNotRestoreDeletedAssociations(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&User{}, &Dir{}, &Rule{}); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	user, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := user.WatchChat(t.Context(), WatchChat{ChatID: 100, TargetID: 200, TargetTopicID: 2}); err != nil {
		t.Fatal(err)
	}
	if err := CreateDirForUser(t.Context(), user.ID, "local", "kept-elsewhere"); err != nil {
		t.Fatal(err)
	}
	rule := &Rule{UserID: user.ID, Type: "filename", Data: "old"}
	if err := CreateRule(t.Context(), rule); err != nil {
		t.Fatal(err)
	}
	stale, err := GetUserByChatID(t.Context(), 42)
	if err != nil || len(stale.WatchChats) != 1 || len(stale.Dirs) != 1 || len(stale.Rules) != 1 {
		t.Fatalf("incomplete fixture: %+v %v", stale, err)
	}
	if err := user.UnwatchByID(t.Context(), stale.WatchChats[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDirForUser(t.Context(), user.ID, stale.Dirs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := DeleteRuleForUser(t.Context(), user.ID, stale.Rules[0].ID); err != nil {
		t.Fatal(err)
	}
	// Other commands have changed associations since this preference snapshot.
	stale.Silent = true
	stale.DefaultStorage = "local"
	stale.FilenameStrategy = "template"
	stale.FilenameTemplate = "{{.ID}}"
	stale.ConflictStrategy = "skip"
	if err := UpdateUserPreferences(t.Context(), stale.ChatID, map[string]any{"silent": stale.Silent, "default_storage": stale.DefaultStorage, "filename_strategy": stale.FilenameStrategy, "filename_template": stale.FilenameTemplate, "conflict_strategy": stale.ConflictStrategy}); err != nil {
		t.Fatal(err)
	}
	loaded, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.WatchChats)+len(loaded.Dirs)+len(loaded.Rules) != 0 {
		t.Fatalf("preference update restored deleted associations: watches=%d dirs=%d rules=%d", len(loaded.WatchChats), len(loaded.Dirs), len(loaded.Rules))
	}
	if !loaded.Silent || loaded.DefaultStorage != "local" || loaded.FilenameStrategy != "template" || loaded.FilenameTemplate != "{{.ID}}" || loaded.ConflictStrategy != "skip" {
		t.Fatalf("preference fields not saved: %+v", loaded)
	}
	// False and empty settings must still be persisted by the explicit patch.
	loaded.Silent = false
	loaded.DefaultStorage, loaded.FilenameStrategy, loaded.FilenameTemplate, loaded.ConflictStrategy = "", "", "", ""
	if err := UpdateUserPreferences(t.Context(), loaded.ChatID, map[string]any{"silent": loaded.Silent, "default_storage": loaded.DefaultStorage, "filename_strategy": loaded.FilenameStrategy, "filename_template": loaded.FilenameTemplate, "conflict_strategy": loaded.ConflictStrategy}); err != nil {
		t.Fatal(err)
	}
	loaded, err = GetUserByChatID(t.Context(), 42)
	if err != nil || loaded.Silent || loaded.DefaultStorage != "" || loaded.FilenameStrategy != "" || loaded.FilenameTemplate != "" || loaded.ConflictStrategy != "" {
		t.Fatalf("zero-value preferences not saved: %+v %v", loaded, err)
	}
}

func TestPreferencePatchesPreserveUnrelatedChangesAndRejectUnsafeFields(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&User{}, &Dir{}, &Rule{}, &WatchChat{}); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	stale, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := UpdateUserPreferences(t.Context(), 42, map[string]any{"apply_rule": true}); err != nil {
		t.Fatal(err)
	}
	stale.FilenameTemplate = "{{.msgid}}"
	if err := UpdateUserPreferences(t.Context(), 42, map[string]any{"filename_template": stale.FilenameTemplate}); err != nil {
		t.Fatal(err)
	}
	current, err := GetUserByChatID(t.Context(), 42)
	if err != nil || !current.ApplyRule || current.FilenameTemplate != stale.FilenameTemplate {
		t.Fatal("stale preference patch overwrote another field")
	}
	for _, values := range []map[string]any{{"watch_chats": []WatchChat{}}, {"chat_id": 999}, {"id": 100}, {"silent": true, "deleted_at": nil}, {}} {
		if err := UpdateUserPreferences(t.Context(), 42, values); err == nil {
			t.Fatalf("unsafe/empty preference patch accepted: %v", values)
		}
	}
	if err := UpdateUserPreferences(t.Context(), 999, map[string]any{"silent": true}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing user inserted or masked: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := UpdateUserPreferences(cancelled, 42, map[string]any{"silent": true}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preference patch wrote: %v", err)
	}
	current, err = GetUserByChatID(t.Context(), 42)
	if err != nil || current.Silent || !current.ApplyRule {
		t.Fatal("rejected patch changed user")
	}
}

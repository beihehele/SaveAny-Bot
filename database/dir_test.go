package database

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestDirectoryChangesKeepDefaultReferenceConsistent(t *testing.T) {
	previous := db
	db = testDatabase(t)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&User{}, &Dir{}, &Rule{}, &WatchChat{}); err != nil {
		t.Fatal(err)
	}
	if err := CreateUser(t.Context(), 42); err != nil {
		t.Fatal(err)
	}
	user, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateDirForUser(t.Context(), user.ID, "本地存储", "inbox"); err != nil {
		t.Fatal(err)
	}
	dirs, err := GetUserDirs(t.Context(), user.ID)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("directories=%v err=%v", dirs, err)
	}
	id := dirs[0].ID
	if err := UpdateUserPreferences(t.Context(), 42, map[string]any{"default_storage": "本地存储", "default_dir": id}); err != nil {
		t.Fatal(err)
	}
	check := func(storage, path string, defaultID uint) {
		t.Helper()
		u, err := GetUserByChatID(t.Context(), 42)
		if err != nil || u.DefaultStorage != "本地存储" || u.DefaultDir != defaultID {
			t.Fatalf("default reference: %+v %v", u, err)
		}
		d, err := GetDirByID(t.Context(), id)
		if err != nil || d.StorageName != storage || d.Path != path {
			t.Fatalf("directory: %+v %v", d, err)
		}
	}
	if err := UpdateDirForUser(t.Context(), user.ID, id, "本地存储", "renamed"); err != nil {
		t.Fatal(err)
	}
	check("本地存储", "renamed", id)
	for _, action := range []func(context.Context, uint) error{
		func(ctx context.Context, owner uint) error {
			return UpdateDirForUser(ctx, owner, id, "other", "foreign")
		},
		func(ctx context.Context, owner uint) error { return DeleteDirForUser(ctx, owner, id) },
	} {
		if err := action(t.Context(), user.ID+1); !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatalf("foreign owner: %v", err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := action(ctx, user.ID); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled mutation: %v", err)
		}
		check("本地存储", "renamed", id)
	}
	// A failure resetting the default must roll back the directory mutation too.
	if err := db.Callback().Update().Before("gorm:update").Register("test:reject_directory_default", func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(errors.New("default update failed"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDirForUser(t.Context(), user.ID, id); err == nil {
		t.Fatal("delete ignored default reset failure")
	}
	check("本地存储", "renamed", id)
	if err := UpdateDirForUser(t.Context(), user.ID, id, "other", "moved"); err == nil {
		t.Fatal("edit ignored default reset failure")
	}
	check("本地存储", "renamed", id)
	if err := db.Callback().Update().Remove("test:reject_directory_default"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateDirForUser(t.Context(), user.ID, id, "other", "moved"); err != nil {
		t.Fatal(err)
	}
	check("other", "moved", 0)
	if err := UpdateDirForUser(t.Context(), user.ID, id, "本地存储", "back"); err != nil {
		t.Fatal(err)
	}
	if err := UpdateUserPreferences(t.Context(), 42, map[string]any{"default_dir": id}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteDirForUser(t.Context(), user.ID, id); err != nil {
		t.Fatal(err)
	}
	user, err = GetUserByChatID(t.Context(), 42)
	if err != nil || user.DefaultDir != 0 || user.DefaultStorage != "本地存储" || len(user.Dirs) != 0 {
		t.Fatalf("delete left stale preference: %+v %v", user, err)
	}
}

package database

import (
	"context"
	"errors"
	"testing"

	"gorm.io/gorm"
)

func TestCreateUserRespectsContextAndLookupErrors(t *testing.T) {
	previous := db
	db = migrationDB(t)
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

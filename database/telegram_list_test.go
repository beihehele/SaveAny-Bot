package database

import (
	"context"
	"path/filepath"
	"testing"
)

func TestTelegramListsPersistAndInvalidateObsoleteTopics(t *testing.T) {
	previous := db
	path := filepath.Join(t.TempDir(), "lists.db")
	db = testDatabaseAt(t, path)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&TelegramList{}); err != nil {
		t.Fatal(err)
	}
	put := func(account, chat int64, payload string, forums []int64) {
		t.Helper()
		if _, err := SaveTelegramList(t.Context(), account, chat, []byte(payload), forums); err != nil {
			t.Fatal(err)
		}
	}
	put(10, 0, `[{"id":-1001,"title":"source"}]`, nil)
	put(10, -1001, `[{"id":2,"title":"topic"}]`, nil)
	put(10, -1002, `[]`, nil)
	put(20, -1002, `[{"id":3,"title":"other account"}]`, nil)
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	db = testDatabaseAt(t, path)
	row, err := GetTelegramList(t.Context(), 10, -1001)
	if err != nil || row == nil || string(row.Data) != `[{"id":2,"title":"topic"}]` {
		t.Fatalf("snapshot not durable: %+v %v", row, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := SaveTelegramList(ctx, 10, -1001, []byte(`[]`), nil); err == nil {
		t.Fatal("cancelled save succeeded")
	}
	after, err := GetTelegramList(t.Context(), 10, -1001)
	if err != nil || after == nil || !after.UpdatedAt.Equal(row.UpdatedAt) || string(after.Data) != string(row.Data) {
		t.Fatal("cancelled save damaged snapshot", err)
	}
	put(10, 0, `[]`, []int64{-1001})
	for _, key := range []struct {
		account, chat int64
		exists        bool
	}{{10, -1001, true}, {10, -1002, false}, {20, -1002, true}, {30, 0, false}} {
		row, err := GetTelegramList(t.Context(), key.account, key.chat)
		if err != nil || (row != nil) != key.exists {
			t.Fatalf("wrong invalidation/isolation: %+v %v", key, err)
		}
	}
	put(10, 0, `[]`, nil)
	if row, err := GetTelegramList(t.Context(), 10, -1001); err != nil || row != nil {
		t.Fatal("stale forum cache survived", err)
	}
	if row, err := GetTelegramList(t.Context(), 20, -1002); err != nil || row == nil {
		t.Fatal("other account cache removed", err)
	}
}

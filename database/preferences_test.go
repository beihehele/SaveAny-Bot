package database

import (
	"sync"
	"testing"
)

func TestPreferenceTransactionValidatesFinalPairAndRollsBack(t *testing.T) {
	previous := db
	db = migrationDB(t)
	t.Cleanup(func() { db = previous })
	if err := db.AutoMigrate(&User{}, &Dir{}, &Rule{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{42, 43} {
		if err := CreateUser(t.Context(), id); err != nil {
			t.Fatal(err)
		}
	}
	u, err := GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := GetUserByChatID(t.Context(), 43)
	if err != nil {
		t.Fatal(err)
	}
	a := Dir{UserID: u.ID, StorageName: "old", Path: "albums"}
	b := Dir{UserID: u.ID, StorageName: "new", Path: "new-albums"}
	foreign := Dir{UserID: other.ID, StorageName: "old", Path: "foreign"}
	for _, dir := range []*Dir{&a, &b, &foreign} {
		if err := db.Create(dir).Error; err != nil {
			t.Fatal(err)
		}
	}
	set := func(values map[string]any) {
		t.Helper()
		if err := UpdateUserPreferences(t.Context(), 42, values); err != nil {
			t.Fatal(err)
		}
	}
	check := func(storage string, dir uint) {
		t.Helper()
		u, err := GetUserByChatID(t.Context(), 42)
		if err != nil || u.DefaultStorage != storage || u.DefaultDir != dir {
			t.Fatalf("invalid pair: %+v %v", u, err)
		}
	}
	set(map[string]any{"default_storage": "old", "default_dir": a.ID})
	set(map[string]any{"default_storage": "old"})
	check("old", a.ID)
	for _, values := range []map[string]any{
		{"default_dir": b.ID, "apply_rule": true},
		{"default_dir": foreign.ID, "silent": true},
		{"default_dir": uint(999)},
		{"default_storage": "new", "default_dir": a.ID},
		{"default_storage": "", "silent": true},
	} {
		if err := UpdateUserPreferences(t.Context(), 42, values); err == nil {
			t.Fatalf("invalid patch committed: %v", values)
		}
		check("old", a.ID)
		u, err := GetUserByChatID(t.Context(), 42)
		if err != nil || u.Silent || u.ApplyRule {
			t.Fatal("failed validation did not roll back other fields")
		}
	}
	set(map[string]any{"default_storage": "new"})
	check("new", 0)
	set(map[string]any{"default_dir": b.ID})
	check("new", b.ID)
	set(map[string]any{"default_storage": "old", "default_dir": a.ID})
	check("old", a.ID)
	// Concurrent entry points must never commit new + the old directory. A
	// directory-only patch may be rejected when the storage switch wins first.
	for range 10 {
		set(map[string]any{"default_storage": "old", "default_dir": uint(0)})
		start := make(chan struct{})
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for _, values := range []map[string]any{{"default_dir": a.ID}, {"default_storage": "new"}} {
			wg.Add(1)
			go func() { defer wg.Done(); <-start; results <- UpdateUserPreferences(t.Context(), 42, values) }()
		}
		close(start)
		wg.Wait()
		if first, second := <-results, <-results; first != nil && second != nil {
			t.Fatalf("both concurrent updates failed: %v; %v", first, second)
		}
		u, err := GetUserByChatID(t.Context(), 42)
		if err != nil {
			t.Fatal(err)
		}
		if u.DefaultDir != 0 && (u.DefaultDir != a.ID || u.DefaultStorage != "old") {
			t.Fatalf("concurrent patch left invalid pair: %+v", u)
		}
	}
}

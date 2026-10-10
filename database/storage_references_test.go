package database

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/rule"
	"gorm.io/gorm"
)

func TestStorageReferenceValidationPreservesSettings(t *testing.T) {
	users := []User{{ChatID: 42, DefaultStorage: "archive", DefaultDir: 7,
		Dirs:       []Dir{{Model: gorm.Model{ID: 7}, StorageName: "archive", Path: "albums"}},
		Rules:      []Rule{{StorageName: rule.RuleStorNameChosen}, {StorageName: "archive"}},
		WatchChats: []WatchChat{{ChatID: 100, TargetID: 200, TargetTopicID: 300}}}}
	before, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateStorageReferences(t.Context(), users, map[string]struct{}{"archive": {}}); err != nil {
		t.Fatal(err)
	}
	after, err := json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("validation changed user settings")
	}
	err = validateStorageReferences(t.Context(), users, nil)
	for _, expected := range []string{"user 42 default", "directory 7", "rule", "archive"} {
		if err == nil || !strings.Contains(err.Error(), expected) {
			t.Fatalf("missing migration detail %q: %v", expected, err)
		}
	}
	after, err = json.Marshal(users)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("failed validation changed user settings")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := validateStorageReferences(ctx, users, nil); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

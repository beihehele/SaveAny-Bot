package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/rule"
)

// ValidateStorageReferences reports stale settings without rewriting user data.
// Startup calls it before opening Telegram connections or starting queue workers.
func ValidateStorageReferences(ctx context.Context) error {
	users, err := GetAllUsers(ctx)
	if err != nil {
		return fmt.Errorf("check stored storage references: %w", err)
	}
	names := make(map[string]struct{})
	for _, storage := range config.C().Storages {
		names[storage.GetName()] = struct{}{}
	}
	return validateStorageReferences(ctx, users, names)
}

func validateStorageReferences(ctx context.Context, users []User, names map[string]struct{}) error {
	var issues []error
	check := func(name, location string) {
		if _, exists := names[name]; !exists {
			issues = append(issues, fmt.Errorf("%s references unavailable storage %q", location, name))
		}
	}
	for _, user := range users {
		if err := ctx.Err(); err != nil {
			return err
		}
		if user.DefaultStorage != "" {
			check(user.DefaultStorage, fmt.Sprintf("user %d default", user.ChatID))
		}
		for _, dir := range user.Dirs {
			check(dir.StorageName, fmt.Sprintf("user %d directory %d", user.ChatID, dir.ID))
		}
		for _, r := range user.Rules {
			if r.StorageName != rule.RuleStorNameChosen {
				check(r.StorageName, fmt.Sprintf("user %d rule %d", user.ChatID, r.ID))
			}
		}
	}
	if len(issues) != 0 {
		return fmt.Errorf("stored settings need migration; preserve the database and correct the named storage references: %w", errors.Join(issues...))
	}
	return nil
}

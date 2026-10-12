package database

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func CreateUser(ctx context.Context, chatID int64) error {
	if _, err := GetUserByChatID(ctx, chatID); err == nil {
		return nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("find user %d: %w", chatID, err)
	}
	if err := db.WithContext(ctx).Create(&User{ChatID: chatID}).Error; err != nil {
		return fmt.Errorf("create user %d: %w", chatID, err)
	}
	return nil
}

func GetAllUsers(ctx context.Context) ([]User, error) {
	var users []User
	err := db.WithContext(ctx).
		Preload(clause.Associations).
		Find(&users).Error
	return users, err
}

func GetUserByChatID(ctx context.Context, chatID int64) (*User, error) {
	var user User
	err := db.WithContext(ctx).
		Preload(clause.Associations).
		Where("chat_id = ?", chatID).First(&user).Error
	return &user, err
}

// UpdateUserPreferences changes only explicitly named scalar preferences. It
// never saves associations, inserts a missing user or overwrites other fields.
func UpdateUserPreferences(ctx context.Context, chatID int64, values map[string]any) error {
	if len(values) == 0 {
		return fmt.Errorf("no preferences specified")
	}
	for key := range values {
		switch key {
		case "silent", "default_storage", "default_dir", "apply_rule", "filename_strategy", "filename_template", "conflict_strategy":
		default:
			return fmt.Errorf("unsupported preference: %s", key)
		}
	}
	patch := maps.Clone(values)
	if name, changed := patch["default_storage"]; changed {
		if _, explicit := patch["default_dir"]; !explicit {
			patch["default_dir"] = gorm.Expr("CASE WHEN default_storage <> ? THEN 0 ELSE default_dir END", name)
		}
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Write first: SQLite serializes writers before reading the resulting
		// preferences. Validation and commit cannot race another entry point.
		result := tx.Model(&User{}).Where("chat_id = ?", chatID).Updates(patch)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		var user User
		if err := tx.Select("id", "silent", "default_storage", "default_dir").Where("chat_id = ?", chatID).First(&user).Error; err != nil {
			return err
		}
		if user.Silent && user.DefaultStorage == "" {
			return fmt.Errorf("default storage required for silent mode")
		}
		_, storageChanged := values["default_storage"]
		_, directoryChanged := values["default_dir"]
		if user.DefaultDir != 0 && (storageChanged || directoryChanged) {
			var dir Dir
			if err := tx.Where("id = ? AND user_id = ? AND storage_name = ?", user.DefaultDir, user.ID, user.DefaultStorage).First(&dir).Error; err != nil {
				return fmt.Errorf("invalid default directory: %w", err)
			}
		}
		return nil
	})
}

func DeleteUser(ctx context.Context, user *User) error {
	return db.WithContext(ctx).
		Unscoped().
		Select(clause.Associations).
		Delete(user).Error
}

func GetUserByID(ctx context.Context, id uint) (*User, error) {
	var user User
	err := db.WithContext(ctx).
		Preload(clause.Associations).
		Where("id = ?", id).First(&user).Error
	return &user, err
}

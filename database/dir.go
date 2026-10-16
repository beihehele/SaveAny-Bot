package database

import (
	"context"

	"gorm.io/gorm"
)

func CreateDirForUser(ctx context.Context, userID uint, storageName, path string) error {
	dir := Dir{
		UserID:      userID,
		StorageName: storageName,
		Path:        path,
	}
	return db.WithContext(ctx).Create(&dir).Error
}

func GetDirByID(ctx context.Context, id uint) (*Dir, error) {
	dir := &Dir{}
	err := db.WithContext(ctx).First(dir, id).Error
	if err != nil {
		return nil, err
	}
	return dir, err
}

func GetUserDirs(ctx context.Context, userID uint) ([]Dir, error) {
	var dirs []Dir
	err := db.WithContext(ctx).Where("user_id = ?", userID).Find(&dirs).Error
	return dirs, err
}

func GetUserDirsByChatID(ctx context.Context, chatID int64) ([]Dir, error) {
	user, err := GetUserByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return GetUserDirs(ctx, user.ID)
}

func GetDirsByUserIDAndStorageName(ctx context.Context, userID uint, storageName string) ([]Dir, error) {
	var dirs []Dir
	err := db.WithContext(ctx).Where("user_id = ? AND storage_name = ?", userID, storageName).Find(&dirs).Error
	return dirs, err
}

func GetDirsByUserChatIDAndStorageName(ctx context.Context, chatID int64, storageName string) ([]Dir, error) {
	user, err := GetUserByChatID(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return GetDirsByUserIDAndStorageName(ctx, user.ID, storageName)
}

// UpdateDirForUser updates a shortcut without moving saved files. Switching its
// storage clears a default selection that would no longer belong to that storage.
func UpdateDirForUser(ctx context.Context, userID, id uint, storageName, path string) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&Dir{}).Where("id = ? AND user_id = ?", id, userID).
			Updates(map[string]any{"storage_name": storageName, "path": path})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&User{}).Where("id = ? AND default_dir = ? AND default_storage <> ?", userID, id, storageName).
			Update("default_dir", 0).Error
	})
}

// DeleteDirForUser removes only an owned shortcut and resets its default use to
// the storage root in the same transaction. Saved files are left in place.
func DeleteDirForUser(ctx context.Context, userID, id uint) error {
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Unscoped().Where("id = ? AND user_id = ?", id, userID).Delete(&Dir{})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}
		return tx.Model(&User{}).Where("id = ? AND default_dir = ?", userID, id).Update("default_dir", 0).Error
	})
}

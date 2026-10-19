package database

import (
	"context"
	"fmt"

	"github.com/krau/SaveAny-Bot/config"
)

func (user *User) WatchChat(ctx context.Context, chat WatchChat) error {
	if config.C().IsCollectionTarget(chat.ChatID) {
		return fmt.Errorf("TG collection target cannot be a watch source")
	}
	chat.UserID = user.ID
	// A command may hold a snapshot predating /unwatch. Saving that whole slice
	// would recreate deleted routes; only persist the newly requested route.
	if err := db.WithContext(ctx).Create(&chat).Error; err != nil {
		return err
	}
	user.WatchChats = append(user.WatchChats, chat)
	return nil
}

// WatchRouteActive checks the identity of a captured route before admission.
// CreatedAt distinguishes a replacement even if an imported database reuses IDs.
func WatchRouteActive(ctx context.Context, route WatchChat) (bool, error) {
	var current WatchChat
	result := db.WithContext(ctx).Select("created_at").
		Where("id = ? AND user_id = ?", route.ID, route.UserID).Find(&current)
	// Compare decoded instants: SQLite drivers and timezones can encode the
	// same instant differently.
	return result.RowsAffected > 0 && current.CreatedAt.Equal(route.CreatedAt), result.Error
}

func (user *User) WatchingRoute(ctx context.Context, sourceID, targetID int64, targetTopicID int) (bool, error) {
	var count int64
	err := db.WithContext(ctx).Model(&WatchChat{}).
		Where("chat_id = ? AND target_id = ? AND target_topic_id = ? AND user_id = ?", sourceID, targetID, targetTopicID, user.ID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (user *User) UnwatchByID(ctx context.Context, id uint) error {
	var watchChat WatchChat
	err := db.WithContext(ctx).Where("id = ? AND user_id = ?", id, user.ID).First(&watchChat).Error
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Unscoped().Delete(&watchChat).Error
}

func GetWatchChatsByChatID(ctx context.Context, chatID int64) ([]*WatchChat, error) {
	var watchChats []*WatchChat
	err := db.WithContext(ctx).Where("chat_id = ?", chatID).Find(&watchChats).Error
	if err != nil {
		return nil, err
	}
	return watchChats, nil
}

package database

import "context"

func (user *User) WatchChat(ctx context.Context, chat WatchChat) error {
	if len(user.WatchChats) == 0 {
		user.WatchChats = make([]WatchChat, 0)
	}

	user.WatchChats = append(user.WatchChats, chat)
	return db.WithContext(ctx).Save(user.WatchChats).Error
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

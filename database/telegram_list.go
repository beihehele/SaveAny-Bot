package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TelegramList stores display metadata only. ChatID zero is the account's chat
// list; a negative ChatID identifies that chat's Topic list.
type TelegramList struct {
	AccountID int64 `gorm:"primaryKey;autoIncrement:false"`
	ChatID    int64 `gorm:"primaryKey;autoIncrement:false"`
	Data      []byte
	UpdatedAt time.Time
}

// GetTelegramList reads the last complete snapshot without network access.
func GetTelegramList(ctx context.Context, accountID, chatID int64) (*TelegramList, error) {
	if accountID <= 0 || chatID > 0 {
		return nil, fmt.Errorf("invalid Telegram list key")
	}
	var row TelegramList
	err := db.WithContext(ctx).Where("account_id = ? AND chat_id = ?", accountID, chatID).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &row, err
}

// SaveTelegramList atomically replaces a complete snapshot. Chat refreshes
// also discard Topic caches for targets that are no longer managed forums.
func SaveTelegramList(ctx context.Context, accountID, chatID int64, data []byte, forums []int64) (time.Time, error) {
	if accountID <= 0 || chatID > 0 || len(data) == 0 {
		return time.Time{}, fmt.Errorf("invalid Telegram list snapshot")
	}
	row := TelegramList{AccountID: accountID, ChatID: chatID, Data: data, UpdatedAt: time.Now().UTC()}
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "account_id"}, {Name: "chat_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"data", "updated_at"}),
		}).Create(&row).Error; err != nil {
			return err
		}
		if chatID == 0 {
			obsolete := tx.Where("account_id = ? AND chat_id <> 0", accountID)
			if len(forums) > 0 {
				obsolete = obsolete.Where("chat_id NOT IN ?", forums)
			}
			return obsolete.Delete(&TelegramList{}).Error
		}
		return nil
	})
	return row.UpdatedAt, err
}

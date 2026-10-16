package database

import (
	"context"

	"gorm.io/gorm"
)

func CreateRule(ctx context.Context, rule *Rule) error {
	return db.WithContext(ctx).Create(rule).Error
}

// UpdateRuleForUser preserves the rule ID and owner when changing its settings.
func UpdateRuleForUser(ctx context.Context, rule *Rule) error {
	result := db.WithContext(ctx).Model(&Rule{}).Where("id = ? AND user_id = ?", rule.ID, rule.UserID).
		Updates(map[string]any{"type": rule.Type, "data": rule.Data, "storage_name": rule.StorageName, "dir_path": rule.DirPath})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

// DeleteRuleForUser deletes only a rule owned by the specified user.
func DeleteRuleForUser(ctx context.Context, userID, ruleID uint) error {
	result := db.WithContext(ctx).Unscoped().Where("id = ? AND user_id = ?", ruleID, userID).Delete(&Rule{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func UpdateUserApplyRule(ctx context.Context, chatID int64, applyRule bool) error {
	return db.WithContext(ctx).Model(&User{}).Where("chat_id = ?", chatID).Update("apply_rule", applyRule).Error
}

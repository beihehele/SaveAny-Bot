package database

import (
	"fmt"
	"slices"

	"gorm.io/gorm"
)

// migrateWatchRouteIndex rebuilds idx_watch_route so it includes target_topic_id.
// GORM AutoMigrate adds columns but does not recreate composite unique indexes when
// their column set changes.
func migrateWatchRouteIndex(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		migrator := tx.Migrator()
		if !migrator.HasTable(&WatchChat{}) {
			return nil
		}
		var indexes []struct {
			Name    string
			Unique  int
			Partial int
		}
		if err := tx.Raw("PRAGMA index_list('watch_chats')").Scan(&indexes).Error; err != nil {
			return err
		}
		for _, index := range indexes {
			if index.Name != "idx_watch_route" {
				continue
			}
			var columns []struct{ Name string }
			if err := tx.Raw("PRAGMA index_info('idx_watch_route')").Scan(&columns).Error; err != nil {
				return err
			}
			names := make([]string, 0, len(columns))
			for _, column := range columns {
				names = append(names, column.Name)
			}
			if index.Unique == 1 && index.Partial == 0 && slices.Equal(names, []string{"user_id", "chat_id", "target_id", "target_topic_id"}) {
				return nil
			}
			if err := migrator.DropIndex(&WatchChat{}, "idx_watch_route"); err != nil {
				return fmt.Errorf("drop idx_watch_route: %w", err)
			}
			break
		}
		if err := migrator.CreateIndex(&WatchChat{}, "idx_watch_route"); err != nil {
			return fmt.Errorf("create idx_watch_route: %w", err)
		}
		return nil
	})
}

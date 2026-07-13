package database

import (
	"fmt"

	"gorm.io/gorm"
)

// migrateWatchRouteIndex rebuilds idx_watch_route so it includes target_topic_id.
// GORM AutoMigrate adds columns but does not recreate composite unique indexes when
// their column set changes.
func migrateWatchRouteIndex(db *gorm.DB) error {
	migrator := db.Migrator()
	if !migrator.HasTable(&WatchChat{}) {
		return nil
	}
	if migrator.HasIndex(&WatchChat{}, "idx_watch_route") {
		if err := migrator.DropIndex(&WatchChat{}, "idx_watch_route"); err != nil {
			return fmt.Errorf("drop idx_watch_route: %w", err)
		}
	}
	if err := migrator.CreateIndex(&WatchChat{}, "idx_watch_route"); err != nil {
		return fmt.Errorf("create idx_watch_route: %w", err)
	}
	return nil
}

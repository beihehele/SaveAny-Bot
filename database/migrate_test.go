package database

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func migrationDB(t *testing.T) *gorm.DB {
	return migrationDBAt(t, filepath.Join(t.TempDir(), "migration.db"))
}

func migrationDBAt(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(GetDialect(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := db.AutoMigrate(&WatchChat{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOfflineBackupRestoreAndMigration(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source := migrationDBAt(t, sourcePath)
	if err := source.Exec("DROP INDEX idx_watch_route").Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Exec("CREATE UNIQUE INDEX idx_watch_route ON watch_chats (user_id, chat_id, target_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := source.Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 3, TargetTopicID: 4, Filter: "restore-me"}).Error; err != nil {
		t.Fatal(err)
	}
	conn, err := source.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// Copy only after shutdown, as required for the documented offline backup.
	data, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restoredPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	restored := migrationDBAt(t, restoredPath)
	if err := migrateWatchRouteIndex(restored); err != nil {
		t.Fatal(err)
	}
	var route WatchChat
	if err := restored.First(&route).Error; err != nil || route.Filter != "restore-me" || route.TargetTopicID != 4 {
		t.Fatalf("restore changed data: %+v %v", route, err)
	}
}

func TestWatchRouteMigrationPreservesTopicsAndIsIdempotent(t *testing.T) {
	db := migrationDB(t)
	if err := db.Exec("DROP INDEX idx_watch_route").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE UNIQUE INDEX idx_watch_route ON watch_chats (user_id, chat_id, target_id)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 3, TargetTopicID: 4, Filter: "existing"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateWatchRouteIndex(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 3, TargetTopicID: 5}).Error; err != nil {
		t.Fatalf("distinct topic rejected: %v", err)
	}
	if err := db.Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 3, TargetTopicID: 4}).Error; err == nil {
		t.Fatal("duplicate route accepted")
	}
	var before, after int
	db.Raw("PRAGMA schema_version").Scan(&before)
	if err := migrateWatchRouteIndex(db); err != nil {
		t.Fatal(err)
	}
	db.Raw("PRAGMA schema_version").Scan(&after)
	if before != after {
		t.Fatal("correct index rebuilt on startup")
	}
	var route WatchChat
	if err := db.Where("target_topic_id = ?", 4).First(&route).Error; err != nil || route.Filter != "existing" {
		t.Fatalf("route changed: %+v %v", route, err)
	}
}

func TestWatchRouteMigrationFailureRollsBack(t *testing.T) {
	db := migrationDB(t)
	if err := db.Exec("DROP INDEX idx_watch_route").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE INDEX idx_watch_route ON watch_chats (user_id, chat_id, target_id)").Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := db.Create(&WatchChat{UserID: 1, ChatID: 2, TargetID: 3, TargetTopicID: 4}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateWatchRouteIndex(db); err == nil {
		t.Fatal("duplicate data silently migrated")
	}
	var columns []struct{ Name string }
	if err := db.Raw("PRAGMA index_info('idx_watch_route')").Scan(&columns).Error; err != nil || len(columns) != 3 {
		t.Fatalf("old index lost: %+v %v", columns, err)
	}
	var count int64
	if err := db.Model(&WatchChat{}).Count(&count).Error; err != nil || count != 2 {
		t.Fatalf("data changed: %d %v", count, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := migrateWatchRouteIndex(db.WithContext(ctx)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
}

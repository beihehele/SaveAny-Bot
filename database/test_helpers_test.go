package database

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func testDatabase(t *testing.T) *gorm.DB {
	return testDatabaseAt(t, filepath.Join(t.TempDir(), "fixture.db"))
}

func testDatabaseAt(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(GetDialect(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&WatchChat{}); err != nil {
		t.Fatal(err)
	}
	return db
}

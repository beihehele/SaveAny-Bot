package database

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// ShardReelTask persists the final outcome of a download and its file uploads.
type ShardReelTask struct {
	ID              string    `gorm:"primaryKey" json:"task_id"`
	Title           string    `json:"title"`
	Source          string    `json:"source"`
	ChatID          int64     `json:"-"`
	MessageID       int       `json:"-"`
	Status          string    `json:"status"`
	Error           string    `json:"error"`
	Summary         string    `json:"-"`
	DownloadDone    bool      `json:"-"`
	CancelRequested bool      `json:"-"`
	Notified        bool      `json:"-"`
	HookDone        bool      `json:"-"`
	ResultVersion   int64     `gorm:"not null;default:0" json:"-"`
	ExpectedFiles   int       `json:"-"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ShardReelFile owns exactly one staged payload and one remote request ID.
type ShardReelFile struct {
	ID            string    `gorm:"primaryKey" json:"id"`
	TaskID        string    `gorm:"index" json:"task_id"`
	ElementID     string    `json:"-"`
	Conflict      string    `json:"-"`
	Storage       string    `gorm:"index" json:"storage"`
	Endpoint      string    `json:"-"`
	Spool         string    `json:"-"`
	Path          string    `json:"path"`
	SavedPath     string    `json:"saved_path,omitempty"`
	Size          int64     `json:"size"`
	SHA256        string    `json:"sha256"`
	Status        string    `gorm:"index" json:"status"`
	Error         string    `json:"error"`
	Attempts      int       `json:"attempts"`
	UploadedBytes int64     `json:"uploaded_bytes"`
	NextAttemptAt time.Time `json:"next_attempt_at"`
	RemoteStarted bool      `json:"-"`
	LocalClean    bool      `json:"local_clean"`
	CleanupError  string    `json:"cleanup_error"`
	RemoteAcked   bool      `json:"-"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// ShardReelDB is the application's store for the ShardReel transfer lifecycle.
func ShardReelDB(ctx context.Context) *gorm.DB {
	if db == nil {
		return nil
	}
	return db.WithContext(ctx)
}

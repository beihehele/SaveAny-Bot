package database

import (
	"gorm.io/gorm"
)

type User struct {
	gorm.Model
	ChatID           int64 `gorm:"uniqueIndex;not null"`
	Silent           bool
	DefaultStorage   string
	DefaultDir       uint // Dir.ID
	Dirs             []Dir
	ApplyRule        bool
	Rules            []Rule
	WatchChats       []WatchChat
	FilenameStrategy string
	FilenameTemplate string
	ConflictStrategy string
}

type WatchChat struct {
	gorm.Model
	UserID          uint   `gorm:"uniqueIndex:idx_watch_route;not null"`
	ChatID          int64  `gorm:"uniqueIndex:idx_watch_route;not null"` // source
	SourceName      string // resolved at /watch time
	TargetID        int64  `gorm:"uniqueIndex:idx_watch_route;not null;default:0"`
	TargetName      string // resolved at /watch time; "本地" when TargetID is 0
	TargetTopicID   int    `gorm:"uniqueIndex:idx_watch_route;not null;default:0"` // 0=unspecified; non-zero=top_msg_id
	TargetTopicName string // resolved at /watch time; empty when TargetTopicID is 0
	Filter          string
}

type Dir struct {
	gorm.Model
	UserID      uint
	StorageName string
	Path        string
}

type Rule struct {
	gorm.Model
	UserID      uint
	Type        string
	Data        string
	StorageName string
	DirPath     string
}

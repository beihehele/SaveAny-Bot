package tcbdata

import (
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

const (
	TypeAdd        = "add"
	TypeSetDefault = "setdefault"
	TypeConfig     = "config"
	TypeCancel     = "cancel"
)

const (
	ConflictStrategyRename    = "rename"
	ConflictStrategyAsk       = "ask"
	ConflictStrategyOverwrite = "overwrite"
	ConflictStrategySkip      = "skip"
)

func ConflictStrategyValues() []string {
	return []string{
		ConflictStrategyRename,
		ConflictStrategyAsk,
		ConflictStrategyOverwrite,
		ConflictStrategySkip,
	}
}

func IsConflictStrategy(strategy string) bool {
	for _, value := range ConflictStrategyValues() {
		if strategy == value {
			return true
		}
	}
	return false
}

type Add struct {
	TaskType         tasktype.TaskType
	SelectedStorName string
	DirID            uint
	SettedDir        bool
	SelectedDirPath  string
	ConflictStrategy string
	// tfiles
	Files   []tfile.TGFileMessage
	AsBatch bool
}

type SetDefaultStorage struct {
	StorageName string
	DirID       uint
}

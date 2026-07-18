package copyfwd

import "sync"

// activeByUser maps user chat ID -> running copy task ID.
// userByTask is the reverse index so cancel paths can release without Execute.
var (
	activeByUser sync.Map // int64 -> string
	userByTask   sync.Map // string -> int64
)

// TryBegin registers a running copy for the user. Returns false if one is already active.
func TryBegin(userChatID int64, taskID string) bool {
	_, loaded := activeByUser.LoadOrStore(userChatID, taskID)
	if loaded {
		return false
	}
	userByTask.Store(taskID, userChatID)
	return true
}

// End clears the running copy for the user if it matches taskID.
func End(userChatID int64, taskID string) {
	v, ok := activeByUser.Load(userChatID)
	if !ok {
		return
	}
	if id, _ := v.(string); id != taskID {
		return
	}
	activeByUser.Delete(userChatID)
	userByTask.Delete(taskID)
}

// EndByTaskID releases the per-user slot when a copy task is cancelled
// before Execute runs (e.g. still queued).
func EndByTaskID(taskID string) {
	v, ok := userByTask.Load(taskID)
	if !ok {
		return
	}
	userChatID, _ := v.(int64)
	End(userChatID, taskID)
}

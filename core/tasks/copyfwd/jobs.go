package copyfwd

import "sync"

// activeByUser maps user chat ID -> running copy task ID.
var activeByUser sync.Map

// TryBegin registers a running copy for the user. Returns false if one is already active.
func TryBegin(userChatID int64, taskID string) bool {
	_, loaded := activeByUser.LoadOrStore(userChatID, taskID)
	return !loaded
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
}

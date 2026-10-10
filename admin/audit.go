package admin

import (
	"sync"
	"time"
)

type auditEntry struct {
	Time   time.Time `json:"time"`
	Action string    `json:"action"`
	Object string    `json:"object,omitempty"`
	Result string    `json:"result"`
}

type auditLog struct {
	mu      sync.Mutex
	entries []auditEntry
}

func (a *auditLog) add(action, object, result string) {
	if len(object) > 512 {
		object = object[:512]
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.entries) == 100 {
		copy(a.entries, a.entries[1:])
		a.entries = a.entries[:99]
	}
	a.entries = append(a.entries, auditEntry{Time: time.Now(), Action: action, Object: object, Result: result})
}

func (a *auditLog) snapshot() []auditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	entries := make([]auditEntry, len(a.entries))
	for i := range a.entries {
		entries[len(a.entries)-1-i] = a.entries[i]
	}
	return entries
}

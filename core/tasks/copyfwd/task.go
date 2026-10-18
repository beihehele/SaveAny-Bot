package copyfwd

import (
	"fmt"
	"strings"
	"sync"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

var _ core.Executable = (*Task)(nil)
var _ taskresult.Provider = (*Task)(nil)

type Task struct {
	ID            string
	UserChatID    int64
	SourceID      int64
	TargetID      int64
	TargetTopicID int
	Filter        string
	Count         int
	Progress      ProgressTracker
	SourceName    string
	TargetName    string
	TopicName     string
	resultMu      sync.RWMutex
	result        taskresult.Summary
}

func (t *Task) Type() tasktype.TaskType { return tasktype.TaskTypeCopy }

func (t *Task) TaskID() string { return t.ID }

// Discard releases ownership when cancellation prevents Execute from running.
func (t *Task) Discard() { End(t.UserChatID, t.ID) }

func (t *Task) Title() string {
	chat := func(name string, id int64) string {
		if name = strings.Join(strings.Fields(name), " "); name != "" {
			return fmt.Sprintf("%s(%d)", name, id)
		}
		return fmt.Sprintf("%d", id)
	}
	filter := t.Filter
	if filter == "" {
		filter = i18n.T(i18nk.BotMsgCopyAllMedia)
	}
	title := fmt.Sprintf("%s %s → %s", chat(t.SourceName, t.SourceID), filter, chat(t.TargetName, t.TargetID))
	if t.TargetTopicID > 0 {
		topic := strings.Join(strings.Fields(t.TopicName), " ")
		if topic != "" {
			title += fmt.Sprintf(" Topic:%s(%d)", topic, t.TargetTopicID)
		} else {
			title += fmt.Sprintf(" Topic:%d", t.TargetTopicID)
		}
	}
	return title
}

func NewTask(
	id string,
	userChatID int64,
	sourceID int64,
	targetID int64,
	targetTopicID int,
	filter string,
	count int,
	progress ProgressTracker,
) *Task {
	return &Task{
		ID:            id,
		UserChatID:    userChatID,
		SourceID:      sourceID,
		TargetID:      targetID,
		TargetTopicID: targetTopicID,
		Filter:        filter,
		Count:         count,
		Progress:      progress,
	}
}

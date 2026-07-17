package copyfwd

import (
	"context"
	"fmt"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
)

var _ core.Executable = (*Task)(nil)

type Task struct {
	ID            string
	UserChatID    int64
	SourceID      int64
	TargetID      int64
	TargetTopicID int
	Filter        string
	Count         int
	ProgressChat  int64
	ProgressMsgID int
	Progress      ProgressTracker
}

func (t *Task) Type() tasktype.TaskType { return tasktype.TaskTypeCopy }

func (t *Task) TaskID() string { return t.ID }

func (t *Task) Title() string {
	return fmt.Sprintf("[copy] %d -> %d (n=%d)", t.SourceID, t.TargetID, t.Count)
}

func (t *Task) Execute(ctx context.Context) error { return nil }

func NewTask(
	id string,
	userChatID int64,
	sourceID int64,
	targetID int64,
	targetTopicID int,
	filter string,
	count int,
	progressChat int64,
	progressMsgID int,
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
		ProgressChat:  progressChat,
		ProgressMsgID: progressMsgID,
		Progress:      progress,
	}
}

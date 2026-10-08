package transfer

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/rs/xid"
)

var _ core.Executable = (*Task)(nil)

type TaskElement struct {
	ID            string
	SourceStorage storage.Storage
	SourcePath    string
	FileInfo      storagetypes.FileInfo
	TargetStorage storage.Storage
	TargetPath    string
}

type Task struct {
	ID           string
	ctx          context.Context
	elems        []TaskElement
	Progress     ProgressTracker
	IgnoreErrors bool
	uploaded     atomic.Int64
	totalSize    int64
	processing   map[string]TaskElementInfo
	processingMu sync.RWMutex
	failed       map[string]error
	results      taskresult.Tracker
}

// Title implements core.Executable.
func (t *Task) Title() string {
	return fmt.Sprintf("[%s](%d files/%.2fMB)", t.Type(), len(t.elems), float64(t.totalSize)/(1024*1024))
}

// Type implements core.Executable.
func (t *Task) Type() tasktype.TaskType {
	return tasktype.TaskTypeTransfer
}

// TaskID implements core.Executable.
func (t *Task) TaskID() string {
	return t.ID
}

func NewTaskElement(
	sourceStorage storage.Storage,
	fileInfo storagetypes.FileInfo,
	targetStorage storage.Storage,
	targetPath string,
) *TaskElement {
	id := xid.New().String()
	return &TaskElement{
		ID:            id,
		SourceStorage: sourceStorage,
		SourcePath:    fileInfo.Path,
		FileInfo:      fileInfo,
		TargetStorage: targetStorage,
		TargetPath:    targetPath,
	}
}

func NewTransferTask(
	id string,
	ctx context.Context,
	elems []TaskElement,
	progress ProgressTracker,
	ignoreErrors bool,
) *Task {
	task := &Task{
		ID:       id,
		ctx:      ctx,
		elems:    elems,
		Progress: progress,
		uploaded: atomic.Int64{},
		totalSize: func() int64 {
			var total int64
			for _, elem := range elems {
				total += elem.FileInfo.Size
			}
			return total
		}(),
		processing:   make(map[string]TaskElementInfo),
		IgnoreErrors: ignoreErrors,
		failed:       make(map[string]error),
	}
	task.results.Reset(task.resultElements())
	return task
}

func (t *Task) resultElements() []taskresult.Element {
	elements := make([]taskresult.Element, len(t.elems))
	for i, elem := range t.elems {
		elements[i] = taskresult.Element{ID: elem.ID, Name: elem.FileInfo.Name}
	}
	return elements
}

// ResultSummary reports file outcomes without changing IgnoreErrors or hooks.
func (t *Task) ResultSummary() taskresult.Summary { return t.results.Snapshot() }

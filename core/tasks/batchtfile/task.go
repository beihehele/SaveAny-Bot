package batchtfile

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/rs/xid"
)

var _ core.Executable = (*Task)(nil)

type TaskElement struct {
	ID       string
	Storage  storage.Storage
	Path     string
	File     tfile.TGFile
	cacheDir string
	stream   bool
}

type Task struct {
	ID           string
	ctx          context.Context
	elems        []TaskElement
	Progress     ProgressTracker
	downloaded   atomic.Int64
	totalSize    int64
	processing   map[string]TaskElementInfo
	processingMu sync.RWMutex
	results      taskresult.Tracker
}

// Title implements core.Exectable.
func (t *Task) Title() string {
	return fmt.Sprintf("[%s](%d files/%.2fMB)", t.Type(), len(t.elems), float64(t.totalSize)/(1024*1024))
}

func (t *Task) Type() tasktype.TaskType {
	return tasktype.TaskTypeTgfiles
}

func NewTaskElement(
	stor storage.Storage,
	path string,
	file tfile.TGFile,
) (*TaskElement, error) {
	id := xid.New().String()
	_, ok := stor.(storage.StorageCannotStream)
	if !config.C().Stream || ok {
		cacheDir, err := filepath.Abs(config.C().Temp.BasePath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for cache: %w", err)
		}
		return &TaskElement{
			ID:       id,
			Storage:  stor,
			Path:     path,
			File:     file,
			cacheDir: cacheDir,
		}, nil
	}
	return &TaskElement{
		ID:      id,
		Storage: stor,
		Path:    path,
		File:    file,
		stream:  true,
	}, nil
}

func NewBatchTGFileTask(
	id string,
	ctx context.Context,
	files []TaskElement,
	progress ProgressTracker,
) *Task {
	task := &Task{
		ID:         id,
		ctx:        ctx,
		elems:      files,
		Progress:   progress,
		downloaded: atomic.Int64{},
		totalSize: func() int64 {
			var total int64
			for _, elem := range files {
				total += elem.File.Size()
			}
			return total
		}(),
		processing:   make(map[string]TaskElementInfo),
		processingMu: sync.RWMutex{},
	}
	task.results.Reset(task.resultElements())
	return task
}

func (t *Task) resultElements() []taskresult.Element {
	elements := make([]taskresult.Element, len(t.elems))
	for i, elem := range t.elems {
		elements[i] = taskresult.Element{ID: elem.ID, Name: elem.File.Name()}
	}
	return elements
}

// ResultSummary reports file outcomes without changing scheduling or hooks.
func (t *Task) ResultSummary() taskresult.Summary { return t.results.Snapshot() }

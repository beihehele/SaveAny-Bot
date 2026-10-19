package tfile

import (
	"fmt"
	"path/filepath"
	"sync"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

var _ core.Executable = (*Task)(nil)

type Task struct {
	ID       string
	File     tfile.TGFile
	Storage  storage.Storage
	Path     string
	Progress ProgressTracker
	stream   bool // true if the file should be downloaded in stream mode
	cacheDir string
	resultMu sync.Mutex
	links    []string
}

func (t *Task) ResultLinks() []string {
	t.resultMu.Lock()
	defer t.resultMu.Unlock()
	return append([]string(nil), t.links...)
}

// Title implements core.Exectable.
func (t *Task) Title() string {
	if target, ok := t.Storage.(interface{ Destination() string }); ok {
		return fmt.Sprintf("[%s](%s → %s)", t.Type(), t.File.Name(), target.Destination())
	}
	return fmt.Sprintf("[%s](%s->%s:%s)", t.Type(), t.File.Name(), t.Storage.Name(), t.Path)
}

func (t *Task) Type() tasktype.TaskType {
	return tasktype.TaskTypeTgfiles
}

// UsesShardReel reports whether completion must wait for durable upload handoff.
func (t *Task) UsesShardReel() bool { return t.Storage.Type() == storenum.Shardreel }

func NewTGFileTask(
	id string,
	file tfile.TGFile,
	stor storage.Storage,
	path string,
	progress ProgressTracker,
) (*Task, error) {
	_, ok := stor.(storage.StorageCannotStream)
	if !config.C().Stream || ok {
		cacheDir, err := filepath.Abs(config.C().Temp.BasePath)
		if err != nil {
			return nil, fmt.Errorf("failed to get absolute path for cache: %w", err)
		}
		tfile := &Task{
			ID:       id,
			File:     file,
			Storage:  stor,
			Path:     path,
			Progress: progress,
			cacheDir: cacheDir,
		}
		return tfile, nil
	}
	tfileTask := &Task{
		ID:       id,
		File:     file,
		Storage:  stor,
		Path:     path,
		Progress: progress,
		stream:   true,
	}
	return tfileTask, nil
}

package batchtfile

import (
	"sync"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestProcessingConcurrentUpdates(t *testing.T) {
	elem := &TaskElement{File: tfile.NewTGFile(&tg.InputDocumentFileLocation{}, nil, 4, "file.bin")}
	task := &Task{processing: make(map[string]TaskElementInfo)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			task.processingMu.Lock()
			task.processing["file"] = elem
			task.processingMu.Unlock()
			task.processingMu.Lock()
			delete(task.processing, "file")
			task.processingMu.Unlock()
		}
	}()
	for range 1000 {
		for _, info := range task.Processing() {
			if info.FileName() != "file.bin" {
				t.Errorf("unexpected file: %s", info.FileName())
			}
		}
	}
	wg.Wait()
}

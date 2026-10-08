package parsed

import (
	"sync"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/parser"
)

func TestProcessingReturnsSnapshot(t *testing.T) {
	resource := &parser.Resource{Filename: "file.bin"}
	task := &Task{processing: map[string]ResourceInfo{"file": resource}}
	snapshot := task.Processing()
	delete(snapshot, "file")
	if task.processing["file"] == nil {
		t.Fatal("changing a snapshot changed task tracking")
	}
	snapshot = task.Processing()
	task.processingMu.Lock()
	delete(task.processing, "file")
	task.processingMu.Unlock()
	if snapshot["file"] == nil {
		t.Fatal("task cleanup changed a previously returned snapshot")
	}
}

func TestProcessingConcurrentUpdates(t *testing.T) {
	resource := &parser.Resource{Filename: "file.bin"}
	task := &Task{processing: make(map[string]ResourceInfo)}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 1000 {
			task.processingMu.Lock()
			task.processing["file"] = resource
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

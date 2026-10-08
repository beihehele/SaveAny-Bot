package queue

import (
	"context"
	"testing"
)

func TestDoneReleasesTaskContext(t *testing.T) {
	qe := NewTaskQueue[string]()
	task := NewTask(context.Background(), "finished", "finished", "data")
	if err := qe.Add(task); err != nil {
		t.Fatal(err)
	}
	if _, err := qe.Get(); err != nil {
		t.Fatal(err)
	}
	qe.Done(task.ID)
	select {
	case <-task.Context().Done():
	default:
		t.Fatal("completed task retained its live child context")
	}
	if qe.IsExecuting(task.ID) {
		t.Fatal("completed task remains executing")
	}
	qe.Done(task.ID)
	qe.Close()
}

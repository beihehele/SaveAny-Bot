package queue_test

import (
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/queue"
)

// Cancelling every queued task must not deadlock Get; a later Add should wake the worker.
func TestGetAfterCancelAllQueued(t *testing.T) {
	q := queue.NewTaskQueue[int]()
	if err := q.Add(newTask("only")); err != nil {
		t.Fatal(err)
	}
	if err := q.CancelTask("only"); err != nil {
		t.Fatal(err)
	}

	done := make(chan *queue.Task[int], 1)
	errCh := make(chan error, 1)
	go func() {
		task, err := q.Get()
		if err != nil {
			errCh <- err
			return
		}
		done <- task
	}()

	// Add from another goroutine so a stuck Lock in Get cannot block the test forever.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = q.Add(newTask("next"))
	}()

	select {
	case task := <-done:
		if task.ID != "next" {
			t.Fatalf("got task %q want next", task.ID)
		}
	case err := <-errCh:
		t.Fatalf("Get error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Get did not unblock after Add (likely recursive Lock deadlock in Get)")
	}
}

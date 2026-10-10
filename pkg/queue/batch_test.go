package queue

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestAddBatchRejectsWithoutPublishingPrefix(t *testing.T) {
	for _, state := range []string{"queued_duplicate", "running_duplicate", "group_duplicate", "cancelled", "nil", "closed"} {
		t.Run(state, func(t *testing.T) {
			qe := NewTaskQueue[int]()
			t.Cleanup(qe.CloseAndCancel)
			first := NewTask(t.Context(), "first", "first", 1)
			last := NewTask(t.Context(), "last", "last", 2)
			t.Cleanup(first.Cancel)
			t.Cleanup(last.Cancel)
			var existing *Task[int]
			wantLength := 0
			if state == "queued_duplicate" || state == "running_duplicate" {
				existing = NewTask(t.Context(), last.ID, "existing", 0)
				if err := qe.Add(existing); err != nil {
					t.Fatal(err)
				}
				wantLength = 1
				if state == "running_duplicate" {
					if _, err := qe.Get(); err != nil {
						t.Fatal(err)
					}
					wantLength = 0
				}
			}
			group := []*Task[int]{first, last}
			switch state {
			case "group_duplicate":
				last.ID = first.ID
			case "cancelled":
				last.Cancel()
			case "nil":
				group[1] = nil
			case "closed":
				qe.Close()
			}
			if err := qe.AddBatch(group...); err == nil {
				t.Fatal("invalid group accepted")
			}
			if qe.Length() != wantLength || first.element != nil || last.element != nil {
				t.Fatal("rejected group published a prefix or changed existing work")
			}
			if first.Context().Err() != nil || (state != "cancelled" && last.Context().Err() != nil) {
				t.Fatal("rejection took ownership of caller contexts")
			}
			if existing != nil && existing.Context().Err() != nil {
				t.Fatal("rejection cancelled existing work")
			}
		})
	}
}

func TestAddBatchPreservesFIFOAndCompletion(t *testing.T) {
	qe := NewTaskQueue[int]()
	t.Cleanup(qe.CloseAndCancel)
	tasks := []*Task[int]{NewTask(t.Context(), "solo", "solo", 0)}
	for index := 1; index < 4; index++ {
		tasks = append(tasks, NewTask(t.Context(), fmt.Sprint(index), "member", index))
	}
	if err := qe.Add(tasks[0]); err != nil {
		t.Fatal(err)
	}
	if err := qe.AddBatch(tasks[1:]...); err != nil {
		t.Fatal(err)
	}
	qe.Close()
	for _, want := range tasks {
		got, err := qe.Get()
		if err != nil || got != want || got.Context().Err() != nil {
			t.Fatalf("group order or context changed: got=%v err=%v", got, err)
		}
		qe.Done(got.ID)
		if got.Context().Err() == nil {
			t.Fatal("completed member retained its context")
		}
	}
	if _, err := qe.Get(); err == nil {
		t.Fatal("closed empty queue supplied work")
	}
	if err := qe.AddBatch(); err != nil || qe.Length() != 0 {
		t.Fatal("empty group changed a closed queue")
	}
}

type batchWaitLocker struct {
	mu      *sync.RWMutex
	waiting chan struct{}
}

func (l batchWaitLocker) Lock() { l.mu.Lock() }
func (l batchWaitLocker) Unlock() {
	// Cond.Wait has registered its waiter before calling Unlock.
	l.waiting <- struct{}{}
	l.mu.Unlock()
}

func TestAddBatchWakesEveryIdleWorker(t *testing.T) {
	qe := NewTaskQueue[int]()
	waiting := make(chan struct{}, 3)
	qe.cond.L = batchWaitLocker{mu: &qe.mu, waiting: waiting}
	results := make(chan error, 3)
	var wg sync.WaitGroup
	t.Cleanup(func() { qe.CloseAndCancel(); wg.Wait() })
	for range 3 {
		wg.Go(func() {
			task, err := qe.Get()
			if err == nil {
				qe.Done(task.ID)
			}
			results <- err
		})
	}
	for range 3 {
		select {
		case <-waiting:
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not enter the queue wait")
		}
	}
	var tasks []*Task[int]
	for index := range 3 {
		tasks = append(tasks, NewTask(t.Context(), fmt.Sprint(index), "member", index))
	}
	if err := qe.AddBatch(tasks...); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		select {
		case err := <-results:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(3 * time.Second):
			t.Fatal("group left idle workers asleep")
		}
	}
}

func TestConcurrentAddBatchDoesNotInterleaveMembers(t *testing.T) {
	qe := NewTaskQueue[int]()
	t.Cleanup(qe.CloseAndCancel)
	var wg sync.WaitGroup
	for group := range 32 {
		wg.Go(func() {
			var tasks []*Task[int]
			for member := range 3 {
				tasks = append(tasks, NewTask(t.Context(), fmt.Sprintf("%d/%d", group, member), "member", group))
			}
			if err := qe.AddBatch(tasks...); err != nil {
				t.Error(err)
				for _, task := range tasks {
					task.Cancel()
				}
			}
		})
	}
	wg.Wait()
	qe.Close()
	for range 32 {
		group := -1
		for member := range 3 {
			task, err := qe.Get()
			if err != nil {
				t.Fatal(err)
			}
			if member == 0 {
				group = task.Data
			}
			if task.Data != group || task.ID != fmt.Sprintf("%d/%d", group, member) {
				t.Fatal("concurrent admissions interleaved group members")
			}
			qe.Done(task.ID)
		}
	}
}

func TestAddBatchRacingShutdownKeepsOwnershipConsistent(t *testing.T) {
	for range 64 {
		qe := NewTaskQueue[int]()
		tasks := []*Task[int]{NewTask(t.Context(), "first", "first", 1), NewTask(t.Context(), "last", "last", 2)}
		start := make(chan struct{})
		result := make(chan error, 1)
		go func() { <-start; result <- qe.AddBatch(tasks...) }()
		close(start)
		qe.CloseAndCancel()
		err := <-result
		if qe.Length() != 0 || len(qe.taskMap) != 0 {
			t.Fatal("shutdown retained admitted group members")
		}
		for _, task := range tasks {
			cancelled := task.Context().Err() != nil
			task.Cancel()
			if cancelled != (err == nil) {
				t.Fatal("shutdown and admission disagreed about group ownership")
			}
		}
	}
}

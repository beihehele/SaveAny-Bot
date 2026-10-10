package queue

import (
	"container/list"
	"errors"
	"fmt"
	"sync"
)

type TaskQueue[T any] struct {
	tasks          *list.List
	taskMap        map[string]*Task[T]
	runningTaskMap map[string]*Task[T]
	mu             sync.RWMutex
	cond           *sync.Cond
	closed         bool
}

func NewTaskQueue[T any]() *TaskQueue[T] {
	tq := &TaskQueue[T]{
		tasks:          list.New(),
		taskMap:        make(map[string]*Task[T]),
		runningTaskMap: make(map[string]*Task[T]),
	}
	tq.cond = sync.NewCond(&tq.mu)
	return tq
}

// Add takes ownership only on success; rejection does not cancel the task.
// Cancelling on rejection could cancel an accepted task resubmitted by pointer.
func (tq *TaskQueue[T]) Add(task *Task[T]) error {
	return tq.AddBatch(task)
}

// AddBatch accepts all tasks in order or rejects the entire group without
// changing queue state or taking ownership. An empty group is a no-op.
func (tq *TaskQueue[T]) AddBatch(tasks ...*Task[T]) error {
	if len(tasks) == 0 {
		return nil
	}
	tq.mu.Lock()
	defer tq.mu.Unlock()

	if tq.closed {
		return errors.New("queue is closed")
	}

	seen := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if task == nil {
			return fmt.Errorf("nil task at index %d", index)
		}
		if _, exists := tq.taskMap[task.ID]; exists {
			return fmt.Errorf("task with ID %s already exists", task.ID)
		}
		if _, exists := seen[task.ID]; exists {
			return fmt.Errorf("task with ID %s repeated in group", task.ID)
		}
		if task.Cancelled() {
			return fmt.Errorf("task %s has been cancelled", task.ID)
		}
		seen[task.ID] = struct{}{}
	}

	for _, task := range tasks {
		task.element = tq.tasks.PushBack(task)
		tq.taskMap[task.ID] = task
	}

	if len(tasks) == 1 {
		tq.cond.Signal()
	} else {
		// Each member remains an independent task; wake all idle workers.
		tq.cond.Broadcast()
	}
	return nil
}

// Get retrieves and removes the next non-cancelled task from the queue, adding it to the running tasks.
// Blocks until a task is available or the queue is closed.
func (tq *TaskQueue[T]) Get() (*Task[T], error) {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	for {
		for tq.tasks.Len() == 0 && !tq.closed {
			tq.cond.Wait()
		}

		if tq.closed && tq.tasks.Len() == 0 {
			return nil, fmt.Errorf("queue is closed and empty")
		}

		for tq.tasks.Len() > 0 {
			element := tq.tasks.Front()
			task := element.Value.(*Task[T])

			tq.tasks.Remove(element)
			task.element = nil

			if task.Cancelled() {
				// Drop cancelled tasks that never ran; do not recurse while holding mu.
				delete(tq.taskMap, task.ID)
				continue
			}
			tq.runningTaskMap[task.ID] = task
			return task, nil
		}

		// Drained only cancelled entries; wait for a new Add unless closed.
		if tq.closed {
			return nil, fmt.Errorf("queue is closed and empty")
		}
	}
}

// Done stops(cancels) and removes the task from the running tasks.
func (tq *TaskQueue[T]) Done(taskID string) {
	tq.mu.Lock()
	task := tq.runningTaskMap[taskID]
	delete(tq.taskMap, taskID)
	delete(tq.runningTaskMap, taskID)
	tq.mu.Unlock()
	// Release the child context from its long-lived parent after hooks/events.
	if task != nil {
		task.Cancel()
	}
}

func (tq *TaskQueue[T]) Length() int {
	tq.mu.RLock()
	defer tq.mu.RUnlock()
	return tq.tasks.Len()
}

// ActiveLength returns the number of non-cancelled tasks in the queue.
func (tq *TaskQueue[T]) ActiveLength() int {
	tq.mu.RLock()
	defer tq.mu.RUnlock()

	count := 0
	for element := tq.tasks.Front(); element != nil; element = element.Next() {
		task := element.Value.(*Task[T])
		if !task.Cancelled() {
			count++
		}
	}
	return count
}

// RunningTasks includes cancelled tasks until Done releases their worker slot.
func (tq *TaskQueue[T]) RunningTasks() []TaskInfo {
	tq.mu.RLock()
	defer tq.mu.RUnlock()

	tasks := make([]TaskInfo, 0, len(tq.runningTaskMap))
	for _, task := range tq.runningTaskMap {
		tasks = append(tasks, TaskInfo{
			ID:        task.ID,
			Title:     task.Title,
			Created:   task.created,
			Cancelled: task.Cancelled(),
		})
	}
	return tasks
}

// QueuedTasks returns the queued (not yet running) tasks' info.
// The sorting is in the order of addition.
func (tq *TaskQueue[T]) QueuedTasks() []TaskInfo {
	tq.mu.RLock()
	defer tq.mu.RUnlock()

	tasks := make([]TaskInfo, 0, tq.tasks.Len())
	for element := tq.tasks.Front(); element != nil; element = element.Next() {
		task := element.Value.(*Task[T])
		if !task.Cancelled() {
			tasks = append(tasks, TaskInfo{
				ID:        task.ID,
				Title:     task.Title,
				Created:   task.created,
				Cancelled: task.Cancelled(),
			})
		}
	}
	return tasks
}

// IsExecuting reports whether taskID is currently checked out for execution
// (present in runningTaskMap), including tasks that have been cancelled but
// have not yet finished and called Done.
func (tq *TaskQueue[T]) IsExecuting(taskID string) bool {
	tq.mu.RLock()
	defer tq.mu.RUnlock()
	_, ok := tq.runningTaskMap[taskID]
	return ok
}

// CancelTask cancels a task by its ID.
// It looks for the task in both queued and running tasks.
// [NOTE] Cancelled tasks will not be removed from the queue, but marked as cancelled. Use Done to remove them.
// [WARN] Cancel invokes the task context's cancel func. Running tasks MUST poll ctx.Err()
// (or select on ctx.Done()) in download/upload loops; otherwise cancellation is delayed until
// the next blocking call returns or the task finishes.
func (tq *TaskQueue[T]) CancelTask(taskID string) error {
	tq.mu.RLock()
	task, exists := tq.taskMap[taskID]
	if !exists {
		task, exists = tq.runningTaskMap[taskID]
	}
	tq.mu.RUnlock()

	if !exists {
		return fmt.Errorf("task %s does not exist", taskID)
	}

	task.Cancel()
	return nil
}

func (tq *TaskQueue[T]) Close() {
	tq.mu.Lock()
	defer tq.mu.Unlock()

	tq.closed = true
	tq.cond.Broadcast()
}

// CloseAndCancel stops submissions and cancels queued and running tasks. Unlike
// Close, it aborts work instead of draining it, and wakes idle workers.
func (tq *TaskQueue[T]) CloseAndCancel() {
	tq.mu.Lock()
	defer tq.mu.Unlock()
	tq.closed = true
	for _, task := range tq.taskMap {
		task.Cancel()
	}
	tq.cond.Broadcast()
}

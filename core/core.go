package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

var queueInstance *queue.TaskQueue[Executable]

var queueMu sync.RWMutex

// Prepare initializes the queue before Bot/UserBot/API can submit tasks. Workers
// still start in Run, after the Telegram client is ready.
func Prepare() {
	queueMu.Lock()
	defer queueMu.Unlock()
	if queueInstance == nil {
		queueInstance = queue.NewTaskQueue[Executable]()
	}
}

func currentQueue() *queue.TaskQueue[Executable] {
	queueMu.RLock()
	defer queueMu.RUnlock()
	return queueInstance
}

type Executable interface {
	Type() tasktype.TaskType
	Title() string
	TaskID() string
	Execute(ctx context.Context) error
}

// TaskDiscarder releases resources when a worker checks out a task but skips
// Execute because it was cancelled before starting. Discard must be idempotent.
type TaskDiscarder interface {
	Discard()
}

func worker(ctx context.Context, qe *queue.TaskQueue[Executable]) {
	logger := log.FromContext(ctx)
	execHooks := config.C().Hook.Exec
	for {
		qtask, err := qe.Get()
		if err != nil {
			logger.Debug("Task worker stopped", "error", err)
			break // queue closed and empty
		}
		exe := qtask.Data
		taskCtx := qtask.Context()
		logger.Infof("Processing task: %s", exe.TaskID())
		taskevent.Emit(taskCtx, taskevent.Event{TaskID: exe.TaskID(), Phase: taskevent.PhaseStart})
		err = taskCtx.Err()
		executed := false
		if err == nil {
			if err := ExecCommandString(taskCtx, execHooks.TaskBeforeStart); err != nil {
				logger.Errorf("Failed to execute before start hook for task %s: %v", exe.TaskID(), err)
			}
			err = taskCtx.Err()
			if err == nil {
				executed = true
				err = exe.Execute(taskCtx)
			}
		}
		if !executed {
			if discardable, ok := exe.(TaskDiscarder); ok {
				discardable.Discard()
			}
		}
		var resultSummary *taskresult.Counts
		if executed {
			if provider, ok := exe.(taskresult.Provider); ok {
				counts := provider.ResultSummary().Counts()
				if counts.Valid() {
					resultSummary = &counts
				}
			}
		}
		// Snapshot before hooks: a late cancellation must not make observers
		// disagree with the hook selected for this execution result.
		outcome := taskresult.ClassifyOutcome(taskCtx, err)
		if err != nil {
			if outcome == taskresult.OutcomeCancelled {
				logger.Infof("Task %s was canceled", exe.TaskID())
				if err := execTerminalHook(ctx, execHooks.TaskCancel); err != nil {
					logger.Errorf("Failed to execute cancel hook for task %s: %v", exe.TaskID(), err)
				}
			} else {
				logger.Errorf("Failed to execute task %s: %v", exe.TaskID(), err)
				if err := execTerminalHook(ctx, execHooks.TaskFail); err != nil {
					logger.Errorf("Failed to execute fail hook for task %s: %v", exe.TaskID(), err)
				}
			}
		} else {
			logger.Infof("Task %s completed successfully", exe.TaskID())
			if err := execTerminalHook(ctx, execHooks.TaskSuccess); err != nil {
				logger.Errorf("Failed to execute success hook for task %s: %v", exe.TaskID(), err)
			}
		}
		taskevent.Emit(taskCtx, taskevent.Event{TaskID: exe.TaskID(), Phase: taskevent.PhaseDone, Err: err, Outcome: outcome, ResultSummary: resultSummary})
		qe.Done(qtask.ID)
	}
}

func execTerminalHook(ctx context.Context, command string) error {
	if ctx.Err() == nil {
		return ExecCommandString(ctx, command)
	}
	// Shutdown must not skip terminal hooks because the service context ended.
	hookCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	return ExecCommandString(hookCtx, command)
}

func Run(ctx context.Context) <-chan struct{} {
	log.FromContext(ctx).Info("Start processing tasks...")
	Prepare()
	qe := currentQueue()
	count := max(1, config.C().Workers)
	var wg sync.WaitGroup
	wg.Add(count)
	for range count {
		go func() { defer wg.Done(); worker(ctx, qe) }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	go func() {
		select {
		case <-ctx.Done():
			qe.CloseAndCancel()
		case <-done:
		}
	}()
	return done
}

// AddTask transfers a new task wrapper to the queue. If admission fails, it
// releases only that wrapper's child context and preserves the caller's context.
func AddTask(ctx context.Context, task Executable) error {
	return AddTasks(ctx, task)
}

// AddTasks transfers all new task wrappers to the queue in one admission.
// Rejection releases every new child context, leaving accepted work and the
// caller's context intact. An empty group is a no-op.
func AddTasks(ctx context.Context, tasks ...Executable) error {
	if len(tasks) == 0 {
		return nil
	}
	qe := currentQueue()
	if qe == nil {
		return errors.New("task queue is not initialized")
	}
	for _, task := range tasks {
		if task == nil {
			return errors.New("cannot add nil task")
		}
	}
	wrappers := make([]*queue.Task[Executable], 0, len(tasks))
	for _, task := range tasks {
		wrappers = append(wrappers, queue.NewTask(ctx, task.TaskID(), task.Title(), task))
	}
	if err := qe.AddBatch(wrappers...); err != nil {
		for _, wrapper := range wrappers {
			wrapper.Cancel()
		}
		return err
	}
	return nil
}

func CancelTask(ctx context.Context, id string) error {
	qe := currentQueue()
	if qe == nil {
		return errors.New("task queue is not initialized")
	}
	return qe.CancelTask(id)
}

// IsTaskExecuting reports whether the task has been checked out by a worker
// (running or cancelled-but-not-yet-Done).
func IsTaskExecuting(id string) bool {
	qe := currentQueue()
	if qe == nil {
		return false
	}
	return qe.IsExecuting(id)
}

func GetLength(ctx context.Context) int {
	if qe := currentQueue(); qe != nil {
		return qe.ActiveLength()
	}
	return 0
}

func GetRunningTasks(ctx context.Context) []queue.TaskInfo {
	if qe := currentQueue(); qe != nil {
		return qe.RunningTasks()
	}
	return nil
}

func GetQueuedTasks(ctx context.Context) []queue.TaskInfo {
	if qe := currentQueue(); qe != nil {
		return qe.QueuedTasks()
	}
	return nil
}

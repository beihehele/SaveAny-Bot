package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"time"

	"github.com/rs/xid"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
	"github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	filemodel "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

// TaskFactory prepares Telegram saves and hands accepted tasks to the service queue.
type TaskFactory struct {
	ctx          context.Context
	prepareCtx   context.Context
	extractFiles TelegramFileExtractor
}

// TelegramFileExtractor resolves Telegram links into downloadable media.
type TelegramFileExtractor func(context.Context, []string) ([]filemodel.TGFileMessage, error)

// CreateTaskWithContext passes the request context to source preparation and
// checks cancellation before enqueue. Accepted tasks keep the service lifetime.
func (f *TaskFactory) CreateTaskWithContext(ctx context.Context, req *CreateTaskRequest) (*CreateTaskResponse, error) {
	requestFactory := *f
	requestFactory.prepareCtx = ctx
	return requestFactory.CreateTask(req)
}

func (f *TaskFactory) preprocessingContext() context.Context {
	if f.prepareCtx != nil {
		return f.prepareCtx
	}
	return f.ctx
}

// NewTaskFactory uses the instance's Telegram clients to resolve source media.
func NewTaskFactory(ctx context.Context) *TaskFactory {
	return NewTaskFactoryWithExtractor(ctx, ExtractFilesFromLinks)
}

// NewTaskFactoryWithExtractor supplies a source adapter while retaining the
// production queue, local writes, progress reporting and cancellation.
func NewTaskFactoryWithExtractor(ctx context.Context, extractor TelegramFileExtractor) *TaskFactory {
	if extractor == nil {
		extractor = ExtractFilesFromLinks
	}
	return &TaskFactory{ctx: ctx, extractFiles: extractor}
}

// CreateTask validates and prepares a Telegram save before handing it to workers.
func (f *TaskFactory) CreateTask(req *CreateTaskRequest) (*CreateTaskResponse, error) {
	if req == nil {
		return nil, errors.New("task request is required")
	}
	if req.Type != tasktype.TaskTypeTgfiles {
		return nil, fmt.Errorf("unsupported task type: %s", req.Type)
	}
	if err := validateResultPolicy(req.ResultPolicy); err != nil {
		return nil, err
	}
	return f.createTask(req)
}

func (f *TaskFactory) createTask(req *CreateTaskRequest) (*CreateTaskResponse, error) {
	stor, ok := storage.GetStorage(req.Storage)
	if !ok {
		return nil, fmt.Errorf("storage not found: %s", req.Storage)
	}
	return f.createTGFilesTask(xid.New().String(), time.Now(), req, stor)
}

func (f *TaskFactory) registerAndEnqueueTask(task core.Executable, taskType tasktype.TaskType, storageName, path, webhook string) error {
	if err := f.preprocessingContext().Err(); err != nil {
		return err
	}
	taskCtx := f.ctx
	taskID := task.TaskID()
	info := RegisterTask(taskID, string(taskType), storageName, path, task.Title(), webhook)
	info.mu.Lock()
	info.webhookContext = f.ctx
	info.mu.Unlock()

	// Inject the progress sink into the context so the task's Emit calls update
	// the API store (and fire the webhook on terminal states) without the task
	// knowing about the API.
	taskCtx = taskevent.WithSink(taskCtx, info)

	err := core.AddTask(taskCtx, task)
	if err != nil {
		DeleteTask(taskID)
		return fmt.Errorf("failed to add task: %w", err)
	}

	return nil
}

func (f *TaskFactory) createTGFilesTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params TGFilesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(params.MessageLinks) == 0 {
		return nil, fmt.Errorf("no message links provided")
	}

	files, err := f.extractFiles(f.preprocessingContext(), params.MessageLinks)
	if err != nil {
		return nil, fmt.Errorf("failed to extract files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found in provided links")
	}
	for _, file := range files {
		if file == nil {
			return nil, errors.New("Telegram source returned an empty file")
		}
	}

	var task core.Executable

	if len(files) == 1 {
		tfileTask, err := tfile.NewTGFileTask(taskID, f.ctx, files[0], stor, path.Join(req.Path, files[0].Name()), nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create tfile task: %w", err)
		}
		task = tfileTask
	} else {
		elems := make([]batchtfile.TaskElement, 0, len(files))
		for _, file := range files {
			elem, err := batchtfile.NewTaskElement(stor, path.Join(req.Path, file.Name()), file)
			if err != nil {
				return nil, fmt.Errorf("failed to create task element: %w", err)
			}
			elems = append(elems, *elem)
		}

		task = batchtfile.NewBatchTGFileTask(taskID, f.ctx, elems, nil)
	}

	err = f.registerAndEnqueueTask(task, tasktype.TaskTypeTgfiles, req.Storage, req.Path, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeTgfiles,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

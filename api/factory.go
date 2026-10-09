package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/rs/xid"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/aria2dl"
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
	"github.com/krau/SaveAny-Bot/core/tasks/directlinks"
	"github.com/krau/SaveAny-Bot/core/tasks/parsed"
	tphtask "github.com/krau/SaveAny-Bot/core/tasks/telegraph"
	"github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/core/tasks/transfer"
	"github.com/krau/SaveAny-Bot/core/tasks/ytdlp"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
	"github.com/krau/SaveAny-Bot/pkg/aria2"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/parser"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/pkg/telegraph"
	"github.com/krau/SaveAny-Bot/storage"
)

// TaskFactory 任务工厂
type TaskFactory struct {
	ctx           context.Context
	prepareCtx    context.Context
	resultPolicy  taskresult.Policy
	clientContext func() (*ext.Context, error)
}

// CreateTaskWithContext limits preprocessing to the request. Enqueued tasks keep
// the service context so disconnecting the HTTP client does not cancel uploads.
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

// NewTaskFactory 创建任务工厂
func NewTaskFactory(ctx context.Context) *TaskFactory {
	return &TaskFactory{ctx: ctx, clientContext: getClientContext}
}

func (f *TaskFactory) contextForStorage(storageName string) (context.Context, error) {
	stor, ok := storage.GetStorage(storageName)
	if !ok || stor.Type() != storenum.Telegram || tgutil.ExtFromContext(f.ctx) != nil {
		return f.ctx, nil
	}
	resolve := f.clientContext
	if resolve == nil {
		resolve = getClientContext
	}
	client, err := resolve()
	if err != nil {
		return nil, fmt.Errorf("telegram storage client is unavailable: %w", err)
	}
	if client == nil {
		return nil, errors.New("telegram storage client is unavailable")
	}
	// Keep the service lifetime and values. The client's own context must not
	// replace the parent of an accepted API task.
	return tgutil.ExtWithContext(f.ctx, client), nil
}

// CreateTask 创建任务
func (f *TaskFactory) CreateTask(req *CreateTaskRequest) (*CreateTaskResponse, error) {
	if req == nil {
		return nil, errors.New("task request is required")
	}
	if err := validateResultPolicy(req.Type, req.ResultPolicy); err != nil {
		return nil, err
	}
	requestFactory := *f
	requestFactory.resultPolicy = req.ResultPolicy
	return requestFactory.createTask(req)
}

func (f *TaskFactory) createTask(req *CreateTaskRequest) (*CreateTaskResponse, error) {
	// 验证存储
	stor, ok := storage.GetStorage(req.Storage)
	if !ok {
		return nil, fmt.Errorf("storage not found: %s", req.Storage)
	}

	taskID := xid.New().String()
	createdAt := time.Now()

	switch req.Type {
	case tasktype.TaskTypeDirectlinks:
		return f.createDirectLinksTask(taskID, createdAt, req, stor)
	case tasktype.TaskTypeYtdlp:
		return f.createYTDLPTask(taskID, createdAt, req, stor)
	case tasktype.TaskTypeAria2:
		return f.createAria2Task(taskID, createdAt, req, stor)
	case tasktype.TaskTypeParseditem:
		return f.createParsedTask(taskID, createdAt, req, stor)
	case tasktype.TaskTypeTgfiles:
		return f.createTGFilesTask(taskID, createdAt, req, stor)
	case tasktype.TaskTypeTphpics:
		return f.createTPHPicsTask(taskID, createdAt, req, stor)
	case tasktype.TaskTypeTransfer:
		return f.createTransferTask(taskID, createdAt, req)
	default:
		return nil, fmt.Errorf("unsupported task type: %s", req.Type)
	}
}

func (f *TaskFactory) registerAndEnqueueTask(task core.Executable, taskType tasktype.TaskType, storageName, path, webhook string) error {
	if err := f.preprocessingContext().Err(); err != nil {
		return err
	}
	taskCtx, err := f.contextForStorage(storageName)
	if err != nil {
		return err
	}
	taskID := task.TaskID()
	info := RegisterTask(taskID, string(taskType), storageName, path, task.Title(), webhook)
	info.mu.Lock()
	info.webhookContext = f.ctx
	info.ResultPolicy = f.resultPolicy
	info.mu.Unlock()

	// Inject the progress sink into the context so the task's Emit calls update
	// the API store (and fire the webhook on terminal states) without the task
	// knowing about the API.
	taskCtx = taskevent.WithSink(taskCtx, info)
	if f.resultPolicy != "" {
		taskCtx = taskresult.WithPolicy(taskCtx, f.resultPolicy)
	}

	err = core.AddTask(taskCtx, task)
	if err != nil {
		DeleteTask(taskID)
		return fmt.Errorf("failed to add task: %w", err)
	}

	return nil
}

// createDirectLinksTask 创建直链下载任务
func (f *TaskFactory) createDirectLinksTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params DirectLinksParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(params.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	task := directlinks.NewTask(taskID, f.ctx, params.URLs, stor, req.Path, nil)

	err := f.registerAndEnqueueTask(task, tasktype.TaskTypeDirectlinks, req.Storage, req.Path, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeDirectlinks,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

// createYTDLPTask 创建 yt-dlp 任务
func (f *TaskFactory) createYTDLPTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params YTDLPParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(params.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	task := ytdlp.NewTask(taskID, f.ctx, params.URLs, params.Flags, stor, req.Path, nil)

	err := f.registerAndEnqueueTask(task, tasktype.TaskTypeYtdlp, req.Storage, req.Path, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeYtdlp,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

// createAria2Task 创建 Aria2 任务
func (f *TaskFactory) createAria2Task(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params Aria2Params
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(params.URLs) == 0 {
		return nil, fmt.Errorf("no URLs provided")
	}

	// 检查 Aria2 是否启用
	cfg := config.C().Aria2
	if !cfg.Enable {
		return nil, fmt.Errorf("aria2 is not enabled")
	}

	aria2Client, err := aria2.NewClient(cfg.Url, cfg.Secret)
	if err != nil {
		return nil, fmt.Errorf("failed to create aria2 client: %w", err)
	}

	// 添加下载任务到 Aria2
	gid, err := aria2Client.AddURI(f.preprocessingContext(), params.URLs, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to add aria2 task: %w", err)
	}

	task := aria2dl.NewTask(taskID, f.ctx, gid, params.URLs, aria2Client, stor, req.Path, nil)

	err = f.registerAndEnqueueTask(task, tasktype.TaskTypeAria2, req.Storage, req.Path, req.Webhook)
	if err != nil {
		// The queue did not accept ownership of the already-created download.
		if cleanupErr := cleanupUnqueuedAria2Download(f.ctx, aria2Client, gid); cleanupErr != nil {
			// The HTTP client may already be gone, so retain recovery details in logs.
			log.FromContext(f.ctx).Error("Failed to clean up unqueued aria2 download", "task_id", taskID, "gid", gid, "error", cleanupErr)
			return nil, errors.Join(err, fmt.Errorf("failed to clean up unqueued aria2 download %s: %w", gid, cleanupErr))
		}
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeAria2,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

// createParsedTask 创建解析任务
func (f *TaskFactory) createParsedTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params ParsedParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if params.URL == "" {
		return nil, fmt.Errorf("no URL provided")
	}

	// 查找合适的解析器
	var p parser.Parser
	for _, parserItem := range parsers.Get() {
		if parser.CanHandleWithContext(f.preprocessingContext(), parserItem, params.URL) {
			p = parserItem
			break
		}
	}

	if p == nil {
		if err := f.preprocessingContext().Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("no parser found for URL: %s", params.URL)
	}

	// 解析 URL
	item, err := p.Parse(f.preprocessingContext(), params.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URL: %w", err)
	}

	task := parsed.NewTask(taskID, f.ctx, stor, req.Path, item, nil)

	err = f.registerAndEnqueueTask(task, tasktype.TaskTypeParseditem, req.Storage, req.Path, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeParseditem,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

// createTGFilesTask 创建 Telegram 文件下载任务
func (f *TaskFactory) createTGFilesTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params TGFilesParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if len(params.MessageLinks) == 0 {
		return nil, fmt.Errorf("no message links provided")
	}

	// 提取文件
	files, err := ExtractFilesFromLinks(f.preprocessingContext(), params.MessageLinks)
	if err != nil {
		return nil, fmt.Errorf("failed to extract files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found in provided links")
	}

	var task core.Executable

	if len(files) == 1 {
		// 单个文件任务
		tfileTask, err := tfile.NewTGFileTask(taskID, f.ctx, files[0], stor, req.Path, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create tfile task: %w", err)
		}
		task = tfileTask
	} else {
		// 批量文件任务
		elems := make([]batchtfile.TaskElement, 0, len(files))
		for _, file := range files {
			elem, err := batchtfile.NewTaskElement(stor, req.Path, file)
			if err != nil {
				return nil, fmt.Errorf("failed to create task element: %w", err)
			}
			elems = append(elems, *elem)
		}

		task = batchtfile.NewBatchTGFileTask(taskID, f.ctx, elems, nil, true)
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

// createTPHPicsTask 创建 Telegraph 图片下载任务
func (f *TaskFactory) createTPHPicsTask(taskID string, createdAt time.Time, req *CreateTaskRequest, stor storage.Storage) (*CreateTaskResponse, error) {
	var params TPHPicsParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	if params.TelegraphURL == "" {
		return nil, fmt.Errorf("no telegraph URL provided")
	}

	// 提取图片
	pics, phPath, err := ExtractTelegraphImages(f.preprocessingContext(), params.TelegraphURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract telegraph images: %w", err)
	}

	if len(pics) == 0 {
		return nil, fmt.Errorf("no images found in telegraph page")
	}

	client := telegraph.NewClient()
	task := tphtask.NewTask(taskID, f.ctx, phPath, pics, stor, req.Path, client, nil)

	err = f.registerAndEnqueueTask(task, tasktype.TaskTypeTphpics, req.Storage, req.Path, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:    taskID,
		Type:      tasktype.TaskTypeTphpics,
		Status:    TaskStatusQueued,
		CreatedAt: createdAt,
	}, nil
}

// createTransferTask 创建存储间传输任务
func (f *TaskFactory) createTransferTask(taskID string, createdAt time.Time, req *CreateTaskRequest) (*CreateTaskResponse, error) {
	var params TransferParams
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return nil, fmt.Errorf("invalid params: %w", err)
	}

	// 验证源存储和目标存储
	sourceStor, ok := storage.GetStorage(params.SourceStorage)
	if !ok {
		return nil, fmt.Errorf("source storage not found: %s", params.SourceStorage)
	}

	targetStor, ok := storage.GetStorage(params.TargetStorage)
	if !ok {
		return nil, fmt.Errorf("target storage not found: %s", params.TargetStorage)
	}

	// 检查源存储是否可读
	sourceReadable, ok := sourceStor.(storage.StorageReadable)
	if !ok {
		return nil, fmt.Errorf("source storage does not support reading: %s", params.SourceStorage)
	}

	// 检查源存储是否可列
	sourceListable, ok := sourceStor.(storage.StorageListable)
	if !ok {
		return nil, fmt.Errorf("source storage does not support listing: %s", params.SourceStorage)
	}

	// 列出源文件
	files, err := sourceListable.ListFiles(f.preprocessingContext(), params.SourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list source files: %w", err)
	}

	if len(files) == 0 {
		return nil, fmt.Errorf("no files found at source path: %s", params.SourcePath)
	}

	// 创建传输元素
	elems := make([]transfer.TaskElement, 0, len(files))
	for _, file := range files {
		elem := transfer.NewTaskElement(sourceReadable, file, targetStor, params.TargetPath)
		elems = append(elems, *elem)
	}

	task := transfer.NewTransferTask(taskID, f.ctx, elems, nil, true)

	err = f.registerAndEnqueueTask(task, tasktype.TaskTypeTransfer, params.TargetStorage, params.TargetPath, req.Webhook)
	if err != nil {
		return nil, err
	}

	return &CreateTaskResponse{
		TaskID:       taskID,
		Type:         tasktype.TaskTypeTransfer,
		Status:       TaskStatusQueued,
		CreatedAt:    createdAt,
		ResultPolicy: f.resultPolicy,
	}, nil
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage"
	ytdlp "github.com/lrstanley/go-ytdlp"
)

// Handlers 处理器结构体
type Handlers struct {
	factory *TaskFactory
}

// NewHandlers 创建处理器
func NewHandlers(factory *TaskFactory) *Handlers {
	return &Handlers{factory: factory}
}

// CreateTaskHandler 创建任务处理器
func (h *Handlers) CreateTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only POST method is allowed")
		return
	}

	var req CreateTaskRequest
	const maxTaskRequestBytes = 1 << 20
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxTaskRequestBytes))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			WriteError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body exceeds 1 MiB")
		} else {
			WriteError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
		}
		return
	}
	if err := json.Unmarshal(body, &req); err != nil {
		WriteError(w, http.StatusBadRequest, "invalid_request", "failed to decode request body: "+err.Error())
		return
	}

	// 验证请求
	if req.Type == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "task type is required")
		return
	}

	if req.Storage == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "storage is required")
		return
	}

	// 创建任务
	prepareCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	resp, err := h.factory.CreateTaskWithContext(prepareCtx, &req)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "task_creation_failed", err.Error())
		return
	}

	WriteJSON(w, http.StatusCreated, resp)
}

// ListTasksHandler 列出任务处理器
func (h *Handlers) ListTasksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET method is allowed")
		return
	}

	tasks := GetAllTasks()
	response := make([]TaskInfoResponse, 0, len(tasks))

	for _, task := range tasks {
		info := convertTaskProgressToResponse(task)
		response = append(response, info)
	}

	// Map iteration is unordered. Task IDs keep timestamp ties deterministic.
	sort.Slice(response, func(i, j int) bool {
		if !response[i].CreatedAt.Equal(response[j].CreatedAt) {
			return response[i].CreatedAt.After(response[j].CreatedAt)
		}
		return response[i].TaskID > response[j].TaskID
	})

	WriteJSON(w, http.StatusOK, TasksListResponse{
		Tasks: response,
		Total: len(response),
	})
}

// GetTaskHandler 获取单个任务处理器
func (h *Handlers) GetTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET method is allowed")
		return
	}

	taskID := extractTaskIDFromPath(r.URL.Path)
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "task ID is required")
		return
	}

	task, ok := GetTask(taskID)
	if !ok {
		WriteError(w, http.StatusNotFound, "task_not_found", "task not found: "+taskID)
		return
	}

	resp := convertTaskProgressToResponse(task)
	WriteJSON(w, http.StatusOK, resp)
}

// CancelTaskHandler 取消任务处理器
func (h *Handlers) CancelTaskHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only DELETE method is allowed")
		return
	}

	taskID := extractTaskIDFromPath(r.URL.Path)
	if taskID == "" {
		WriteError(w, http.StatusBadRequest, "invalid_request", "task ID is required")
		return
	}

	task, ok := GetTask(taskID)
	if !ok {
		WriteError(w, http.StatusNotFound, "task_not_found", "task not found: "+taskID)
		return
	}

	// Cancel the task; the terminal status is set via the task event stream.
	if err := core.CancelTask(r.Context(), taskID); err != nil {
		WriteError(w, http.StatusInternalServerError, "cancel_failed", "failed to cancel task: "+err.Error())
		return
	}

	task.UpdateStatus(TaskStatusCancelled)
	WriteJSON(w, http.StatusOK, map[string]string{"message": "task cancelled successfully"})
}

// ListStoragesHandler 列出存储处理器
func (h *Handlers) ListStoragesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET method is allowed")
		return
	}

	available := storage.AllStorages()
	storages := make([]StorageInfo, 0, len(available))
	for name, stor := range available {
		_, readable := stor.(storage.StorageReadable)
		_, listable := stor.(storage.StorageListable)
		_, cannotStream := stor.(storage.StorageCannotStream)
		storages = append(storages, StorageInfo{
			Name:            name,
			Type:            string(stor.Type()),
			Readable:        readable,
			Listable:        listable,
			Stream:          !cannotStream,
			DetectExistence: storage.CanDetectExistence(stor),
		})
	}
	sort.Slice(storages, func(i, j int) bool { return storages[i].Name < storages[j].Name })

	WriteJSON(w, http.StatusOK, StoragesResponse{Storages: storages})
}

// GetTaskTypesHandler 获取支持的任务类型
func (h *Handlers) GetTaskTypesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "only GET method is allowed")
		return
	}

	types := []tasktype.TaskType{
		tasktype.TaskTypeDirectlinks,
		tasktype.TaskTypeYtdlp,
		tasktype.TaskTypeAria2,
		tasktype.TaskTypeParseditem,
		tasktype.TaskTypeTgfiles,
		tasktype.TaskTypeTphpics,
		tasktype.TaskTypeTransfer,
	}

	WriteJSON(w, http.StatusOK, map[string]any{
		"types":        types,
		"capabilities": taskCapabilities(r.Context(), types),
	})
}

func taskCapabilities(ctx context.Context, types []tasktype.TaskType) []TaskCapability {
	capabilities := make([]TaskCapability, 0, len(types))
	for _, typ := range types {
		capability := TaskCapability{Type: typ, Available: true}
		if typ == tasktype.TaskTypeTransfer {
			capability.ResultPolicies = []taskresult.Policy{taskresult.Legacy, taskresult.Strict}
		}
		switch typ {
		case tasktype.TaskTypeAria2:
			if !config.C().Aria2.Enable || strings.TrimSpace(config.C().Aria2.Url) == "" {
				capability.Available, capability.Reason = false, "aria2 is not configured"
			}
		case tasktype.TaskTypeYtdlp:
			// Use the pinned wrapper's resolution rules, including its cache. This
			// only builds a command; it never executes or downloads a binary.
			if cmd := ytdlp.New().BuildCommand(ctx); cmd.Err != nil {
				capability.Available, capability.Reason = false, "yt-dlp executable is unavailable"
			}
		case tasktype.TaskTypeParseditem:
			if len(parsers.Get()) == 0 {
				capability.Available, capability.Reason = false, "no parsers are registered"
			}
		case tasktype.TaskTypeTgfiles:
			if _, err := getClientContext(); err != nil {
				capability.Available, capability.Reason = false, "Telegram client is not initialized"
			}
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

// HealthCheckHandler 健康检查处理器
func (h *Handlers) HealthCheckHandler(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// extractTaskIDFromPath 从路径中提取任务 ID
// 路径格式: /api/v1/tasks/:id
func extractTaskIDFromPath(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 4 {
		return ""
	}
	return parts[3]
}

// convertTaskProgressToResponse renders a task's current state, computing
// percent and speed from the snapshot taken under the task's mutex.
func convertTaskProgressToResponse(task *TaskProgressInfo) TaskInfoResponse {
	s := task.responseSnapshot()
	status, total, downloaded, totalFiles, downloadedFiles, startedAt, errMsg, updatedAt := s.status, s.total, s.downloaded, s.totalFiles, s.downloadedFiles, s.startedAt, s.err, s.updatedAt

	resp := TaskInfoResponse{
		TaskID:        task.TaskID,
		Type:          tasktype.TaskType(task.Type),
		Status:        status,
		Title:         task.Title,
		Storage:       task.Storage,
		Path:          task.Path,
		Error:         errMsg,
		CreatedAt:     task.CreatedAt,
		UpdatedAt:     updatedAt,
		ResultPolicy:  s.resultPolicy,
		ResultSummary: s.resultSummary,
	}

	var percent float64
	var speedMBPS float64
	if total > 0 {
		percent = float64(downloaded) * 100 / float64(total)
	} else if totalFiles > 0 {
		percent = float64(downloadedFiles) * 100 / float64(totalFiles)
	}
	if !startedAt.IsZero() {
		elapsed := time.Since(startedAt).Seconds()
		if elapsed > 0 {
			speedMBPS = float64(downloaded) / elapsed / (1024 * 1024)
		}
	}

	if total > 0 || totalFiles > 0 {
		resp.Progress = &TaskProgress{
			TotalBytes:      total,
			DownloadedBytes: downloaded,
			TotalFiles:      totalFiles,
			DownloadedFiles: downloadedFiles,
			Percent:         percent,
			SpeedMBPS:       speedMBPS,
		}
	}

	return resp
}

// NotFoundHandler 404 处理器
func NotFoundHandler(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusNotFound, "not_found", "endpoint not found: "+r.URL.Path)
}

// MethodNotAllowedHandler 405 处理器
func MethodNotAllowedHandler(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed: "+r.Method)
}

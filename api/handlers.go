package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/core/taskcontrol"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/inputresult"
	"github.com/krau/SaveAny-Bot/storage"
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
	if err := decodeTaskJSON(body, &req); err != nil {
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
		var selection *inputresult.Error
		if errors.As(err, &selection) {
			WriteJSON(w, http.StatusBadRequest, ErrorResponse{Error: "task_creation_failed", Message: err.Error(), Report: selection.Report.Clone()})
		} else {
			WriteError(w, http.StatusBadRequest, "task_creation_failed", err.Error())
		}
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
	uploads, err := uploadResponses(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "storage_failed", "failed to read upload tasks")
		return
	}
	response = mergeUploadResponses(response, uploads)

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

	if response, found, err := uploadTask(r.Context(), taskID); err != nil {
		WriteError(w, 500, "storage_failed", "failed to read upload task")
		return
	} else if found {
		WriteJSON(w, 200, response)
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

	if _, found, err := uploadTask(r.Context(), taskID); err != nil {
		WriteError(w, 500, "storage_failed", "failed to read upload task")
		return
	} else if found {
		if err := taskcontrol.CancelTask(r.Context(), taskID); err != nil {
			WriteError(w, 409, "cancel_failed", err.Error())
			return
		}
		WriteJSON(w, 200, map[string]string{"message": "cancellation requested"})
		return
	}
	task, ok := GetTask(taskID)
	if !ok {
		WriteError(w, http.StatusNotFound, "task_not_found", "task not found: "+taskID)
		return
	}

	// Cancel the task; the terminal status is set via the task event stream.
	if err := taskcontrol.CancelTask(r.Context(), taskID); err != nil {
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
		var chatID int64
		var topicID int
		if cfg, ok := config.C().GetStorageByName(name).(*storcfg.TelegramStorageConfig); ok {
			chatID, topicID = cfg.ChatID, cfg.TopicID
		}
		storages = append(storages, StorageInfo{ChatID: chatID, TopicID: topicID,
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
	types := []tasktype.TaskType{tasktype.TaskTypeTgfiles}
	WriteJSON(w, http.StatusOK, map[string]any{"types": types, "capabilities": taskCapabilities(r.Context(), types)})
}

func taskCapabilities(ctx context.Context, types []tasktype.TaskType) []TaskCapability {
	capabilities := make([]TaskCapability, 0, len(types))
	for _, typ := range types {
		capability := TaskCapability{Type: typ, Available: true}
		if _, err := getClientContext(); err != nil {
			capability.Available, capability.Reason = false, "Telegram client is not initialized"
		}
		capabilities = append(capabilities, capability)
	}
	return capabilities
}

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

	resp := TaskInfoResponse{DeliveryProgress: s.deliveryProgress, ResultLinks: s.resultLinks,
		TaskID:        task.TaskID,
		Type:          tasktype.TaskType(task.Type),
		Status:        status,
		Title:         task.Title,
		Storage:       task.Storage,
		Path:          task.Path,
		Error:         errMsg,
		CreatedAt:     task.CreatedAt,
		UpdatedAt:     updatedAt,
		ResultSummary: s.resultSummary,
		Report:        s.report,
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

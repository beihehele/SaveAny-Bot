package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

// TaskStatus 表示任务状态
type TaskStatus string

const (
	TaskStatusQueued    TaskStatus = "queued"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusCompleted TaskStatus = "completed"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusCancelled TaskStatus = "cancelled"
)

// CreateTaskRequest 创建任务请求
type CreateTaskRequest struct {
	Type         tasktype.TaskType `json:"type"`
	Storage      string            `json:"storage"`
	Path         string            `json:"path"`
	Webhook      string            `json:"webhook,omitempty"`
	Params       json.RawMessage   `json:"params"`
	ResultPolicy taskresult.Policy `json:"result_policy,omitempty"`
}

// CreateTaskResponse 创建任务响应
type CreateTaskResponse struct {
	TaskID       string            `json:"task_id"`
	Type         tasktype.TaskType `json:"type"`
	Status       TaskStatus        `json:"status"`
	CreatedAt    time.Time         `json:"created_at"`
	ResultPolicy taskresult.Policy `json:"result_policy,omitempty"`
}

// TaskProgress 任务进度
type TaskProgress struct {
	TotalBytes      int64   `json:"total_bytes,omitempty"`
	DownloadedBytes int64   `json:"downloaded_bytes,omitempty"`
	TotalFiles      int     `json:"total_files,omitempty"`
	DownloadedFiles int     `json:"downloaded_files,omitempty"`
	Percent         float64 `json:"percent,omitempty"`
	SpeedMBPS       float64 `json:"speed_mbps,omitempty"`
}

// TaskInfoResponse 任务信息响应
type TaskInfoResponse struct {
	TaskID        string             `json:"task_id"`
	Type          tasktype.TaskType  `json:"type"`
	Status        TaskStatus         `json:"status"`
	Title         string             `json:"title"`
	Progress      *TaskProgress      `json:"progress,omitempty"`
	Storage       string             `json:"storage"`
	Path          string             `json:"path"`
	Error         string             `json:"error,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
	ResultPolicy  taskresult.Policy  `json:"result_policy,omitempty"`
	ResultSummary *taskresult.Counts `json:"result_summary,omitempty"`
}

// TasksListResponse 任务列表响应
type TasksListResponse struct {
	Tasks []TaskInfoResponse `json:"tasks"`
	Total int                `json:"total"`
}

// StoragesResponse 存储列表响应
type StoragesResponse struct {
	Storages []StorageInfo `json:"storages"`
}

// StorageInfo 存储信息
type StorageInfo struct {
	Name            string `json:"name"`
	Type            string `json:"type"`
	Readable        bool   `json:"readable"`
	Listable        bool   `json:"listable"`
	Stream          bool   `json:"stream"`
	DetectExistence bool   `json:"detect_existence"`
}

// TaskCapability describes local prerequisites, without probing external services.
type TaskCapability struct {
	Type           tasktype.TaskType   `json:"type"`
	Available      bool                `json:"available"`
	Reason         string              `json:"reason,omitempty"`
	ResultPolicies []taskresult.Policy `json:"result_policies,omitempty"`
}

// WebhookPayload Webhook 回调负载
type WebhookPayload struct {
	TaskID        string             `json:"task_id"`
	Type          string             `json:"type"`
	Status        TaskStatus         `json:"status"`
	Storage       string             `json:"storage"`
	Path          string             `json:"path"`
	CompletedAt   *time.Time         `json:"completed_at,omitempty"`
	Error         string             `json:"error,omitempty"`
	ResultPolicy  taskresult.Policy  `json:"result_policy,omitempty"`
	ResultSummary *taskresult.Counts `json:"result_summary,omitempty"`
}

// ErrorResponse 错误响应
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

// APIError API 错误
type APIError struct {
	StatusCode int
	ErrorCode  string
	Message    string
}

func (e *APIError) Error() string {
	return e.Message
}

// WriteJSON 写入 JSON 响应
func WriteJSON(w http.ResponseWriter, statusCode int, data any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(data)
}

// WriteError 写入错误响应
func WriteError(w http.ResponseWriter, statusCode int, errCode, message string) error {
	return WriteJSON(w, statusCode, ErrorResponse{
		Error:   errCode,
		Message: message,
	})
}

// TGFilesParams contains Telegram message links to save locally.
type TGFilesParams struct {
	MessageLinks []string `json:"message_links"`
}

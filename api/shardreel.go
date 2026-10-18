package api

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

// UploadResponse projects a durable task into the existing task representation.
func UploadResponse(ctx context.Context, group database.ShardReelTask) (TaskInfoResponse, error) {
	db := database.ShardReelDB(ctx)
	var files []database.ShardReelFile
	if err := db.Where("task_id = ?", group.ID).Order("created_at, id").Find(&files).Error; err != nil {
		return TaskInfoResponse{}, err
	}
	return uploadResponse(group, files), nil
}

func uploadResponse(group database.ShardReelTask, files []database.ShardReelFile) TaskInfoResponse {
	summary := shardreel.Summary(group, files)
	counts := summary.Counts()
	stores := []string{}
	for _, file := range files {
		if !slices.Contains(stores, file.Storage) {
			stores = append(stores, file.Storage)
		}
	}
	response := TaskInfoResponse{TaskID: group.ID, Type: tasktype.TaskTypeTgfiles, Status: TaskStatus(group.Status), Title: group.Title, Source: group.Source,
		Storage: strings.Join(stores, ", "), Error: group.Error, CreatedAt: group.CreatedAt, UpdatedAt: group.UpdatedAt,
		ResultSummary: &counts, UploadFiles: files, Downloading: !group.DownloadDone && group.Status == "running" && !group.CancelRequested}
	if current, ok := GetTask(group.ID); ok {
		live := convertTaskProgressToResponse(current)
		response.Report = live.Report
		if response.Downloading {
			response.Progress = live.Progress
		}
	}
	if len(files) == 1 {
		response.Path = files[0].Path
		if files[0].SavedPath != "" {
			response.Path = files[0].SavedPath
		}
	}
	for _, file := range files {
		if (file.Status == "failed" || file.Status == "retry_wait") && file.SHA256 != "" && !file.LocalClean && !group.CancelRequested {
			response.CanRetry = true
		}
	}
	return response
}

func uploadTask(ctx context.Context, id string) (TaskInfoResponse, bool, error) {
	db := database.ShardReelDB(ctx)
	if db == nil || !db.Migrator().HasTable(&database.ShardReelTask{}) {
		return TaskInfoResponse{}, false, nil
	}
	var group database.ShardReelTask
	err := db.First(&group, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return TaskInfoResponse{}, false, nil
	}
	if err != nil {
		return TaskInfoResponse{}, false, err
	}
	response, err := UploadResponse(ctx, group)
	return response, true, err
}

func uploadResponses(ctx context.Context) ([]TaskInfoResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	db := database.ShardReelDB(ctx)
	if db == nil || !db.Migrator().HasTable(&database.ShardReelTask{}) {
		return nil, nil
	}
	var groups []database.ShardReelTask
	if err := db.Find(&groups).Error; err != nil {
		return nil, err
	}
	result := make([]TaskInfoResponse, 0, len(groups))
	if len(groups) == 0 {
		return result, nil
	}
	var files []database.ShardReelFile
	if err := db.Order("created_at, id").Find(&files).Error; err != nil {
		return nil, err
	}
	byTask := make(map[string][]database.ShardReelFile, len(groups))
	for _, file := range files {
		byTask[file.TaskID] = append(byTask[file.TaskID], file)
	}
	for _, group := range groups {
		result = append(result, uploadResponse(group, byTask[group.ID]))
	}
	return result, nil
}

func mergeUploadResponses(tasks, uploads []TaskInfoResponse) []TaskInfoResponse {
	positions := make(map[string]int, len(tasks))
	for index, task := range tasks {
		positions[task.TaskID] = index
	}
	for _, upload := range uploads {
		if index, exists := positions[upload.TaskID]; exists {
			tasks[index] = upload
		} else {
			positions[upload.TaskID] = len(tasks)
			tasks = append(tasks, upload)
		}
	}
	return tasks
}

// ResetUploadObserver starts a deliberate manual retry of an earlier failed task.
func ResetUploadObserver(id string) {
	if task, ok := GetTask(id); ok {
		task.mu.Lock()
		task.Status, task.Error, task.webhookNotified = TaskStatusRunning, "", false
		task.mu.Unlock()
	}
}

// CompleteUpload delivers the actual terminal result to an existing API observer.
func CompleteUpload(group database.ShardReelTask) {
	var summary taskresult.Summary
	if err := json.Unmarshal([]byte(group.Summary), &summary); err != nil {
		return
	}
	counts := summary.Counts()
	if task, ok := GetTask(group.ID); ok {
		outcome := taskresult.OutcomeSuccess
		var resultErr error
		if group.Status == "failed" {
			outcome = taskresult.OutcomeFailed
			resultErr = errors.New(group.Error)
		}
		if group.Status == "cancelled" {
			outcome = taskresult.OutcomeCancelled
			resultErr = context.Canceled
		}
		task.Emit(taskevent.Event{TaskID: group.ID, Phase: taskevent.PhaseDone, Outcome: outcome, Err: resultErr, ResultSummary: &counts})
	}
}

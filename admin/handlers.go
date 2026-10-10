package admin

import (
	"context"
	"errors"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/storage"
)

type taskRow struct {
	api.TaskInfoResponse
	Scope     string `json:"scope"`
	Executing bool   `json:"executing"`
	CanCancel bool   `json:"can_cancel"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	cfg := config.C()
	s.write(w, http.StatusOK, map[string]any{
		"version": config.Version, "commit": config.GitCommit, "build_time": config.BuildTime,
		"uptime_seconds": int(time.Since(s.started).Seconds()), "workers": cfg.Workers,
		"queued": len(core.GetQueuedTasks(r.Context())), "running": len(core.GetRunningTasks(r.Context())),
		"storages": len(storage.AllStorages()), "go_version": runtime.Version(),
		"platform": runtime.GOOS + "/" + runtime.GOARCH,
	})
}

func (s *Server) taskRows(ctx context.Context) []taskRow {
	byID := make(map[string]taskRow)
	for _, info := range api.TaskSnapshots() {
		info.Title, info.Error = s.redact(info.Title), s.redact(info.Error)
		byID[info.TaskID] = taskRow{TaskInfoResponse: info, Scope: "tracked"}
	}
	add := func(info queue.TaskInfo, running bool) {
		row, exists := byID[info.ID]
		if !exists {
			row = taskRow{TaskInfoResponse: api.TaskInfoResponse{
				TaskID: info.ID, Title: s.redact(info.Title), CreatedAt: info.Created,
			}, Scope: "queue"}
		}
		row.Executing, row.CanCancel = running, !info.Cancelled
		if running {
			row.Status = api.TaskStatusRunning
		} else {
			row.Status = api.TaskStatusQueued
		}
		if info.Cancelled {
			row.Status = "cancelling"
		}
		byID[info.ID] = row
	}
	for _, info := range core.GetQueuedTasks(ctx) {
		add(info, false)
	}
	for _, info := range core.GetRunningTasks(ctx) {
		add(info, true)
	}
	rows := make([]taskRow, 0, len(byID))
	for _, row := range byID {
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		}
		return rows[i].TaskID > rows[j].TaskID
	})
	return rows
}

func page(r *http.Request, total int) (int, int, error) {
	offset, limit := 0, 100
	var err error
	if value := r.URL.Query().Get("offset"); value != "" {
		offset, err = strconv.Atoi(value)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("invalid offset")
		}
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 200 {
			return 0, 0, errors.New("invalid limit")
		}
	}
	if offset >= total {
		offset = 0
		if total > 0 {
			offset = ((total - 1) / limit) * limit
		}
	}
	end := offset + min(limit, total-offset)
	return offset, end, nil
}

func (s *Server) tasks(w http.ResponseWriter, r *http.Request) {
	rows := s.taskRows(r.Context())
	filtered := make([]taskRow, 0, len(rows))
	query := strings.ToLower(r.URL.Query().Get("q"))
	status := r.URL.Query().Get("status")
	for _, row := range rows {
		if status != "" && string(row.Status) != status {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(row.Title+" "+row.TaskID+" "+row.Storage), query) {
			continue
		}
		filtered = append(filtered, row)
	}
	start, end, err := page(r, len(filtered))
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.write(w, http.StatusOK, map[string]any{"tasks": filtered[start:end], "total": len(filtered), "offset": start})
}

func (s *Server) createTask(w http.ResponseWriter, r *http.Request) {
	var request api.CreateTaskRequest
	if err := decodeBody(w, r, &request, 1<<20); err != nil {
		var large *http.MaxBytesError
		if errors.As(err, &large) {
			s.fail(w, http.StatusRequestEntityTooLarge, "request_too_large")
		} else {
			s.fail(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	if request.Type == "" || request.Storage == "" || request.Webhook != "" {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	prepareCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	response, err := s.factory.CreateTaskWithContext(prepareCtx, &request)
	if err != nil {
		log.FromContext(r.Context()).Warn("Admin task preparation failed", "error", s.redact(err.Error()))
		s.audit.add("create_task", s.redact(string(request.Type)), "failed")
		s.fail(w, http.StatusBadRequest, "task_creation_failed")
		return
	}
	s.audit.add("create_task", response.TaskID, "ok")
	s.write(w, http.StatusCreated, response)
}

func (s *Server) cancelTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := core.CancelTask(r.Context(), id); err != nil {
		s.fail(w, http.StatusNotFound, "task_not_active")
		return
	}
	if info, ok := api.GetTask(id); ok {
		info.UpdateStatus(api.TaskStatusCancelled)
	}
	status := "cancelled"
	if core.IsTaskExecuting(id) {
		status = "cancelling"
	}
	s.audit.add("cancel_task", id, "ok")
	s.write(w, http.StatusOK, map[string]string{"status": status})
}

type userPreferences struct {
	ChatID           int64           `json:"chat_id"`
	Silent           bool            `json:"silent"`
	DefaultStorage   string          `json:"default_storage"`
	DefaultDir       uint            `json:"default_dir"`
	ApplyRule        bool            `json:"apply_rule"`
	FilenameStrategy string          `json:"filename_strategy"`
	FilenameTemplate string          `json:"filename_template"`
	ConflictStrategy string          `json:"conflict_strategy"`
	Dirs             []directoryView `json:"directories"`
	Rules            []ruleView      `json:"rules"`
	Routes           []routeView     `json:"routes"`
}

type directoryView struct {
	ID      uint   `json:"id"`
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

type ruleView struct {
	ID      uint   `json:"id"`
	Type    string `json:"type"`
	Data    string `json:"data"`
	Storage string `json:"storage"`
	Path    string `json:"path"`
}

type routeView struct {
	ID       uint   `json:"id"`
	SourceID int64  `json:"source_id"`
	Source   string `json:"source"`
	TargetID int64  `json:"target_id"`
	Target   string `json:"target"`
	TopicID  int    `json:"topic_id"`
	Topic    string `json:"topic"`
	Filter   string `json:"filter"`
}

func (s *Server) preferences(w http.ResponseWriter, r *http.Request) {
	users, err := database.GetAllUsers(r.Context())
	if err != nil {
		s.fail(w, http.StatusServiceUnavailable, "database_unavailable")
		return
	}
	result := make([]userPreferences, 0, len(users))
	for _, user := range users {
		view := userPreferences{ChatID: user.ChatID, Silent: user.Silent, DefaultStorage: user.DefaultStorage,
			DefaultDir: user.DefaultDir, ApplyRule: user.ApplyRule, FilenameStrategy: user.FilenameStrategy,
			FilenameTemplate: s.redact(user.FilenameTemplate), ConflictStrategy: user.ConflictStrategy,
			Dirs: []directoryView{}, Rules: []ruleView{}, Routes: []routeView{}}
		for _, dir := range user.Dirs {
			view.Dirs = append(view.Dirs, directoryView{dir.ID, dir.StorageName, dir.Path})
		}
		for _, rule := range user.Rules {
			view.Rules = append(view.Rules, ruleView{rule.ID, rule.Type, s.redact(rule.Data), rule.StorageName, rule.DirPath})
		}
		for _, route := range user.WatchChats {
			view.Routes = append(view.Routes, routeView{route.ID, route.ChatID, route.SourceName, route.TargetID,
				route.TargetName, route.TargetTopicID, route.TargetTopicName, s.redact(route.Filter)})
		}
		result = append(result, view)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ChatID < result[j].ChatID })
	s.write(w, http.StatusOK, map[string]any{"users": result})
}

func (s *Server) diagnostics(w http.ResponseWriter, r *http.Request) {
	cfg := config.C()
	// A whitelist avoids accidentally exposing new secret-bearing config fields.
	s.write(w, http.StatusOK, map[string]any{
		"settings": map[string]any{
			"workers": cfg.Workers, "retry": cfg.Retry, "threads": cfg.Threads, "stream": cfg.Stream,
			"log_level": cfg.Log.Level, "userbot_enabled": cfg.Telegram.Userbot.Enable,
			"api_enabled":   cfg.API.Enable,
			"secure_cookie": s.cfg.SecureCookie, "session_ttl": s.cfg.SessionTTL.String(),
		}, "audit": s.audit.snapshot(),
	})
}

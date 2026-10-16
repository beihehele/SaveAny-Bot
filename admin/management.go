package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"

	botHandlers "github.com/krau/SaveAny-Bot/client/bot/handlers"
	"github.com/krau/SaveAny-Bot/client/bot/handlers/utils/mediautil"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/fnamest"
	"github.com/krau/SaveAny-Bot/pkg/msgfilter"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

func (s *Server) control(w http.ResponseWriter, r *http.Request, action string, fn func(context.Context) (any, error)) {
	select {
	case s.controls <- struct{}{}:
		defer func() { <-s.controls }()
	default:
		s.fail(w, http.StatusTooManyRequests, "management_busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	value, err := fn(ctx)
	if err != nil {
		log.FromContext(ctx).Warn("Admin management failed", "action", action, "error", s.redact(err.Error()))
		s.audit.add(action, "", "failed")
		status := http.StatusBadRequest
		code := "management_failed"
		if errors.Is(err, botHandlers.ErrManagementConflict) {
			status, code = http.StatusConflict, "management_conflict"
		} else if errors.Is(err, botHandlers.ErrListSyncBusy) {
			status, code = http.StatusTooManyRequests, "list_sync_busy"
		} else if errors.Is(err, errSaveSettingMissing) {
			status, code = http.StatusNotFound, "setting_not_found"
		} else if errors.Is(err, errSavePath) {
			code = "invalid_save_path"
		} else if errors.Is(err, errRuleData) {
			code = "invalid_rule_data"
		} else if errors.Is(err, errSaveStorage) {
			code = "save_storage_unavailable"
		}
		s.fail(w, status, code)
		return
	}
	s.audit.add(action, "", "ok")
	s.write(w, http.StatusOK, value)
}

func (s *Server) createRoute(w http.ResponseWriter, r *http.Request) {
	var req botHandlers.RouteRequest
	if decodeBody(w, r, &req, 16<<10) != nil || req.Count != 0 {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "create_route", func(ctx context.Context) (any, error) {
		route, err := s.management.CreateWatch(ctx, req)
		return map[string]any{"id": route.ID}, err
	})
}

func (s *Server) chats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64 `json:"user_chat_id"`
	}
	if decodeBody(w, r, &req, 1024) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "list_chats", func(ctx context.Context) (any, error) { return s.management.CachedChats(ctx, req.UserChatID) })
}

func (s *Server) refreshChats(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64 `json:"user_chat_id"`
	}
	if decodeBody(w, r, &req, 1024) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "refresh_chats", func(ctx context.Context) (any, error) { return s.management.RefreshChats(ctx, req.UserChatID) })
}

func (s *Server) stopRoute(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64 `json:"user_chat_id"`
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil || id == 0 || decodeBody(w, r, &req, 1024) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "stop_route", func(ctx context.Context) (any, error) {
		return map[string]bool{"ok": true}, s.management.StopWatch(ctx, req.UserChatID, uint(id))
	})
}

func (s *Server) createCopy(w http.ResponseWriter, r *http.Request) {
	var req botHandlers.RouteRequest
	if decodeBody(w, r, &req, 16<<10) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "create_copy", func(ctx context.Context) (any, error) {
		id, err := s.management.CreateCopy(ctx, s.ctx, req)
		return map[string]string{"task_id": id}, err
	})
}

func (s *Server) topics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64  `json:"user_chat_id"`
		Target     string `json:"target"`
	}
	if decodeBody(w, r, &req, 1024) != nil || len(req.Target) > 256 || req.Target == "" {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "list_topics", func(ctx context.Context) (any, error) {
		return s.management.CachedTopics(ctx, req.UserChatID, req.Target)
	})
}

func (s *Server) refreshTopics(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64  `json:"user_chat_id"`
		Target     string `json:"target"`
	}
	if decodeBody(w, r, &req, 1024) != nil || len(req.Target) > 256 || req.Target == "" {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "refresh_topics", func(ctx context.Context) (any, error) {
		return s.management.RefreshTopics(ctx, req.UserChatID, req.Target)
	})
}

type preferencePatch struct {
	UserChatID       int64   `json:"user_chat_id"`
	Silent           *bool   `json:"silent"`
	DefaultStorage   *string `json:"default_storage"`
	DefaultDir       *uint   `json:"default_dir"`
	ApplyRule        *bool   `json:"apply_rule"`
	FilenameStrategy *string `json:"filename_strategy"`
	FilenameTemplate *string `json:"filename_template"`
	ConflictStrategy *string `json:"conflict_strategy"`
}

func (s *Server) updatePreferences(w http.ResponseWriter, r *http.Request) {
	var req preferencePatch
	if decodeBody(w, r, &req, 16<<10) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "update_preferences", func(ctx context.Context) (any, error) {
		_, err := botHandlers.ManagementUser(ctx, req.UserChatID)
		if err != nil {
			return nil, err
		}
		values := map[string]any{}
		if req.Silent != nil {
			values["silent"] = *req.Silent
		}
		if req.ApplyRule != nil {
			values["apply_rule"] = *req.ApplyRule
		}
		if req.FilenameStrategy != nil {
			if _, err := fnamest.ParseFnameST(*req.FilenameStrategy); err != nil {
				return nil, err
			}
			values["filename_strategy"] = *req.FilenameStrategy
		}
		if req.FilenameTemplate != nil {
			if len(*req.FilenameTemplate) > 4096 {
				return nil, errors.New("template too long")
			}
			if _, err := template.New("filename").Parse(*req.FilenameTemplate); err != nil {
				return nil, err
			}
			values["filename_template"] = *req.FilenameTemplate
		}
		if req.ConflictStrategy != nil {
			if !tcbdata.IsConflictStrategy(*req.ConflictStrategy) {
				return nil, errors.New("invalid conflict strategy")
			}
			values["conflict_strategy"] = *req.ConflictStrategy
		}
		if req.DefaultStorage != nil {
			if _, err := storage.GetStorageByUserIDAndName(ctx, req.UserChatID, *req.DefaultStorage); err != nil {
				return nil, err
			}
			values["default_storage"] = *req.DefaultStorage
		}
		if req.DefaultDir != nil {
			values["default_dir"] = *req.DefaultDir
		}
		return map[string]bool{"ok": true}, database.UpdateUserPreferences(ctx, req.UserChatID, values)
	})
}

func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID   int64  `json:"user_chat_id"`
		Filter       string `json:"filter"`
		Text         string `json:"text"`
		Template     string `json:"template"`
		OriginalName string `json:"original_name"`
	}
	if decodeBody(w, r, &req, 32<<10) != nil || len(req.Filter) > 4096 || len(req.Template) > 4096 || len(req.Text) > 8192 || len(req.OriginalName) > 256 {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "preview", func(ctx context.Context) (any, error) {
		if _, err := botHandlers.ManagementUser(ctx, req.UserChatID); err != nil {
			return nil, err
		}
		filter, err := msgfilter.ParseFilter(req.Filter)
		if err != nil {
			return nil, err
		}
		match := filter == nil || msgfilter.Eval(filter, req.Text)
		if err := validatePreviewTemplate(req.Template); err != nil {
			return nil, err
		}
		message := &tg.Message{ID: 123, Date: 1700000000, PeerID: &tg.PeerChannel{ChannelID: 456}, Message: req.Text,
			Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, MimeType: "application/octet-stream", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: req.OriginalName}}}}}
		u := &database.User{FilenameStrategy: fnamest.Template.String(), FilenameTemplate: req.Template}
		file, err := tfile.FromMedia(message.Media, nil, mediautil.TfileOptions(ctx, u, message)...)
		if err != nil {
			return nil, err
		}
		return map[string]any{"matches": match, "filename": file.Name()}, nil
	})
}

// Preview accepts text and the documented fields, avoiding execution of
// unbounded printf/loop expressions in a synchronous HTTP request.
func validatePreviewTemplate(value string) error {
	tmpl, err := template.New("filename").Parse(value)
	if err != nil {
		return err
	}
	allowed := map[string]bool{"msgid": true, "msgtags": true, "msggen": true, "msgraw": true, "msgdate": true, "origname": true, "chatid": true}
	for _, item := range tmpl.Tree.Root.Nodes {
		switch item := item.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if len(item.Pipe.Decl) != 0 || len(item.Pipe.Cmds) != 1 || len(item.Pipe.Cmds[0].Args) != 1 {
				return errors.New("preview supports filename fields only")
			}
			field, ok := item.Pipe.Cmds[0].Args[0].(*parse.FieldNode)
			if !ok || len(field.Ident) != 1 || !allowed[field.Ident[0]] {
				return errors.New("unknown preview field")
			}
		default:
			return errors.New("preview supports filename fields only")
		}
	}
	return nil
}

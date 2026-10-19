package admin

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gorm.io/gorm"

	botHandlers "github.com/krau/SaveAny-Bot/client/bot/handlers"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/rule"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

var (
	errSavePath           = errors.New("invalid save directory")
	errRuleData           = errors.New("invalid save rule")
	errSaveStorage        = errors.New("storage is not available to this user")
	errSaveSettingMissing = errors.New("save setting not found")
)

func saveSettingResult(err error) (any, error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		err = errSaveSettingMissing
	}
	return map[string]bool{"ok": true}, err
}

func settingID(r *http.Request) (uint, error) {
	if r.Method == http.MethodPost {
		return 0, nil
	}
	id, err := strconv.ParseUint(r.PathValue("id"), 10, 32)
	if err != nil || id == 0 {
		return 0, errors.New("invalid setting ID")
	}
	return uint(id), nil
}

// saveDirectoryPath validates metadata only; directories are created by a save,
// and editing or deleting a shortcut never moves or removes stored files.
func saveDirectoryPath(chatID int64, name, raw string, isRule bool) (string, error) {
	chosen := isRule && name == rule.RuleStorNameChosen
	storageConfig := config.C().GetStorageByName(name)
	if !chosen && (storageConfig == nil || !config.C().HasStorage(chatID, name)) {
		return "", errSaveStorage
	}
	if !chosen && storageConfig.GetType() == storenum.Telegram {
		if isRule && raw == "" {
			return "", nil
		}
		return "", errSavePath
	}
	if isRule && raw == rule.RuleDirPathNewForAlbum {
		return raw, nil
	}
	if strings.IndexFunc(raw, unicode.IsControl) >= 0 {
		return "", errSavePath
	}
	clean, err := storagePath(strings.TrimSpace(raw))
	if err != nil {
		return "", errSavePath
	}
	// CHOSEN can resolve to local storage, so its paths must also work locally.
	if chosen || storageConfig.GetType() == storenum.Local {
		if strings.Contains(raw, ":") {
			return "", errSavePath
		}
	}
	clean = strings.TrimPrefix(clean, "/")
	if !isRule && clean == "" {
		return "", errSavePath
	}
	if clean != "" {
		// CHOSEN must work with every backend this user can select.
		for _, store := range config.C().Storages {
			if store.GetType() == storenum.Shardreel && config.C().HasStorage(chatID, store.GetName()) && (chosen || store.GetName() == name) {
				if _, err := shardreel.ValidatePath(clean); err != nil {
					return "", errSavePath
				}
				break
			}
		}
	}
	return clean, nil
}

func (s *Server) saveDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64  `json:"user_chat_id"`
		Storage    string `json:"storage"`
		Path       string `json:"path"`
	}
	id, err := settingID(r)
	if err != nil || decodeBody(w, r, &req, 16<<10) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "save_directory", func(ctx context.Context) (any, error) {
		user, err := botHandlers.ManagementUser(ctx, req.UserChatID)
		if err != nil {
			return nil, err
		}
		path, err := saveDirectoryPath(req.UserChatID, req.Storage, req.Path, false)
		if err != nil {
			return nil, err
		}
		if id == 0 {
			err = database.CreateDirForUser(ctx, user.ID, req.Storage, path)
		} else {
			err = database.UpdateDirForUser(ctx, user.ID, id, req.Storage, path)
		}
		return saveSettingResult(err)
	})
}

func (s *Server) deleteDirectory(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64 `json:"user_chat_id"`
	}
	id, err := settingID(r)
	if err != nil || decodeBody(w, r, &req, 1024) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "delete_directory", func(ctx context.Context) (any, error) {
		user, err := botHandlers.ManagementUser(ctx, req.UserChatID)
		if err != nil {
			return nil, err
		}
		return saveSettingResult(database.DeleteDirForUser(ctx, user.ID, id))
	})
}

func (s *Server) saveRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64   `json:"user_chat_id"`
		Type       string  `json:"type"`
		Data       *string `json:"data"`
		Storage    string  `json:"storage"`
		Path       string  `json:"path"`
	}
	id, err := settingID(r)
	if err != nil || decodeBody(w, r, &req, 16<<10) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "save_rule", func(ctx context.Context) (any, error) {
		user, err := botHandlers.ManagementUser(ctx, req.UserChatID)
		if err != nil {
			return nil, err
		}
		path, err := saveDirectoryPath(req.UserChatID, req.Storage, req.Path, true)
		if err != nil {
			return nil, err
		}
		data := ""
		if req.Data != nil {
			data = *req.Data
		} else if id != 0 {
			// An unchanged expression may contain hidden credentials. Preserve the
			// stored value rather than writing its display value back to the rule.
			found := false
			for _, existing := range user.Rules {
				if existing.ID == id {
					if existing.Type != req.Type {
						return nil, errRuleData
					}
					data, found = existing.Data, true
					break
				}
			}
			if !found {
				return nil, errSaveSettingMissing
			}
		}
		if strings.TrimSpace(data) == "" || len(data) > 4096 {
			return nil, errRuleData
		}
		switch req.Type {
		case rule.FileNameRegex.String(), rule.MessageRegex.String():
			if _, err := regexp.Compile(data); err != nil {
				return nil, errRuleData
			}
		case rule.IsAlbum.String():
			if data != "true" && data != "false" {
				return nil, errRuleData
			}
		default:
			return nil, errRuleData
		}
		entry := &database.Rule{UserID: user.ID, Type: req.Type, Data: data, StorageName: req.Storage, DirPath: path}
		entry.ID = id
		if id == 0 {
			err = database.CreateRule(ctx, entry)
		} else {
			err = database.UpdateRuleForUser(ctx, entry)
		}
		return saveSettingResult(err)
	})
}

func (s *Server) deleteSaveRule(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserChatID int64 `json:"user_chat_id"`
	}
	id, err := settingID(r)
	if err != nil || decodeBody(w, r, &req, 1024) != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.control(w, r, "delete_rule", func(ctx context.Context) (any, error) {
		user, err := botHandlers.ManagementUser(ctx, req.UserChatID)
		if err != nil {
			return nil, err
		}
		return saveSettingResult(database.DeleteRuleForUser(ctx, user.ID, id))
	})
}

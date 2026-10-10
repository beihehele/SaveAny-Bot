package admin

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/log"
	"github.com/spf13/viper"

	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/storage"
)

func protectedPaths() []string {
	cfg := config.C()
	result := []string{viper.ConfigFileUsed()}
	for _, file := range []string{cfg.DB.Path, cfg.DB.Session, cfg.Telegram.Userbot.Session} {
		if file == "" {
			continue
		}
		for _, suffix := range []string{"", "-wal", "-shm", "-journal"} {
			result = append(result, file+suffix)
		}
	}
	return result
}

func storagePath(raw string) (string, error) {
	raw = strings.ReplaceAll(raw, "\\", "/")
	if len(raw) > 4096 || strings.HasPrefix(raw, "//") || strings.ContainsAny(raw, ":\x00") {
		return "", errors.New("invalid storage path")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == ".." {
			return "", errors.New("invalid storage path")
		}
	}
	return path.Clean("/" + strings.TrimLeft(raw, "/")), nil
}

func (s *Server) localStorage(w http.ResponseWriter, r *http.Request) (storage.Storage, string, string, bool) {
	name := r.URL.Query().Get("storage")
	stor, ok := storage.GetStorage(name)
	cfg, valid := config.C().GetStorageByName(name).(*storcfg.LocalStorageConfig)
	if !ok || !valid || stor.Type() != storenum.Local {
		s.fail(w, http.StatusBadRequest, "local_storage_required")
		return nil, "", "", false
	}
	filePath, err := storagePath(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_path")
		return nil, "", "", false
	}
	return stor, cfg.BasePath, filePath, true
}

func (s *Server) blocked(base, relative string, opened os.FileInfo) bool {
	absolute, err := filepath.Abs(filepath.Join(base, filepath.FromSlash(strings.TrimLeft(relative, "/"))))
	if err != nil {
		return true
	}
	for _, protected := range s.protected {
		if protected == "" {
			continue
		}
		protectedAbs, err := filepath.Abs(protected)
		if err == nil && absolute == protectedAbs {
			return true
		}
		if opened != nil {
			if info, err := os.Stat(protected); err == nil && os.SameFile(opened, info) {
				return true
			}
		}
	}
	return false
}

type fileView struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"is_dir"`
	ModTime time.Time `json:"modified_at"`
}

func (s *Server) files(w http.ResponseWriter, r *http.Request) {
	stor, base, dir, ok := s.localStorage(w, r)
	if !ok {
		return
	}
	listable, ok := stor.(storage.StorageListable)
	if !ok {
		s.fail(w, http.StatusBadRequest, "local_storage_required")
		return
	}
	ctx := r.Context()
	entries, err := listable.ListFiles(ctx, dir)
	if err != nil {
		s.fail(w, http.StatusBadRequest, "file_unavailable")
		return
	}
	result := make([]fileView, 0, len(entries))
	for _, entry := range entries {
		if ctx.Err() != nil {
			return
		}
		info, err := os.Stat(filepath.Join(base, filepath.FromSlash(strings.TrimLeft(entry.Path, "/"))))
		if err != nil || (!info.IsDir() && !info.Mode().IsRegular()) || s.blocked(base, entry.Path, info) {
			continue
		}
		result = append(result, fileView{entry.Name, filepath.ToSlash(entry.Path), entry.Size, entry.IsDir, entry.ModTime})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].IsDir != result[j].IsDir {
			return result[i].IsDir
		}
		return result[i].Name < result[j].Name
	})
	start, end, err := page(r, len(result))
	if err != nil {
		s.fail(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s.write(w, http.StatusOK, map[string]any{"files": result[start:end], "total": len(result), "path": dir, "offset": start})
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	stor, base, filePath, ok := s.localStorage(w, r)
	if !ok {
		return
	}
	readable, ok := stor.(storage.StorageReadable)
	if !ok || s.blocked(base, filePath, nil) {
		s.fail(w, http.StatusForbidden, "file_unavailable")
		return
	}
	reader, _, err := readable.OpenFile(r.Context(), filePath)
	if err != nil {
		s.fail(w, http.StatusNotFound, "file_unavailable")
		return
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.FromContext(r.Context()).Warn("Close downloaded local file", "error", s.redact(err.Error()))
		}
	}()
	file, ok := reader.(*os.File)
	if !ok {
		s.fail(w, http.StatusInternalServerError, "file_unavailable")
		return
	}
	info, err := file.Stat()
	if err != nil || s.blocked(base, filePath, info) {
		s.fail(w, http.StatusForbidden, "file_unavailable")
		return
	}
	// Large attachments must not inherit the short JSON response deadline.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.fail(w, http.StatusInternalServerError, "file_unavailable")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(filePath)}))
	s.audit.add("download", s.redact(path.Base(filePath)), "requested")
	http.ServeContent(w, r, path.Base(filePath), info.ModTime(), file)
}

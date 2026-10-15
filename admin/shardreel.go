package admin

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/storage"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

func remoteStore(r *http.Request) (*shardreel.Store, bool) {
	backend, exists := storage.GetStorage(r.URL.Query().Get("storage"))
	if !exists {
		return nil, false
	}
	store, ok := backend.(*shardreel.Store)
	return store, ok
}

func (s *Server) remoteFiles(w http.ResponseWriter, r *http.Request) bool {
	store, ok := remoteStore(r)
	if !ok {
		return false
	}
	dir, err := storagePath(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, 400, "invalid_path")
		return true
	}
	entries, err := store.ListFiles(r.Context(), dir)
	if err != nil {
		s.fail(w, 502, "file_unavailable")
		return true
	}
	rows := make([]fileView, 0, len(entries))
	for _, entry := range entries {
		rows = append(rows, fileView{entry.Name, entry.Path, entry.Size, entry.IsDir, entry.ModTime})
	}
	start, end, err := page(r, len(rows))
	if err != nil {
		s.fail(w, 400, "invalid_request")
		return true
	}
	s.write(w, 200, map[string]any{"files": rows[start:end], "total": len(rows), "path": dir, "offset": start})
	return true
}

func (s *Server) remoteDownload(w http.ResponseWriter, r *http.Request) bool {
	store, ok := remoteStore(r)
	if !ok {
		return false
	}
	target, err := storagePath(r.URL.Query().Get("path"))
	if err != nil {
		s.fail(w, 400, "invalid_path")
		return true
	}
	reader, size, err := store.OpenFile(r.Context(), target)
	if err != nil {
		s.fail(w, 502, "file_unavailable")
		return true
	}
	defer func() {
		if err := reader.Close(); err != nil {
			log.FromContext(r.Context()).Warn("Close remote download", "error", err)
		}
	}()
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		s.fail(w, 500, "file_unavailable")
		return true
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": path.Base(target)}))
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	if _, err := io.Copy(w, reader); err != nil {
		log.FromContext(r.Context()).Warn("Remote download interrupted", "error", err)
	}
	return true
}

func (s *Server) checkStorage(w http.ResponseWriter, r *http.Request) {
	backend, exists := storage.GetStorage(r.PathValue("name"))
	store, ok := backend.(*shardreel.Store)
	if !exists || !ok {
		s.fail(w, 400, "invalid_request")
		return
	}
	value, err := store.Check(r.Context())
	if err != nil {
		s.fail(w, 502, "upload_connection_failed")
		return
	}
	s.write(w, 200, value)
}

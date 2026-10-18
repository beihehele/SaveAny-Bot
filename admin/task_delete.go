package admin

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/storage"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

func (s *Server) deleteTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	found := false
	for _, row := range s.taskRows(r.Context()) {
		if row.TaskID != id {
			continue
		}
		found = true
		if !row.CanDelete || core.IsTaskExecuting(id) {
			s.fail(w, http.StatusConflict, "task_not_finished")
			return
		}
		break
	}
	if !found {
		s.fail(w, http.StatusNotFound, "task_not_found")
		return
	}
	stores := make(map[string]*shardreel.Store)
	for name, backend := range storage.AllStorages() {
		if store, ok := backend.(*shardreel.Store); ok {
			stores[name] = store
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if _, err := shardreel.DeleteTask(ctx, id, stores); err != nil {
		if errors.Is(err, shardreel.ErrTaskNotFinished) {
			s.fail(w, http.StatusConflict, "task_not_finished")
		} else {
			log.FromContext(r.Context()).Warn("Delete task record", "task", id, "error", s.redact(err.Error()))
			s.audit.add("delete_task", id, "failed")
			s.fail(w, http.StatusConflict, "task_delete_failed")
		}
		return
	}
	api.DeleteTask(id)
	taskhistory.Default.Delete(id)
	s.audit.add("delete_task", id, "ok")
	s.write(w, http.StatusOK, map[string]string{"status": "deleted"})
}

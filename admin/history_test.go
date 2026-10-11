package admin

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestConsoleRetainsBotFailureAndRedactsIt(t *testing.T) {
	id := "owned-history-admin-test"
	ctx, rollback, _ := taskhistory.Default.Track(t.Context(), id, "copy", "sensitive-token title")
	defer rollback()
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseStart})
	taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Err: errors.New("sensitive-token failure"), Outcome: taskresult.OutcomeFailed})
	s := testServer(t)
	s.secrets = append(s.secrets, "sensitive-token")
	cookie, csrf := signIn(t, s)
	denied := perform(s, http.MethodGet, "/admin/api/v1/tasks?q="+id, "", nil, "", "")
	if denied.Code != 401 {
		t.Fatal("history became public")
	}
	response := perform(s, http.MethodGet, "/admin/api/v1/tasks?q="+id, "", cookie, csrf, "")
	body := response.Body.String()
	if response.Code != 200 || !strings.Contains(body, "\"scope\":\"history\"") || !strings.Contains(body, "\"source\":\"bot\"") || !strings.Contains(body, "\"status\":\"failed\"") || strings.Contains(body, "sensitive-token") {
		t.Fatalf("invalid history projection: %d %s", response.Code, body)
	}
	diagnostics := perform(s, http.MethodGet, "/admin/api/v1/diagnostics", "", cookie, csrf, "")
	if diagnostics.Code != 200 || !strings.Contains(diagnostics.Body.String(), "\"watch\":") || !strings.Contains(diagnostics.Body.String(), "\"disks\":") || !strings.Contains(diagnostics.Body.String(), "\"scans\":") || strings.Contains(diagnostics.Body.String(), "sensitive-token") {
		t.Fatal("diagnostic whitelist invalid")
	}
}

func TestDiskSamplingDoesNotCreateMissingPath(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "windows" {
		t.Skip("disk observations are unavailable on this platform")
	}
	base := t.TempDir()
	if _, err := availableDiskBytes(base); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(base, "missing")
	if _, err := availableDiskBytes(missing); err == nil {
		t.Fatal("missing disk path accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("sampling modified filesystem")
	}
}

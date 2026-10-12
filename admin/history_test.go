package admin

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	botHandlers "github.com/krau/SaveAny-Bot/client/bot/handlers"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestConsoleCopyCountsKeepCompletionAndPendingMeaning(t *testing.T) {
	s := testServer(t)
	cookie, csrf := signIn(t, s)
	for _, tc := range []struct {
		id          string
		counts      taskresult.Counts
		outcome     taskresult.Outcome
		status      string
		copyOutcome string
	}{
		{"copy-all-failed-counts", taskresult.Counts{Total: 2, Failed: 2}, taskresult.OutcomeSuccess, "completed", "failed"},
		{"copy-partial-counts", taskresult.Counts{Total: 2, Succeeded: 1, Failed: 1}, taskresult.OutcomeSuccess, "completed", "partial"},
		{"copy-succeeded-counts", taskresult.Counts{Total: 2, Succeeded: 2}, taskresult.OutcomeSuccess, "completed", "succeeded"},
		{"copy-empty-counts", taskresult.Counts{}, taskresult.OutcomeSuccess, "completed", "empty"},
		{"copy-cancelled-counts", taskresult.Counts{Total: 2, Succeeded: 1, Pending: 1}, taskresult.OutcomeCancelled, "cancelled", ""},
	} {
		t.Run(tc.id, func(t *testing.T) {
			ctx, rollback, _ := taskhistory.Default.Track(t.Context(), tc.id, "copy", "synthetic copy")
			defer rollback()
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseStart})
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: tc.outcome, ResultSummary: &tc.counts})
			response := perform(s, http.MethodGet, "/admin/api/v1/tasks?q="+tc.id, "", cookie, csrf, "")
			var body struct {
				Tasks []taskRow `json:"tasks"`
			}
			if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Tasks) != 1 {
				t.Fatalf("invalid history response: %d %s", response.Code, response.Body.String())
			}
			row := body.Tasks[0]
			if row.CopyOutcome != tc.copyOutcome {
				t.Fatalf("copy result hidden/misclassified: %+v", row)
			}
			if row.Type != "copy" || string(row.Status) != tc.status || row.Scope != "history" || row.Source != "bot" || row.Progress != nil || row.ResultSummary == nil || *row.ResultSummary != tc.counts {
				t.Fatalf("copy units/status became file progress: %+v", row)
			}
			// Mutating a returned value must not affect retained observations.
			row.ResultSummary.Total = 999
			for _, record := range taskhistory.Default.Snapshots() {
				if record.ID == tc.id && *record.ResultSummary != tc.counts {
					t.Fatal("HTTP projection mutated history counts")
				}
			}
		})
	}
}

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

func TestWatchForwardDiagnosticsAreReadOnlyAndAuthenticated(t *testing.T) {
	s := testServer(t)
	before := botHandlers.GetWatchForwardStats()
	denied := perform(s, http.MethodGet, "/admin/api/v1/diagnostics", "", nil, "", "")
	if denied.Code != http.StatusUnauthorized {
		t.Fatal("forward diagnostics became public")
	}
	cookie, csrf := signIn(t, s)
	response := perform(s, http.MethodGet, "/admin/api/v1/diagnostics", "", cookie, csrf, "")
	var body struct {
		Forward botHandlers.WatchForwardStats `json:"watch_forward"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Forward != before {
		t.Fatalf("forward diagnostic projection lost counts: %d %s", response.Code, response.Body.String())
	}
	if before.Workers != 4 || before.Capacity != 100 || botHandlers.GetWatchForwardStats() != before {
		t.Fatal("observation launched work or changed limits")
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

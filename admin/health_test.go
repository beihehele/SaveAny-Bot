package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
)

func TestOutboxOverviewCountsReservationsWithoutChangingFiles(t *testing.T) {
	const env = "SAVEANY_OUTBOX_OVERVIEW_TEST_ROOT"
	if os.Getenv(env) == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), binary, "-test.run=^TestOutboxOverviewCountsReservationsWithoutChangingFiles$", "-test.count=1")
		command.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated outbox overview: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(os.Getenv(env))
	if err := os.WriteFile("config.toml", []byte(`[[storages]]
name='uploads'
type='shardreel'
enable=true
url='http://unused.invalid'
api_key='isolated-storage-test-key-32-bytes'
max_pending_bytes=10000
[[storages]]
name='empty'
type='shardreel'
enable=true
url='http://unused.invalid'
api_key='isolated-storage-test-key-32-bytes'
[[storages]]
name='local'
type='local'
enable=true
base_path='downloads'
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), "config.toml"); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	db := database.ShardReelDB(t.Context())
	var files []database.ShardReelFile
	for i, status := range []string{"preparing", "queued", "uploading", "processing", "retry_wait", "cancelling", "failed", "succeeded", "skipped", "cancelled", "failed"} {
		files = append(files, database.ShardReelFile{ID: fmt.Sprint(i), Storage: "uploads", Status: status, Size: int64((i + 1) * 100), LocalClean: i >= 8})
	}
	files = append(files, database.ShardReelFile{ID: "old-storage", Storage: "removed", Status: "queued", Size: 9999})
	if err := db.Create(&files).Error; err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	if denied := perform(s, http.MethodGet, "/admin/api/v1/overview", "", nil, "", ""); denied.Code != 401 {
		t.Fatal("outbox summary became public")
	}
	cookie, csrf := signIn(t, s)
	response := perform(s, http.MethodGet, "/admin/api/v1/overview", "", cookie, csrf, "")
	var body struct {
		Outboxes []uploadOutbox `json:"outboxes"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Outboxes) != 2 {
		t.Fatalf("invalid overview: %d %s", response.Code, response.Body.String())
	}
	if got := body.Outboxes[0]; got.PendingFiles != 6 || got.FailedFiles != 2 || got.ReservedBytes != 3600 || got.MaxPendingBytes != 10000 {
		t.Fatalf("incorrect reservation totals: %+v", got)
	}
	if got := body.Outboxes[1]; got.Storage != "empty" || got.PendingFiles != 0 || got.FailedFiles != 0 || got.ReservedBytes != 0 || got.MaxPendingBytes != 20<<30 {
		t.Fatalf("empty outbox not shown: %+v", got)
	}
	for i := range files {
		if err := db.First(&files[i], "id = ?", files[i].ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	after, err := json.Marshal(files)
	if err != nil || string(before) != string(after) {
		t.Fatalf("overview changed transfer records: %v", err)
	}
	if _, err := os.Stat("data/shardreel/uploads"); !os.IsNotExist(err) {
		t.Fatalf("overview touched outbox filesystem: %v", err)
	}
	for _, status := range []string{"queued", "running", "cancelling", "completed", "failed", "cancelled"} {
		group := database.ShardReelTask{ID: "off-page-task", Status: status}
		if err := db.Save(&group).Error; err != nil {
			t.Fatal(err)
		}
		response := perform(s, http.MethodGet, "/admin/api/v1/tasks?q=no-match&status=completed&offset=100", "", cookie, csrf, "")
		var tasks struct {
			Tasks          []taskRow `json:"tasks"`
			HasActiveTasks bool      `json:"has_active_tasks"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &tasks) != nil || len(tasks.Tasks) != 0 {
			t.Fatalf("invalid filtered tasks: %d %s", response.Code, response.Body.String())
		}
		wantActive := status == "queued" || status == "running" || status == "cancelling"
		if tasks.HasActiveTasks != wantActive {
			t.Fatalf("filtered %s task changed polling activity: got %v", status, tasks.HasActiveTasks)
		}
	}
	if err := db.Migrator().DropTable(&database.ShardReelFile{}); err != nil {
		t.Fatal(err)
	}
	failed := perform(s, http.MethodGet, "/admin/api/v1/overview", "", cookie, csrf, "")
	var failure struct {
		Error string `json:"error"`
	}
	if failed.Code != 500 || json.Unmarshal(failed.Body.Bytes(), &failure) != nil || failure.Error != "internal_error" {
		t.Fatalf("unavailable accounting appeared healthy: %d %s", failed.Code, failed.Body.String())
	}
}

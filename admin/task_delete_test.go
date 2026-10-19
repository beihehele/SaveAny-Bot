package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/api"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestTaskRecordDeletionHTTP(t *testing.T) {
	const env = "SAVEANY_ADMIN_DELETE_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestTaskRecordDeletionHTTP$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated task deletion: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	if err := os.WriteFile("config.toml", []byte("[db]\npath='state.db'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), "config.toml"); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	db := database.ShardReelDB(t.Context())
	if err := db.AutoMigrate(&database.ShardReelTask{}, &database.ShardReelFile{}); err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	cookie, csrf := signIn(t, s)
	saved := filepath.Join(dir, "saved.bin")
	if err := os.WriteFile(saved, []byte("keep saved file"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, status := range []api.TaskStatus{api.TaskStatusCompleted, api.TaskStatusFailed, api.TaskStatusCancelled} {
		id := string(status)
		ctx, _, _ := taskhistory.Default.Track(t.Context(), id, "tgfiles", "saved media")
		info := api.RegisterTask(id, "tgfiles", "local", saved, "saved media", "")
		info.UpdateStatus(status)
		outcome := map[api.TaskStatus]taskresult.Outcome{api.TaskStatusCompleted: taskresult.OutcomeSuccess, api.TaskStatusFailed: taskresult.OutcomeFailed, api.TaskStatusCancelled: taskresult.OutcomeCancelled}[status]
		taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: outcome})
		// A single ID can appear in all three sources; deletion must remove all.
		group := database.ShardReelTask{ID: id, Status: string(status), DownloadDone: true, Notified: true}
		file := database.ShardReelFile{ID: id + "_file", TaskID: id, Status: "succeeded", LocalClean: true, RemoteAcked: true}
		if err := db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
		endpoint := "/admin/api/v1/tasks/" + id
		for _, check := range []struct {
			cookie       *http.Cookie
			csrf, origin string
			status       int
		}{
			{nil, "", "", 401}, {cookie, "", "http://console.test", 403}, {cookie, csrf, "http://other.test", 403},
		} {
			if response := perform(s, "DELETE", endpoint, "", check.cookie, check.csrf, check.origin); response.Code != check.status {
				t.Fatalf("delete access check: %d %s", response.Code, response.Body.String())
			}
		}
		for _, row := range s.taskRows(t.Context()) {
			if row.TaskID == id && (!row.CanDelete || row.CanCancel) {
				t.Fatalf("terminal actions incorrect: %+v", row)
			}
		}
		response := perform(s, "DELETE", endpoint, "", cookie, csrf, "http://console.test")
		if response.Code != 200 {
			t.Fatalf("delete: %d %s", response.Code, response.Body.String())
		}
		if _, exists := api.GetTask(id); exists {
			t.Fatal("API observer retained")
		}
		taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: outcome})
		if response := perform(s, "GET", "/admin/api/v1/tasks?q="+id, "", cookie, csrf, ""); response.Code != 200 || !containsEmptyTasks(response.Body.Bytes()) {
			t.Fatalf("deleted record reappeared: %s", response.Body.String())
		}
		var count int64
		if err := db.Model(&database.ShardReelFile{}).Where("task_id = ?", id).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("file history retained: count=%d err=%v", count, err)
		}
		if response := perform(s, "DELETE", endpoint, "", cookie, csrf, "http://console.test"); response.Code != 404 {
			t.Fatal("missing record accepted", response.Code)
		}
	}
	if content, err := os.ReadFile(saved); err != nil || string(content) != "keep saved file" {
		t.Fatalf("history deletion changed saved file: %q %v", content, err)
	}
	// Collection receipts belong to the record; deleting them never needs a
	// Telegram client or removes the actual collected messages.
	collectionCtx, _, _ := taskhistory.Default.Track(t.Context(), "collection-record", "tgfiles", "TG收藏")
	observer := api.RegisterTask("collection-record", "tgfiles", "TG收藏", "", "TG收藏", "")
	collectionCtx = taskevent.WithSink(collectionCtx, observer)
	taskevent.Emit(collectionCtx, taskevent.Event{Phase: taskevent.PhaseProgress, DeliveryProgress: &taskevent.DeliveryProgress{Stage: "sent"}, ResultLinks: []string{"https://t.me/c/222/501"}})
	taskevent.Emit(collectionCtx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
	if response := perform(s, "DELETE", "/admin/api/v1/tasks/collection-record", "", cookie, csrf, "http://console.test"); response.Code != 200 {
		t.Fatal("collection record deletion failed", response.Body.String())
	}
	taskevent.Emit(collectionCtx, taskevent.Event{Phase: taskevent.PhaseProgress, ResultLinks: []string{"https://t.me/c/222/501"}})
	if response := perform(s, "GET", "/admin/api/v1/tasks?q=collection-record", "", cookie, csrf, ""); response.Code != 200 || !containsEmptyTasks(response.Body.Bytes()) {
		t.Fatal("late receipt recreated collection record", response.Body.String())
	}
	for _, status := range []string{"queued", "running"} {
		id := "active-" + status
		ctx, rollback, _ := taskhistory.Default.Track(t.Context(), id, "copy", "active")
		defer rollback()
		if status == "running" {
			taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseStart})
		}
		response := perform(s, "DELETE", "/admin/api/v1/tasks/"+id, "", cookie, csrf, "http://console.test")
		if response.Code != 409 {
			t.Fatalf("active task deleted: %d %s", response.Code, response.Body.String())
		}
	}
	// Removing the last row of page two must fall back to the remaining page.
	for i := 0; i < 101; i++ {
		ctx, _, _ := taskhistory.Default.Track(t.Context(), fmt.Sprintf("pages-%03d", i), "copy", "page fixture")
		taskevent.Emit(ctx, taskevent.Event{Phase: taskevent.PhaseDone, Outcome: taskresult.OutcomeSuccess})
	}
	readPage := func() (taskRow, int, int) {
		response := perform(s, "GET", "/admin/api/v1/tasks?q=pages-&offset=100", "", cookie, csrf, "")
		var data struct {
			Tasks  []taskRow `json:"tasks"`
			Total  int       `json:"total"`
			Offset int       `json:"offset"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || len(data.Tasks) == 0 {
			t.Fatal("page response invalid", response.Body.String())
		}
		return data.Tasks[0], data.Total, data.Offset
	}
	last, _, _ := readPage()
	if response := perform(s, "DELETE", "/admin/api/v1/tasks/"+last.TaskID, "", cookie, csrf, "http://console.test"); response.Code != 200 {
		t.Fatal("page deletion failed", response.Body.String())
	}
	_, total, offset := readPage()
	if total != 100 || offset != 0 {
		t.Fatalf("empty last page retained: total=%d offset=%d", total, offset)
	}
	// A failed schema read must not masquerade as an absent upload table.
	api.RegisterTask("closed-db", "tgfiles", "local", "/", "closed database", "").UpdateStatus(api.TaskStatusCompleted)
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	response := perform(s, "DELETE", "/admin/api/v1/tasks/closed-db", "", cookie, csrf, "http://console.test")
	if _, exists := api.GetTask("closed-db"); response.Code != 409 || !exists {
		t.Fatalf("database failure reported deletion success: %d %s retained=%v", response.Code, response.Body.String(), exists)
	}
}

func containsEmptyTasks(body []byte) bool {
	var data struct {
		Tasks []taskRow `json:"tasks"`
	}
	return json.Unmarshal(body, &data) == nil && len(data.Tasks) == 0
}

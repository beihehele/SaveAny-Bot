package admin

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
)

func TestConsoleKeepsBotDownloadProgressUntilUploadHandoff(t *testing.T) {
	const env = "SAVEANY_ADMIN_DOWNLOAD_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestConsoleKeepsBotDownloadProgressUntilUploadHandoff$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated download projection: %v\n%s", err, output)
		}
		return
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("[db]\npath=%q\n", filepath.ToSlash(filepath.Join(dir, "state.db")))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	const id = "bot-download"
	ctx, rollback, _ := taskhistory.Default.Track(t.Context(), id, "tgfiles", "album")
	defer rollback()
	taskevent.Emit(ctx, taskevent.Event{TaskID: id, Phase: taskevent.PhaseStart})
	taskevent.Emit(ctx, taskevent.Event{TaskID: id, Phase: taskevent.PhaseProgress, TotalBytes: 1000, DownloadedBytes: 250})
	db := database.ShardReelDB(t.Context())
	if err := db.AutoMigrate(&database.ShardReelTask{}, &database.ShardReelFile{}); err != nil {
		t.Fatal(err)
	}
	group := database.ShardReelTask{ID: id, Status: "running", ExpectedFiles: 4}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	file := database.ShardReelFile{ID: "file", TaskID: id, ElementID: "file", Status: "preparing", Size: 1000}
	if err := db.Create(&file).Error; err != nil {
		t.Fatal(err)
	}
	s := testServer(t)
	rows := s.taskRows(t.Context())
	if len(rows) != 1 || !rows[0].Downloading || rows[0].Progress == nil || rows[0].Progress.Percent != 25 || rows[0].Progress.DownloadedBytes != 250 || rows[0].ResultSummary.Interrupted != 0 {
		t.Fatalf("live Bot download overwritten by outbox: %+v", rows)
	}
	if err := db.Model(&group).Update("download_done", true).Error; err != nil {
		t.Fatal(err)
	}
	rows = s.taskRows(t.Context())
	if len(rows) != 1 || rows[0].Downloading || rows[0].Progress != nil || rows[0].ResultSummary.Interrupted != 3 {
		t.Fatalf("stale download progress survived handoff: %+v", rows)
	}
}

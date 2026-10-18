package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

func TestUploadResponsesBatchQueriesPreserveProjections(t *testing.T) {
	const env = "SAVEANY_BATCH_UPLOAD_TEST_ROOT"
	if os.Getenv(env) == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(t.Context(), binary, "-test.run=^TestUploadResponsesBatchQueriesPreserveProjections$", "-test.count=1")
		command.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated batch projection: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(os.Getenv(env))
	if err := os.WriteFile("config.toml", []byte(`[[storages]]
name='test-outbox'
type='shardreel'
enable=true
url='http://unused.invalid'
api_key='isolated-storage-test-key-32-bytes'
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), "config.toml"); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	db := database.ShardReelDB(t.Context())
	groups := []database.ShardReelTask{
		{ID: "album", Status: "completed", ExpectedFiles: 2},
		{ID: "retry", Status: "failed", ExpectedFiles: 1},
		{ID: "cancelled", Status: "cancelled", CancelRequested: true, ExpectedFiles: 1},
		{ID: "no-files", Status: "failed", ExpectedFiles: 1},
	}
	if err := db.Create(&groups).Error; err != nil {
		t.Fatal(err)
	}
	files := []database.ShardReelFile{
		{ID: "album-b", TaskID: "album", ElementID: "b", Storage: "test-outbox", Path: "/caption/b", Status: "succeeded", CreatedAt: time.Unix(2, 0)},
		{ID: "album-a", TaskID: "album", ElementID: "a", Storage: "test-outbox", Path: "/caption/a", Status: "succeeded", CreatedAt: time.Unix(1, 0)},
		{ID: "retry-file", TaskID: "retry", ElementID: "r", Storage: "test-outbox", Path: "/original", SavedPath: "/renamed", Status: "failed", SHA256: "retained"},
		{ID: "cancel-file", TaskID: "cancelled", ElementID: "c", Storage: "test-outbox", Status: "failed", SHA256: "retained"},
	}
	if err := db.Create(&files).Error; err != nil {
		t.Fatal(err)
	}
	expected := make(map[string]TaskInfoResponse)
	for _, group := range groups {
		response, err := UploadResponse(t.Context(), group)
		if err != nil {
			t.Fatal(err)
		}
		expected[group.ID] = response
	}
	if expected["album"].UploadFiles[0].ID != "album-a" || !expected["retry"].CanRetry || expected["retry"].Path != "/renamed" || expected["cancelled"].CanRetry {
		t.Fatalf("invalid fixture projections: %+v", expected)
	}
	var queries atomic.Int64
	const callback = "test:count-batch-queries"
	if err := db.Callback().Query().After("gorm:query").Register(callback, func(*gorm.DB) { queries.Add(1) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Callback().Query().Remove(callback); err != nil {
			t.Error(err)
		}
	})
	responses, err := uploadResponses(t.Context())
	if err != nil || len(responses) != len(expected) {
		t.Fatalf("batch projection: %v, %+v", err, responses)
	}
	if queries.Load() != 2 {
		t.Fatalf("expected two batch queries, got %d", queries.Load())
	}
	for _, response := range responses {
		got, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.Marshal(expected[response.TaskID])
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("batch changed %s: got %s, want %s", response.TaskID, got, want)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := uploadResponses(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("batch read lost request cancellation: %v", err)
	}
}

func TestUploadMergeUsesDurableStateAndKeepsOrdinaryTasks(t *testing.T) {
	tasks := []TaskInfoResponse{{TaskID: "upload", Status: TaskStatusRunning}, {TaskID: "ordinary", Status: TaskStatusCompleted}}
	uploads := []TaskInfoResponse{{TaskID: "upload", Status: TaskStatusCancelled, Path: "/caption/file.bin"}, {TaskID: "new", Status: TaskStatusQueued}}
	result := mergeUploadResponses(tasks, uploads)
	if len(result) != 3 || result[0].Status != TaskStatusCancelled || result[0].Path != "/caption/file.bin" ||
		result[1].TaskID != "ordinary" || result[1].Status != TaskStatusCompleted || result[2].TaskID != "new" {
		t.Fatalf("incorrect merged observations: %+v", result)
	}
}

func TestUploadResponseKeepsLiveDownloadProgressOnlyDuringDownload(t *testing.T) {
	const id = "live-download-projection"
	RegisterTask(id, "tgfiles", "outbox", "", "album", "")
	t.Cleanup(func() { DeleteTask(id) })
	live, ok := GetTask(id)
	if !ok {
		t.Fatal("missing observer")
	}
	live.Emit(taskevent.Event{TaskID: id, Phase: taskevent.PhaseStart})
	live.Emit(taskevent.Event{TaskID: id, Phase: taskevent.PhaseProgress, TotalBytes: 1000, DownloadedBytes: 250})
	group := database.ShardReelTask{ID: id, Status: "running", ExpectedFiles: 2}
	files := []database.ShardReelFile{{ElementID: "one", Status: "preparing", Size: 1000}}
	response := uploadResponse(group, files)
	if !response.Downloading || response.Progress == nil || response.Progress.Percent != 25 || response.Progress.DownloadedBytes != 250 || response.ResultSummary.Interrupted != 0 {
		t.Fatalf("lost active download: %+v", response)
	}
	group.DownloadDone = true
	response = uploadResponse(group, files)
	if response.Downloading || response.Progress != nil || response.ResultSummary.Interrupted != 1 {
		t.Fatalf("stale download progress survived upload handoff: %+v", response)
	}
	group.DownloadDone = false
	group.CancelRequested = true
	if response = uploadResponse(group, files); response.Downloading || response.Progress != nil {
		t.Fatalf("cancelled download still shown as active: %+v", response)
	}
}

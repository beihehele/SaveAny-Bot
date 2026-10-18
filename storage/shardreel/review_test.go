package shardreel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestOfflineCancellationOfUnstartedUploadCleansLocalPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/storage/info" {
			t.Errorf("unstarted cancellation contacted remote upload: %s", r.URL.Path)
		}
		w.WriteHeader(503)
	}))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("cancel offline"), "offline")
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).
		Updates(map[string]any{"cancel_requested": true, "download_done": true}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx, nil) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		current := rowAt(t, s, row.ID)
		if current.Status == "cancelled" && current.LocalClean {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("offline cancellation stalled: %+v", rowAt(t, s, row.ID))
}

func TestGeneric404DoesNotConfirmRemoteCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		if err := json.NewEncoder(w).Encode(map[string]string{"detail": "storage API disabled"}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("retain"), "missing-api")
	if err := s.update(t.Context(), row.ID, map[string]any{"remote_started": true}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Update("cancel_requested", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	current := rowAt(t, s, row.ID)
	if current.Status == "cancelled" || current.LocalClean {
		t.Fatalf("generic 404 caused unconfirmed deletion: %+v", current)
	}
}

func TestOnlyMissingReceipt404AllowsAcknowledgmentCleanup(t *testing.T) {
	for _, code := range []string{"", "TRANSFER_NOT_FOUND"} {
		t.Run(code, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(404)
				if err := json.NewEncoder(w).Encode(map[string]any{"detail": map[string]string{"code": code}}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			s := fixture(t, server.URL)
			row := stage(t, s, []byte("saved"), "ack")
			if err := s.update(t.Context(), row.ID, map[string]any{"status": "succeeded", "remote_started": true}); err != nil {
				t.Fatal(err)
			}
			if err := s.maintenance(t.Context()); err != nil {
				t.Fatal(err)
			}
			if current := rowAt(t, s, row.ID); current.RemoteAcked != (code == "TRANSFER_NOT_FOUND") {
				t.Fatalf("incorrect receipt acknowledgment: %+v", current)
			}
		})
	}
}

func TestDiscardIncludesMembersThatNeverReachedOutbox(t *testing.T) {
	previous := taskresult.Summary{Elements: []taskresult.Element{{ID: "saved", State: taskresult.Pending}, {ID: "download-failed", State: taskresult.Failed}}}
	encoded, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	group := database.ShardReelTask{Summary: string(encoded), CancelRequested: true}
	result := Summary(group, []database.ShardReelFile{{ElementID: "saved", Status: "succeeded"}})
	if result.Succeeded != 1 || result.Cancelled != 1 || result.Failed != 0 {
		t.Fatalf("discard left an untracked failed member: %+v", result)
	}
	group.CancelRequested = false
	if result := Summary(group, []database.ShardReelFile{{ElementID: "saved", Status: "succeeded"}}); result.Failed != 1 {
		t.Fatal("ordinary failure was masked without explicit discard")
	}
}

func TestSummaryWaitsForUnstartedDownloads(t *testing.T) {
	group := database.ShardReelTask{Status: "running", ExpectedFiles: 4}
	files := []database.ShardReelFile{{ElementID: "first", Status: "preparing"}, {ElementID: "second", Status: "preparing"}}
	result := Summary(group, files)
	if result.Total != 4 || result.Pending != 4 || result.Interrupted != 0 {
		t.Fatalf("unstarted downloads reported as interrupted: %+v", result)
	}
	group.DownloadDone = true
	result = Summary(group, files)
	if result.Pending != 2 || result.Interrupted != 2 {
		t.Fatalf("finished download lost missing members: %+v", result)
	}
	group.DownloadDone = false
	group.CancelRequested = true
	result = Summary(group, files)
	if result.Cancelled != 2 || result.Pending != 2 || result.Interrupted != 0 {
		t.Fatalf("cancellation lost unstarted members: %+v", result)
	}
}

func TestDiscardReconcilesAndPrunesPreviouslyFailedTask(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	row := stage(t, s, []byte("discard"), "discard")
	previous, err := json.Marshal(taskresult.Summary{Elements: []taskresult.Element{
		{ID: row.ElementID, State: taskresult.Pending}, {ID: "not-downloaded", State: taskresult.Failed},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Updates(map[string]any{
		"summary": string(previous), "download_done": true, "cancel_requested": true, "status": "cancelling", "error": "old failure",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.step(t.Context(), false); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(t.Context(), s.db, nil); err != nil {
		t.Fatal(err)
	}
	var group database.ShardReelTask
	if err := s.db.First(&group, "id = ?", row.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if group.Status != "cancelled" || !group.Notified || group.Error != "" {
		t.Fatalf("discard did not finish: %+v", group)
	}
	if err := s.db.Model(&group).UpdateColumn("updated_at", time.Now().Add(-8*24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if err := pruneHistory(t.Context(), s.db); err != nil {
		t.Fatal(err)
	}
	var remaining int64
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("clean discarded task was retained: count=%d error=%v", remaining, err)
	}
}

func TestPayloadRecoveryRespectsCancellation(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	row := stage(t, s, []byte("complete payload"), "stop-recovery")
	if err := s.update(t.Context(), row.ID, map[string]any{"status": "preparing", "sha256": ""}); err != nil {
		t.Fatal(err)
	}
	row.Status, row.SHA256 = "preparing", ""
	root, err := os.OpenRoot(s.cfg.SpoolPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := s.recoverPayload(ctx, &row, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("recovery ignored cancellation: %v", err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "preparing" || current.LocalClean {
		t.Fatalf("interrupted recovery changed the payload: %+v", current)
	}
}

func TestRecoveryFinishesCancelledPartialDownload(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	row := stage(t, s, []byte("partial download"), "cancelled-restart")
	if err := os.Remove(filepath.Join(s.cfg.SpoolPath, row.ID, "payload")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.cfg.SpoolPath, row.ID, "payload.part"), []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.update(t.Context(), row.ID, map[string]any{"status": "preparing", "sha256": ""}); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Update("cancel_requested", true).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := reconcile(t.Context(), s.db, nil); err != nil {
		t.Fatal(err)
	}
	var group database.ShardReelTask
	if err := s.db.First(&group, "id = ?", row.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); group.Status != "cancelled" || current.Status != "cancelled" || !current.LocalClean || current.Error != "" {
		t.Fatalf("explicit cancellation became a download failure after restart: task=%+v file=%+v", group, current)
	}
}

func TestManualActionDuringCompletionKeepsItsNewLifecycle(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	configuration := fmt.Sprintf("[db]\npath=%q\n", filepath.ToSlash(filepath.Join(root, "tasks.db")))
	if err := os.WriteFile(configPath, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	db := database.ShardReelDB(t.Context())
	if err := db.AutoMigrate(&database.ShardReelTask{}, &database.ShardReelFile{}); err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, action := range []string{"retry", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s := fixture(t, "http://unused.invalid")
			s.db = db
			row := stage(t, s, []byte("retained failed upload"), action)
			if err := s.update(t.Context(), row.ID, map[string]any{"status": "failed"}); err != nil {
				t.Fatal(err)
			}
			if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Update("download_done", true).Error; err != nil {
				t.Fatal(err)
			}
			notifying, release := make(chan struct{}), make(chan struct{})
			reconciled := make(chan error, 1)
			go func() {
				reconciled <- reconcile(t.Context(), s.db, func(context.Context, database.ShardReelTask) error {
					close(notifying)
					<-release
					return nil
				})
			}()
			<-notifying
			// An unrelated download must hand off without waiting for a slow
			// hook or notification; the real worker uses a bounded DB context.
			other := database.ShardReelTask{ID: action + "-download", Status: "running"}
			if err := db.Create(&other).Error; err != nil {
				close(release)
				<-reconciled
				t.Fatal(err)
			}
			handoffCtx, cancelHandoff := context.WithTimeout(t.Context(), time.Second)
			handedOff, handoffErr := FinishDownload(handoffCtx, other.ID, taskresult.Summary{}, nil)
			cancelHandoff()
			if !handedOff || handoffErr != nil {
				close(release)
				<-reconciled
				t.Fatalf("notification blocked an unrelated download: handoff=%v err=%v", handedOff, handoffErr)
			}
			if err := db.Delete(&other).Error; err != nil {
				close(release)
				<-reconciled
				t.Fatal(err)
			}
			requested := make(chan error, 1)
			go func() {
				if action == "retry" {
					requested <- Retry(t.Context(), row.TaskID)
				} else {
					_, err := RequestCancel(t.Context(), row.TaskID)
					requested <- err
				}
			}()
			// Deliver the earlier result while a new user action is in flight.
			var requestErr error
			requestFinished := false
			select {
			case requestErr = <-requested:
				requestFinished = true
			case <-time.After(100 * time.Millisecond):
			}
			close(release)
			if err := <-reconciled; err != nil {
				t.Fatal(err)
			}
			if !requestFinished {
				requestErr = <-requested
			}
			if requestErr != nil {
				t.Fatal(requestErr)
			}
			var group database.ShardReelTask
			if err := s.db.First(&group, "id = ?", row.TaskID).Error; err != nil {
				t.Fatal(err)
			}
			wantStatus := "running"
			if action == "cancel" {
				wantStatus = "cancelling"
			}
			if group.Notified || group.HookDone || group.Status != wantStatus || group.ResultVersion != 1 {
				t.Fatalf("earlier completion overwrote the new %s lifecycle: %+v", action, group)
			}
		})
	}
}

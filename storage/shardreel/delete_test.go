package shardreel

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/krau/SaveAny-Bot/database"
)

func finishDeleteFixture(t *testing.T, s *Store, id, status string) database.ShardReelFile {
	t.Helper()
	file := stage(t, s, []byte("retained payload"), id)
	if err := s.update(t.Context(), file.ID, map[string]any{"status": status}); err != nil {
		t.Fatal(err)
	}
	groupStatus := "completed"
	if status == "failed" {
		groupStatus = "failed"
	}
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", id).Updates(map[string]any{"status": groupStatus, "download_done": true, "notified": true}).Error; err != nil {
		t.Fatal(err)
	}
	return rowAt(t, s, file.ID)
}

func assertDeleteRecords(t *testing.T, s *Store, id string, want int64) {
	t.Helper()
	var groups, files int64
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", id).Count(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.ShardReelFile{}).Where("task_id = ?", id).Count(&files).Error; err != nil {
		t.Fatal(err)
	}
	if groups != want || files != want {
		t.Fatalf("history lost or retained unexpectedly: groups=%d files=%d want=%d", groups, files, want)
	}
}

func TestDeleteCompletedUploadOnlyCleansPayloadAndAcknowledgesReceipt(t *testing.T) {
	var acks atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/storage/uploads/complete_file/ack" {
			t.Errorf("deletion changed saved remote data: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
			return
		}
		acks.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer remote.Close()
	s := fixture(t, remote.URL)
	file := finishDeleteFixture(t, s, "complete", "succeeded")
	if err := s.update(t.Context(), file.ID, map[string]any{"remote_started": true}); err != nil {
		t.Fatal(err)
	}
	other := finishDeleteFixture(t, s, "other", "failed")
	found, err := deleteTask(t.Context(), s.db, "complete", map[string]*Store{s.Name(): s})
	if err != nil || !found || acks.Load() != 1 {
		t.Fatalf("delete: found=%v err=%v acks=%d", found, err, acks.Load())
	}
	assertDeleteRecords(t, s, "complete", 0)
	assertDeleteRecords(t, s, "other", 1)
	if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, file.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged data retained: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, other.ID, "payload")); err != nil {
		t.Fatal("another task payload changed", err)
	}
}

func TestDeleteFailedUploadPreservesAConcurrentRemoteSuccess(t *testing.T) {
	var receipt atomic.Value
	var acknowledgements atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "DELETE" && r.URL.Path == "/v1/storage/uploads/saved_file":
			// The remote completed after the last failed observation. Cancelling
			// its transfer must return that receipt, never delete the saved file.
			if err := json.NewEncoder(w).Encode(receipt.Load()); err != nil {
				t.Error(err)
			}
		case r.Method == "POST" && r.URL.Path == "/v1/storage/uploads/saved_file/ack":
			acknowledgements.Add(1)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Errorf("unexpected saved-file mutation: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer remote.Close()
	s := fixture(t, remote.URL)
	file := finishDeleteFixture(t, s, "saved", "failed")
	if err := s.update(t.Context(), file.ID, map[string]any{"remote_started": true}); err != nil {
		t.Fatal(err)
	}
	receipt.Store(remoteResult{Status: "succeeded", Path: file.Path, Size: file.Size, SHA256: file.SHA256})
	if _, err := deleteTask(t.Context(), s.db, file.TaskID, map[string]*Store{s.Name(): s}); err != nil {
		t.Fatal(err)
	}
	assertDeleteRecords(t, s, file.TaskID, 0)
	if acknowledgements.Load() != 1 {
		t.Fatal("confirmed success receipt not acknowledged")
	}
}

func TestDeleteFailedUploadWaitsForRemoteAndBlocksRetry(t *testing.T) {
	for _, response := range []string{"unavailable", "cancelling", "ack_failed"} {
		t.Run(response, func(t *testing.T) {
			var allow atomic.Bool
			entered, release := make(chan struct{}), make(chan struct{})
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					if !allow.Load() {
						close(entered)
						<-release
						if response == "unavailable" {
							w.WriteHeader(503)
							return
						}
						if response == "cancelling" {
							_, _ = w.Write([]byte(`{"status":"cancelling"}`))
							return
						}
					}
					_, _ = w.Write([]byte(`{"status":"cancelled"}`))
					return
				}
				if r.Method == "POST" && r.URL.Path == "/v1/storage/uploads/failed_file/ack" {
					if !allow.Load() && response == "ack_failed" {
						w.WriteHeader(503)
						return
					}
					_, _ = w.Write([]byte(`{"ok":true}`))
					return
				}
				t.Errorf("unexpected upload or deletion: %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer remote.Close()
			s := fixture(t, remote.URL)
			file := finishDeleteFixture(t, s, "failed", "failed")
			if err := s.update(t.Context(), file.ID, map[string]any{"remote_started": true}); err != nil {
				t.Fatal(err)
			}
			result := make(chan error, 1)
			go func() {
				_, err := deleteTask(t.Context(), s.db, "failed", map[string]*Store{s.Name(): s})
				result <- err
			}()
			<-entered
			retryErr := retryTask(t.Context(), s.db, "failed")
			close(release)
			deleteErr := <-result
			if retryErr == nil || deleteErr == nil {
				t.Fatalf("cleanup raced with retry or discarded receipt: retry=%v delete=%v", retryErr, deleteErr)
			}
			assertDeleteRecords(t, s, "failed", 1)
			if response != "ack_failed" {
				if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, file.ID, "payload")); err != nil {
					t.Fatal("unconfirmed cancellation lost payload", err)
				}
			}
			allow.Store(true)
			if _, err := deleteTask(t.Context(), s.db, "failed", map[string]*Store{s.Name(): s}); err != nil {
				t.Fatal(err)
			}
			assertDeleteRecords(t, s, "failed", 0)
		})
	}
}

func TestDeleteRejectsActiveOrUndeliveredUploads(t *testing.T) {
	for _, state := range []string{"running", "queued", "cancelling", "not_notified", "not_downloaded", "active_file"} {
		t.Run(state, func(t *testing.T) {
			s := fixture(t, "http://unused.invalid")
			file := finishDeleteFixture(t, s, state, "failed")
			values := map[string]any{"status": state}
			switch state {
			case "not_notified":
				values = map[string]any{"notified": false}
			case "not_downloaded":
				values = map[string]any{"download_done": false}
			case "active_file":
				values = map[string]any{"status": "failed"}
				if err := s.update(t.Context(), file.ID, map[string]any{"status": "retry_wait"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", state).Updates(values).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := deleteTask(t.Context(), s.db, state, map[string]*Store{s.Name(): s}); !errors.Is(err, ErrTaskNotFinished) {
				t.Fatalf("active task accepted: %v", err)
			}
			assertDeleteRecords(t, s, state, 1)
			var group database.ShardReelTask
			if err := s.db.First(&group, "id = ?", state).Error; err != nil || group.CancelRequested {
				t.Fatalf("rejected deletion changed cancellation: %+v %v", group, err)
			}
		})
	}
}

func TestDeleteCleanupFailureRetainsHistoryAndCanBeRetried(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	file := finishDeleteFixture(t, s, "cleanup", "failed")
	unexpected := filepath.Join(s.cfg.SpoolPath, file.ID, "keep")
	if err := os.WriteFile(unexpected, []byte("not a payload"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteTask(t.Context(), s.db, "cleanup", map[string]*Store{s.Name(): s}); err == nil {
		t.Fatal("filesystem failure erased history")
	}
	assertDeleteRecords(t, s, "cleanup", 1)
	if row := rowAt(t, s, file.ID); row.LocalClean || row.CleanupError == "" {
		t.Fatalf("cleanup failure not retained: %+v", row)
	}
	if err := os.Remove(unexpected); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteTask(t.Context(), s.db, "cleanup", map[string]*Store{s.Name(): s}); err != nil {
		t.Fatal(err)
	}
	assertDeleteRecords(t, s, "cleanup", 0)
}

func TestDeleteCleanHistoryNeedsNoConfiguredStorage(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	file := finishDeleteFixture(t, s, "clean", "succeeded")
	if err := s.clean(t.Context(), &file); err != nil {
		t.Fatal(err)
	}
	if _, err := deleteTask(t.Context(), s.db, "clean", nil); err != nil {
		t.Fatal(err)
	}
	assertDeleteRecords(t, s, "clean", 0)
	if found, err := deleteTask(t.Context(), s.db, "missing", nil); found || err != nil {
		t.Fatalf("missing task: %v %v", found, err)
	}
}

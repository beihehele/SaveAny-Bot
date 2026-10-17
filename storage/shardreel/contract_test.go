package shardreel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

func TestInitializationAndCompletionObserveRemoteState(t *testing.T) {
	for _, endpoint := range []string{"initialize", "complete"} {
		for _, state := range []string{"cancelled", "skipped", "succeeded", "processing", "cancelling"} {
			t.Run(endpoint+"/"+state, func(t *testing.T) {
				puts := 0
				remote := remoteResult{Status: state}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					result := remote
					switch {
					case r.Method == "GET":
						result.Status = "expired"
					case r.Method == "PUT":
						puts++
						if endpoint == "initialize" {
							t.Error("uploaded after receiving remote state")
						}
						if err := json.NewEncoder(w).Encode(map[string]int64{"received": remote.Size}); err != nil {
							t.Error(err)
						}
						return
					case r.Method == "POST" && r.URL.Path == "/v1/storage/uploads":
						if endpoint == "complete" {
							result.Status = "uploading"
						}
					case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/complete"):
					default:
						t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					}
					if err := json.NewEncoder(w).Encode(result); err != nil {
						t.Error(err)
					}
				}))
				defer server.Close()
				s := fixture(t, server.URL)
				row := stage(t, s, []byte("original bytes"), "receipt")
				remote.Size, remote.SHA256 = row.Size, row.SHA256
				if err := s.update(t.Context(), row.ID, map[string]any{"remote_started": true}); err != nil {
					t.Fatal(err)
				}
				if err := s.step(t.Context(), true); err != nil {
					t.Fatal(err)
				}
				current := rowAt(t, s, row.ID)
				terminal := state == "succeeded" || state == "skipped" || state == "cancelled"
				if current.Status != state || current.LocalClean != terminal || (endpoint == "complete" && puts != 1) {
					t.Fatalf("state=%s clean=%v puts=%d error=%s", current.Status, current.LocalClean, puts, current.Error)
				}
			})
		}
	}
}

func TestTransferStateConflictRequiresReceiptBeforeCleanup(t *testing.T) {
	for _, endpoint := range []string{"chunk", "complete"} {
		for _, code := range []string{"TRANSFER_NOT_WRITABLE", "TRANSFER_CANCELLED", "REQUEST_MISMATCH"} {
			t.Run(endpoint+"/"+code, func(t *testing.T) {
				cancelled, puts := false, 0
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.Method == "GET" && cancelled:
						if err := json.NewEncoder(w).Encode(remoteResult{Status: "cancelled"}); err != nil {
							t.Error(err)
						}
					case r.Method == "POST" && r.URL.Path == "/v1/storage/uploads":
						if err := json.NewEncoder(w).Encode(remoteResult{Status: "uploading"}); err != nil {
							t.Error(err)
						}
					case r.Method == "PUT" && endpoint == "complete":
						puts++
						if err := json.NewEncoder(w).Encode(map[string]int{"received": 7}); err != nil {
							t.Error(err)
						}
					default:
						cancelled = true
						if r.Method == "PUT" {
							puts++
						}
						w.WriteHeader(http.StatusConflict)
						if err := json.NewEncoder(w).Encode(map[string]any{"detail": map[string]string{"code": code}}); err != nil {
							t.Error(err)
						}
					}
				}))
				defer server.Close()
				s := fixture(t, server.URL)
				row := stage(t, s, []byte("payload"), "state-conflict")
				if err := s.step(t.Context(), true); err != nil {
					t.Fatal(err)
				}
				current := rowAt(t, s, row.ID)
				if current.LocalClean || puts != 1 {
					t.Fatalf("premature cleanup or repeated upload: %+v puts=%d", current, puts)
				}
				if code == "REQUEST_MISMATCH" {
					if current.Status != "failed" {
						t.Fatalf("unrelated conflict was ignored: %+v", current)
					}
					return
				}
				if current.Status != "processing" {
					t.Fatalf("state conflict: %+v", current)
				}
				due(t, s, row.ID)
				if err := s.step(t.Context(), true); err != nil {
					t.Fatal(err)
				}
				if current = rowAt(t, s, row.ID); current.Status != "cancelled" || !current.LocalClean || puts != 1 {
					t.Fatalf("receipt: %+v puts=%d", current, puts)
				}
			})
		}
	}
}

func TestAcceptedRetryRemainsObservedAtBudget(t *testing.T) {
	state, retries, unavailable := "failed", 0, false
	remote := remoteResult{Status: state, CanRetry: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/retry") {
			retries++
			state = "processing"
		} else if unavailable {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		remote.Status = state
		if err := json.NewEncoder(w).Encode(remote); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("original bytes"), "retry")
	remote.Size, remote.SHA256 = row.Size, row.SHA256
	if err := s.update(t.Context(), row.ID, map[string]any{"remote_started": true, "attempts": len(retryDelays) - 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "retry_wait" || current.Attempts != len(retryDelays) || retries != 0 || !current.NextAttemptAt.After(time.Now()) {
		t.Fatalf("retry must wait before starting: %+v retries=%d", current, retries)
	}
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "processing" || current.Attempts != len(retryDelays) || current.LocalClean {
		t.Fatalf("accepted retry: %+v", current)
	}
	unavailable = true
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "processing" || current.LocalClean {
		t.Fatalf("temporary read failure abandoned remote task: %+v", current)
	}
	unavailable, state = false, "succeeded"
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "succeeded" || !current.LocalClean || retries != 1 {
		t.Fatalf("completion: %+v retries=%d", current, retries)
	}
}

func TestRemoteFailureStopsBeforeStartingAnotherRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		canRetry bool
		attempts int
	}{
		{"exhausted", true, len(retryDelays)},
		{"not-retryable", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Errorf("unexpected action %s", r.Method)
				}
				if err := json.NewEncoder(w).Encode(remoteResult{Status: "failed", CanRetry: tc.canRetry, ErrorCode: "SOURCE_MISSING"}); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			s := fixture(t, server.URL)
			row := stage(t, s, []byte("payload"), "failure")
			if err := s.update(t.Context(), row.ID, map[string]any{"remote_started": true, "attempts": tc.attempts}); err != nil {
				t.Fatal(err)
			}
			if err := s.step(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			if current := rowAt(t, s, row.ID); current.Status != "failed" || current.LocalClean {
				t.Fatalf("%+v", current)
			}
		})
	}
}

func TestLostRetryReplyAtBudgetIsReconciled(t *testing.T) {
	remote := remoteResult{Status: "failed", CanRetry: true}
	retries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/retry") {
			retries++
			remote.Status = "processing"
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		if err := json.NewEncoder(w).Encode(remote); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("original bytes"), "lost-retry")
	remote.Size, remote.SHA256 = row.Size, row.SHA256
	if err := s.update(t.Context(), row.ID, map[string]any{"remote_started": true, "status": "retry_wait", "attempts": len(retryDelays)}); err != nil {
		t.Fatal(err)
	}
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if row := rowAt(t, s, row.ID); row.Status != "processing" || row.LocalClean {
		t.Fatalf("lost reply: %+v", row)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	remote.Status = "succeeded"
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if row := rowAt(t, s, row.ID); row.Status != "succeeded" || !row.LocalClean || retries != 1 {
		t.Fatalf("completion: %+v retries=%d", row, retries)
	}
}

func TestSkipPolicyIsDurableAndSentToReceiver(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/storage/uploads" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Conflict string `json:"on_conflict"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Conflict != "skip" {
			t.Errorf("conflict=%s", body.Conflict)
		}
		if err := json.NewEncoder(w).Encode(remoteResult{Status: "skipped", Path: "/keep.bin"}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	s := fixture(t, server.URL)
	ctx := storagetypes.WithSkipExisting(storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: "skip", FileID: "file"}))
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(3))
	if err := s.Save(ctx, bytes.NewBufferString("new"), "/keep.bin"); !errors.Is(err, storagetypes.ErrSaveQueued) {
		t.Fatal(err)
	}
	if current := rowAt(t, s, "skip_file"); current.Conflict != "skip" {
		t.Fatalf("lost skip policy: %+v", current)
	}
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, "skip_file"); current.Status != "skipped" || !current.LocalClean {
		t.Fatalf("%+v", current)
	}
}

func TestRemotePathNameBoundaries(t *testing.T) {
	for _, tc := range []struct {
		path  string
		valid bool
	}{
		{"/" + strings.Repeat("中", 512) + "/a.bin", true},
		{"/" + strings.Repeat("a", 513) + "/a.bin", false},
		{"/" + strings.Repeat("中", 513), false},
		{"/a\tb/file.bin", false},
		{"/a\x1fb/file.bin", false},
		{"/a\x00b/file.bin", false},
		{"/bad\xff/file.bin", false},
	} {
		if _, err := ValidatePath(tc.path); (err == nil) != tc.valid {
			t.Errorf("path %q: %v", tc.path, err)
		}
	}
}

func TestSkipDistinguishesCompletedFileFromOccupiedPath(t *testing.T) {
	for _, tc := range []struct {
		name, status                      string
		dir, missing, wantSkip, wantError bool
	}{
		{"ready", "ready", false, false, true, false},
		{"pending", "init", false, false, false, true},
		{"failed", "failed", false, false, false, true},
		{"directory", "ready", true, false, false, true},
		{"missing", "", false, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body any = map[string]any{"is_dir": tc.dir, "status": tc.status}
				if tc.missing {
					w.WriteHeader(404)
					body = map[string]any{"detail": map[string]string{"code": "PATH_NOT_FOUND"}}
				}
				if err := json.NewEncoder(w).Encode(body); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			s := fixture(t, server.URL)
			if exists, err := s.Exists(t.Context(), "/file.bin"); err != nil || exists == tc.missing {
				t.Fatalf("exists=%v err=%v", exists, err)
			}
			if skip, err := s.CanSkip(t.Context(), "/file.bin"); skip != tc.wantSkip || (err != nil) != tc.wantError {
				t.Fatalf("skip=%v err=%v", skip, err)
			}
		})
	}
}

func TestLiveStorageSkipAndCancelledInitialization(t *testing.T) {
	endpoint := os.Getenv("SAVEANY_SHARDREEL_TEST_URL")
	if endpoint == "" {
		t.Skip("set SAVEANY_SHARDREEL_TEST_URL for isolated cross-project HTTP verification")
	}
	s := fixture(t, endpoint)
	s.client.Timeout = 30 * time.Second
	s.cfg.APIKey = os.Getenv("SAVEANY_SHARDREEL_TEST_KEY")
	prefix := strconv.FormatInt(time.Now().UnixNano(), 10)
	target := "/contract-" + prefix + "/file.bin"
	stageFile := func(id, data string, skip bool) string {
		t.Helper()
		id = prefix + "-" + id
		ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: id, FileID: "file"})
		ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(len(data)))
		if skip {
			ctx = storagetypes.WithSkipExisting(ctx)
		}
		if err := s.Save(ctx, strings.NewReader(data), target); !errors.Is(err, storagetypes.ErrSaveQueued) {
			t.Fatal(err)
		}
		return id + "_file"
	}
	originalID := stageFile("original", "original", false)
	deadline := time.Now().Add(time.Minute)
	for rowAt(t, s, originalID).Status != "succeeded" && time.Now().Before(deadline) {
		due(t, s, originalID)
		if err := s.step(t.Context(), true); err != nil {
			t.Fatal(err)
		}
		if rowAt(t, s, originalID).Status == "failed" {
			t.Fatal(rowAt(t, s, originalID).Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rowAt(t, s, originalID).Status != "succeeded" {
		t.Fatal("original ingest timed out")
	}
	skippedID := stageFile("skipped", "different bytes", true)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if row := rowAt(t, s, skippedID); row.Status != "skipped" || !row.LocalClean {
		t.Fatalf("skip: %+v", row)
	}
	reader, size, err := s.OpenFile(t.Context(), target)
	if err != nil {
		t.Fatal(err)
	}
	var original bytes.Buffer
	_, readErr := original.ReadFrom(reader)
	if err := errors.Join(readErr, reader.Close()); err != nil {
		t.Fatal(err)
	}
	if size != 8 || original.String() != "original" {
		t.Fatal("skip replaced original bytes")
	}

	target += ".cancel"
	cancelledID := stageFile("cancelled", "cancelled bytes", false)
	row := rowAt(t, s, cancelledID)
	body := map[string]any{"request_id": row.ID, "path": row.Path, "size": row.Size, "sha256": row.SHA256, "on_conflict": row.Conflict}
	var remote remoteResult
	if err := s.call(t.Context(), "POST", "/uploads", body, nil, &remote); err != nil {
		t.Fatal(err)
	}
	if err := s.call(t.Context(), "DELETE", "/uploads/"+row.ID, nil, nil, &remote); err != nil {
		t.Fatal(err)
	}
	if remote.Status != "cancelled" {
		t.Fatalf("cancel: %+v", remote)
	}
	// The local sender lost the initialization reply and must observe the
	// receiver's durable cancellation when it initializes the same request.
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if row := rowAt(t, s, cancelledID); row.Status != "cancelled" || !row.LocalClean {
		t.Fatalf("cancelled initialization: %+v", row)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestLiveStorageCancellationDuringWrite(t *testing.T) {
	endpoint := os.Getenv("SAVEANY_SHARDREEL_TEST_URL")
	if endpoint == "" {
		t.Skip("set SAVEANY_SHARDREEL_TEST_URL for isolated cross-project HTTP verification")
	}
	for _, phase := range []string{"chunk", "complete"} {
		t.Run(phase, func(t *testing.T) {
			s := fixture(t, endpoint)
			s.client.Timeout = 30 * time.Second
			s.cfg.APIKey = os.Getenv("SAVEANY_SHARDREEL_TEST_KEY")
			row := stage(t, s, []byte("cancel before write"), "cancel-"+phase+"-"+strconv.FormatInt(time.Now().UnixNano(), 10))
			cancelled := false
			s.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if !cancelled && ((phase == "chunk" && r.Method == "PUT") || (phase == "complete" && strings.HasSuffix(r.URL.Path, "/complete"))) {
					var result remoteResult
					if err := s.call(r.Context(), "DELETE", "/uploads/"+row.ID, nil, nil, &result); err != nil {
						return nil, err
					}
					if result.Status != "cancelled" {
						return nil, errors.New("remote did not confirm cancellation")
					}
					cancelled = true
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			if err := s.step(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			if current := rowAt(t, s, row.ID); !cancelled || current.Status != "processing" || current.LocalClean {
				t.Fatalf("before receipt: %+v", current)
			}
			due(t, s, row.ID)
			if err := s.step(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			if current := rowAt(t, s, row.ID); current.Status != "cancelled" || !current.LocalClean {
				t.Fatalf("after receipt: %+v", current)
			}
		})
	}
}

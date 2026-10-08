package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/storage"
)

type aria2TestTransport func(*http.Request) (*http.Response, error)

func (f aria2TestTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type aria2TestResponse struct {
	io.Reader
	afterClose func()
}

func (r aria2TestResponse) Close() error {
	if r.afterClose != nil {
		r.afterClose()
	}
	return nil
}

type aria2TestStorage struct{ storage.Storage }

func (aria2TestStorage) Name() string { return "aria2-test" }

func TestAria2FactoryHandsOffOrCleansUpDownloads(t *testing.T) {
	for _, tc := range []struct {
		name                                                                                      string
		cancelRequest, cancelService, completed, children, stopFails, childStopFails, resultFails bool
		wantMethods                                                                               []string
	}{
		{name: "accepted", wantMethods: []string{"aria2.addUri"}},
		{name: "request canceled after RPC", cancelRequest: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.removeDownloadResult"}},
		{name: "enqueue rejects canceled service", cancelService: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.removeDownloadResult"}},
		{name: "already completed", cancelRequest: true, completed: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.tellStatus", "aria2.removeDownloadResult"}},
		{name: "completed metadata with children", cancelRequest: true, completed: true, children: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.tellStatus", "aria2.forceRemove", "aria2.removeDownloadResult", "aria2.removeDownloadResult"}},
		{name: "failed child retains metadata", cancelRequest: true, completed: true, children: true, childStopFails: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.tellStatus", "aria2.forceRemove", "aria2.tellStatus"}},
		{name: "cleanup cannot stop or inspect", cancelRequest: true, stopFails: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.tellStatus"}},
		{name: "cleanup cannot remove result", cancelRequest: true, resultFails: true,
			wantMethods: []string{"aria2.addUri", "aria2.forceRemove", "aria2.removeDownloadResult"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configPath := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(configPath, []byte("workers=1\n[aria2]\nenable=true\nurl='http://aria2.invalid/jsonrpc'\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := config.Init(t.Context(), configPath); err != nil {
				t.Fatal(err)
			}
			core.Prepare()
			requestCtx, cancelRequest := context.WithCancel(t.Context())
			defer cancelRequest()
			var cleanupLog bytes.Buffer
			serviceCtx, cancelService := context.WithCancel(log.WithContext(t.Context(), log.New(&cleanupLog)))
			defer cancelService()
			oldTransport := http.DefaultTransport
			t.Cleanup(func() { http.DefaultTransport = oldTransport })
			const gid = "0123456789abcdef"
			const childGID = "abcdef0123456789"
			var methods []string
			var stopped, removed []string
			http.DefaultTransport = aria2TestTransport(func(req *http.Request) (*http.Response, error) {
				var call struct {
					ID     string            `json:"id"`
					Method string            `json:"method"`
					Params []json.RawMessage `json:"params"`
				}
				if err := json.NewDecoder(req.Body).Decode(&call); err != nil {
					return nil, err
				}
				methods = append(methods, call.Method)
				response := map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": gid}
				var afterClose func()
				if call.Method == "aria2.addUri" {
					afterClose = func() {
						if tc.cancelRequest {
							cancelRequest()
						}
						if tc.cancelService {
							cancelService()
						}
					}
				} else {
					if err := req.Context().Err(); err != nil {
						t.Errorf("cleanup inherited cancellation: %v", err)
					}
					deadline, ok := req.Context().Deadline()
					if !ok || time.Until(deadline) > 5*time.Second {
						t.Error("cleanup lacks bounded deadline")
					}
					var target string
					if len(call.Params) == 0 {
						return nil, errors.New("missing cleanup GID")
					}
					if err := json.Unmarshal(call.Params[0], &target); err != nil {
						return nil, err
					}
					if target != gid && (!tc.children || target != childGID) {
						t.Errorf("cleanup targets unrelated GID %q", target)
					}
					switch call.Method {
					case "aria2.forceRemove":
						stopped = append(stopped, target)
						if tc.stopFails || tc.completed && target == gid || tc.childStopFails && target == childGID {
							delete(response, "result")
							response["error"] = map[string]any{"code": 1, "message": "cannot stop download"}
						}
					case "aria2.tellStatus":
						if tc.stopFails || tc.childStopFails && target == childGID {
							delete(response, "result")
							response["error"] = map[string]any{"code": 1, "message": "cannot inspect download"}
						} else {
							status := map[string]any{"gid": gid}
							if tc.children {
								status["followedBy"] = []string{childGID}
							}
							response["result"] = status
						}
					case "aria2.removeDownloadResult":
						removed = append(removed, target)
						response["result"] = "OK"
						if tc.resultFails {
							delete(response, "result")
							response["error"] = map[string]any{"code": 1, "message": "cannot remove result"}
						}
					default:
						t.Errorf("unexpected RPC %s", call.Method)
					}
				}
				data, err := json.Marshal(response)
				if err != nil {
					return nil, err
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), Body: aria2TestResponse{Reader: strings.NewReader(string(data)), afterClose: afterClose}}, nil
			})
			factory := NewTaskFactory(serviceCtx)
			factory.prepareCtx = requestCtx
			taskID := t.Name() + "-" + time.Now().Format(time.RFC3339Nano)
			// API records and queued contexts are cleaned even if an assertion fails.
			t.Cleanup(func() { DeleteTask(taskID); cancelService() })
			response, err := factory.createAria2Task(taskID, time.Now(), &CreateTaskRequest{Storage: "aria2-test",
				Params: json.RawMessage(`{"urls":["https://example.invalid/file"]}`)}, aria2TestStorage{})
			if tc.cancelRequest || tc.cancelService {
				if err == nil || response != nil {
					t.Fatal("rejected creation returned success")
				}
				if tc.cancelRequest && !errors.Is(err, context.Canceled) {
					t.Fatalf("original cancellation lost: %v", err)
				}
				if tc.stopFails || tc.childStopFails || tc.resultFails {
					if !strings.Contains(err.Error(), "failed to clean up") || !strings.Contains(err.Error(), gid) {
						t.Fatalf("cleanup failure or recovery GID hidden: %v", err)
					}
					if !strings.Contains(cleanupLog.String(), gid) {
						t.Fatal("disconnected client left no recovery GID in service logs")
					}
				}
				if _, ok := GetTask(taskID); ok {
					t.Fatal("rejected task retained API record")
				}
			} else {
				if err != nil || response == nil {
					t.Fatalf("accepted creation failed: %v", err)
				}
				cancelRequest()
				queued := false
				for _, info := range core.GetQueuedTasks(serviceCtx) {
					if info.ID == taskID {
						queued = !info.Cancelled
					}
				}
				if !queued {
					t.Fatal("request disconnect canceled an accepted task")
				}
			}
			if !reflect.DeepEqual(methods, tc.wantMethods) {
				t.Fatalf("RPC sequence = %v, want %v", methods, tc.wantMethods)
			}
			if tc.children {
				if !reflect.DeepEqual(stopped, []string{gid, childGID}) {
					t.Fatalf("stopped GIDs = %v, child ownership lost", stopped)
				}
				if tc.childStopFails {
					if len(removed) != 0 {
						t.Fatal("metadata removed before failed child could be recovered")
					}
				} else if !reflect.DeepEqual(removed, []string{childGID, gid}) {
					t.Fatalf("result cleanup order = %v, want child before metadata", removed)
				}
			}
		})
	}
}

package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type cancellationTask struct {
	id  string
	run func(context.Context) error
}

func (t cancellationTask) TaskID() string                    { return t.id }
func (t cancellationTask) Title() string                     { return t.id }
func (t cancellationTask) Type() tasktype.TaskType           { return tasktype.TaskTypeDirectlinks }
func (t cancellationTask) Execute(ctx context.Context) error { return t.run(ctx) }

// A separate process owns the real global queue, hooks, API store and webhook.
func TestCancellationDecisionEndToEnd(t *testing.T) {
	const helperEnv = "SAVEANY_CANCELLATION_TEST_PROCESS"
	if os.Getenv(helperEnv) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestCancellationDecisionEndToEnd$", "-test.count=1")
		command.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated cancellation integration: %v\n%s", err, output)
		}
		return
	}
	serviceCtx, stop := context.WithCancel(t.Context())
	defer stop()
	hooks := filepath.Join(t.TempDir(), "hooks.txt")
	text := "workers=1\n[hook.exec]\n"
	for _, name := range []string{"task_before_start", "task_success", "task_fail", "task_cancel"} {
		text += name + "=" + strconv.Quote(fmt.Sprintf("echo %s >> %q", name, filepath.ToSlash(hooks))) + "\n"
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(serviceCtx, configPath); err != nil {
		t.Fatal(err)
	}
	received := make(chan WebhookPayload, 10)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	core.Prepare()
	done := core.Run(serviceCtx)
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	internal := fmt.Errorf("engine forcibly closed: %w", context.Canceled)
	fileError := errors.New("connection EOF")
	for _, tc := range []struct {
		name                                      string
		err                                       error
		cancelDuringExecute, cancelDuringDelivery bool
		status                                    TaskStatus
	}{
		{name: "internal", err: internal, status: TaskStatusFailed},
		{name: "task-cancel", err: fileError, cancelDuringExecute: true, status: TaskStatusCancelled},
		{name: "nil-after-cancel", cancelDuringExecute: true, status: TaskStatusCompleted},
		{name: "late-cancel", err: fileError, cancelDuringDelivery: true, status: TaskStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(serviceCtx)
			defer cancel()
			info := RegisterTask(tc.name, "directlinks", "local", "", tc.name, server.URL)
			defer DeleteTask(tc.name)
			info.webhookContext = serviceCtx
			finished := make(chan taskevent.Event, 1)
			ctx = taskevent.WithSink(ctx, taskevent.SinkFunc(func(e taskevent.Event) {
				if e.Phase == taskevent.PhaseDone {
					// Even cancellation during delivery must not reinterpret the
					// decision made before the worker ran its terminal hook.
					if tc.cancelDuringDelivery {
						cancel()
					}
					finished <- e
				}
			}), info)
			task := cancellationTask{id: tc.name, run: func(context.Context) error {
				if tc.cancelDuringExecute {
					cancel()
				}
				return tc.err
			}}
			if err := core.AddTask(ctx, task); err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-finished:
				if e.Err != tc.err {
					t.Fatal("event lost original error")
				}
				if e.Outcome == taskresult.OutcomeUnknown {
					t.Fatal("worker omitted terminal outcome")
				}
			case <-time.After(20 * time.Second):
				t.Fatal("missing terminal event")
			}
			select {
			case payload := <-received:
				if payload.TaskID != tc.name || payload.Status != tc.status {
					t.Fatalf("webhook=%+v", payload)
				}
				rr := httptest.NewRecorder()
				NewHandlers(NewTaskFactory(serviceCtx)).GetTaskHandler(rr, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+tc.name, nil))
				var response TaskInfoResponse
				if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if rr.Code != http.StatusOK || response.Status != tc.status || response.Error != payload.Error {
					t.Fatalf("GET and webhook disagree: %+v %+v", response, payload)
				}
				if tc.status == TaskStatusFailed && response.Error != tc.err.Error() {
					t.Fatal("failure lost original reason")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("missing terminal webhook")
			}
		})
	}
	content, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	for hook, want := range map[string]int{"task_before_start": 4, "task_success": 1, "task_fail": 2, "task_cancel": 1} {
		if strings.Count(string(content), hook) != want {
			t.Fatalf("wrong hooks: %s", content)
		}
	}
	select {
	case extra := <-received:
		t.Fatalf("duplicate webhook: %+v", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

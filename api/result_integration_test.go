package api

import (
	"context"
	"encoding/json"
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
	"github.com/krau/SaveAny-Bot/storage"
)

// The API and queue registries are process-wide. A child test process exercises
// real queue shutdown without closing the shared queue used by other API tests.
func TestTransferResultPolicyEndToEnd(t *testing.T) {
	const helperEnv = "SAVEANY_TRANSFER_RESULT_TEST_PROCESS"
	if os.Getenv(helperEnv) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestTransferResultPolicyEndToEnd$", "-test.count=1")
		command.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated transfer integration: %v\n%s", err, output)
		}
		return
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source, target := t.TempDir(), t.TempDir()
	hooks := filepath.Join(t.TempDir(), "hooks.txt")
	text := "workers=1\nstream=true\n"
	for name, path := range map[string]string{"policy-source": source, "policy-target": target} {
		text += fmt.Sprintf("[[storages]]\nname=%q\ntype='local'\nenable=true\nbase_path=%q\n", name, filepath.ToSlash(path))
	}
	text += "[hook.exec]\n"
	for _, name := range []string{"task_before_start", "task_success", "task_fail", "task_cancel"} {
		text += name + "=" + strconv.Quote(fmt.Sprintf("echo %s >> %q", name, filepath.ToSlash(hooks))) + "\n"
	}
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(ctx, configPath); err != nil {
		t.Fatal(err)
	}
	storage.LoadStorages(ctx)
	core.Prepare()
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
	factory := NewTaskFactory(ctx)
	handlers := NewHandlers(factory)
	type expected struct {
		policy        string
		status        TaskStatus
		saved, failed int
		dir           string
	}
	cases := []expected{{policy: "", status: TaskStatusCompleted, saved: 1, failed: 1, dir: "omitted"}, {policy: "legacy", status: TaskStatusCompleted, saved: 1, failed: 1, dir: "legacy"}, {policy: "strict", status: TaskStatusFailed, saved: 1, failed: 1, dir: "strict"}, {policy: "strict", status: TaskStatusFailed, failed: 2, dir: "all-failed"}}
	wants := make(map[string]expected)
	for _, tc := range cases {
		if err := os.MkdirAll(filepath.Join(source, tc.dir), 0700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"good.bin", "bad.bin"} {
			if err := os.WriteFile(filepath.Join(source, tc.dir, name), []byte(name), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(target, tc.dir, "bad.bin"), 0700); err != nil {
			t.Fatal(err)
		}
		if tc.saved == 0 {
			if err := os.MkdirAll(filepath.Join(target, tc.dir, "good.bin"), 0700); err != nil {
				t.Fatal(err)
			}
		}
		request := map[string]any{"type": "transfer", "storage": "policy-target", "webhook": server.URL, "params": map[string]any{"source_storage": "policy-source", "source_path": tc.dir, "target_storage": "policy-target", "target_path": tc.dir}}
		if tc.policy != "" {
			request["result_policy"] = tc.policy
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		rr := httptest.NewRecorder()
		handlers.CreateTaskHandler(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(string(body))))
		if rr.Code != http.StatusCreated {
			t.Fatalf("creation %s: %d %s", tc.dir, rr.Code, rr.Body.String())
		}
		var created CreateTaskResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &created); err != nil {
			t.Fatal(err)
		}
		if string(created.ResultPolicy) != tc.policy {
			t.Fatal("request-local policy was lost")
		}
		wants[created.TaskID] = tc
		defer DeleteTask(created.TaskID)
		// Listing has already captured both files. Removing a fixture before
		// workers start makes the read fail without changing storage behavior.
		if err := os.Remove(filepath.Join(source, tc.dir, "bad.bin")); err != nil {
			t.Fatal(err)
		}
		if tc.saved == 0 {
			if err := os.Remove(filepath.Join(source, tc.dir, "good.bin")); err != nil {
				t.Fatal(err)
			}
		}
	}
	if factory.resultPolicy != "" {
		t.Fatal("shared factory retained a request policy")
	}
	done := core.Run(ctx)
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	seen := make(map[string]bool)
	for range cases {
		var payload WebhookPayload
		select {
		case payload = <-received:
		case <-time.After(10 * time.Second):
			t.Fatal("missing terminal webhook")
		}
		tc, ok := wants[payload.TaskID]
		if !ok || seen[payload.TaskID] {
			t.Fatal("unexpected/duplicate notification")
		}
		seen[payload.TaskID] = true
		if payload.Status != tc.status || string(payload.ResultPolicy) != tc.policy {
			t.Fatalf("wrong webhook: %+v", payload)
		}
		rr := httptest.NewRecorder()
		handlers.GetTaskHandler(rr, httptest.NewRequest(http.MethodGet, "/api/v1/tasks/"+payload.TaskID, nil))
		var response TaskInfoResponse
		if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Status != tc.status || string(response.ResultPolicy) != tc.policy {
			t.Fatal("GET and webhook disagree")
		}
		if tc.policy == "" {
			if payload.ResultSummary != nil || response.ResultSummary != nil || strings.Contains(rr.Body.String(), "result_") {
				t.Fatal("omitted policy changed default payload")
			}
		} else {
			if response.ResultSummary == nil || payload.ResultSummary == nil || *response.ResultSummary != *payload.ResultSummary || response.ResultSummary.Total != 2 || response.ResultSummary.Succeeded != tc.saved || response.ResultSummary.Failed != tc.failed {
				t.Fatalf("incorrect counts: %+v %+v", response, payload)
			}
		}
		if tc.saved > 0 {
			bytes, err := os.ReadFile(filepath.Join(target, tc.dir, "good.bin"))
			if err != nil || string(bytes) != "good.bin" {
				t.Fatal("successful file was changed or rolled back")
			}
		}
		if info, err := os.Stat(filepath.Join(target, tc.dir, "bad.bin")); err != nil || !info.IsDir() {
			t.Fatal("existing failed target was damaged")
		}
	}
	content, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	for hook, want := range map[string]int{"task_before_start": 4, "task_success": 2, "task_fail": 2, "task_cancel": 0} {
		if strings.Count(string(content), hook) != want {
			t.Fatalf("incorrect hooks: %s", content)
		}
	}
}

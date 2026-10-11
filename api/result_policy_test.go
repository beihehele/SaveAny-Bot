package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func TestResultPolicyValidationPrecedesStorageAndTaskCreation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		typ    tasktype.TaskType
		policy string
		want   string
	}{
		{name: "unknown policy", typ: tasktype.TaskTypeTgfiles, policy: "typo", want: "result_policy is not supported"},
		{name: "tgfiles strict stays unavailable", typ: tasktype.TaskTypeTgfiles, policy: "strict", want: "result_policy is not supported"},
		{name: "other types reject explicit legacy", typ: tasktype.TaskTypeTgfiles, policy: "legacy", want: "result_policy is not supported"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			factory := NewTaskFactory(t.Context())
			req := &CreateTaskRequest{Type: tc.typ, Storage: "missing", ResultPolicy: tc.policy, Params: json.RawMessage(`{}`)}
			if _, err := factory.CreateTask(req); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validation error=%v", err)
			}
			body, err := json.Marshal(req)
			if err != nil {
				t.Fatal(err)
			}
			rr := httptest.NewRecorder()
			NewHandlers(factory).CreateTaskHandler(rr, httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(string(body))))
			if rr.Code != http.StatusBadRequest || !strings.Contains(rr.Body.String(), tc.want) {
				t.Fatalf("response=%d %s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestResultSummariesAreDetachedAndTerminalDecisionsStayFixed(t *testing.T) {
	counts := taskresult.Counts{Total: 2, Succeeded: 1, Failed: 1}
	info := &TaskProgressInfo{Status: TaskStatusRunning}
	input := counts
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, ResultSummary: &input})
	input.Total = 0
	response := convertTaskProgressToResponse(info)
	if response.Status != TaskStatusCompleted {
		t.Fatal("API changed the worker's terminal decision")
	}
	if response.ResultSummary == nil || *response.ResultSummary != counts {
		t.Fatal("event result aliased or lost")
	}
	response.ResultSummary.Failed = 0
	if *convertTaskProgressToResponse(info).ResultSummary != counts {
		t.Fatal("response mutated stored result")
	}

	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Err: context.Canceled, ResultSummary: &taskresult.Counts{Total: 2, Cancelled: 2}})
	if convertTaskProgressToResponse(info).Status != TaskStatusCompleted {
		t.Fatal("late event rewrote completed task")
	}
}

func TestUnavailableAndInvalidCountsAreOmitted(t *testing.T) {
	for _, counts := range []*taskresult.Counts{nil, {Total: 2, Succeeded: 3}, {Total: 1, Failed: -1}} {
		info := &TaskProgressInfo{Status: TaskStatusRunning}
		info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, ResultSummary: counts})
		response := convertTaskProgressToResponse(info)
		data, err := json.Marshal(response)
		if err != nil {
			t.Fatal(err)
		}
		if response.Status != TaskStatusCompleted || strings.Contains(string(data), "result_") {
			t.Fatalf("unavailable results altered response: %s", data)
		}
	}
}

func TestCancelledTaskAcceptsFinalCountsWithoutAnotherWebhook(t *testing.T) {
	info := &TaskProgressInfo{Status: TaskStatusRunning, TaskID: "cancelled-result", Webhook: "unused"}
	info.mu.Lock()
	info.Status = TaskStatusCancelled
	info.UpdatedAt = time.Now().Add(-time.Minute)
	first := info.notificationLocked()
	info.mu.Unlock()
	if first == nil || first.ResultSummary != nil {
		t.Fatal("early cancellation fabricated execution results")
	}
	counts := taskresult.Counts{Total: 2, Succeeded: 1, Cancelled: 1}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Err: context.Canceled, ResultSummary: &counts})
	response := convertTaskProgressToResponse(info)
	if response.Status != TaskStatusCancelled || response.ResultSummary == nil || *response.ResultSummary != counts || time.Since(response.UpdatedAt) > time.Minute {
		t.Fatal("final cancellation counts were not published")
	}
	info.mu.Lock()
	second := info.notificationLocked()
	info.mu.Unlock()
	if second != nil {
		t.Fatal("result enrichment generated another webhook")
	}
	if first.ResultSummary != nil {
		t.Fatal("queued webhook payload was mutated after creation")
	}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, ResultSummary: &taskresult.Counts{Total: 2, Succeeded: 2}})
	if *convertTaskProgressToResponse(info).ResultSummary != counts {
		t.Fatal("duplicate terminal event rewrote cancellation counts")
	}
}

func TestConcurrentResultSnapshotsRemainConsistent(t *testing.T) {
	info := &TaskProgressInfo{Status: TaskStatusRunning}
	counts := taskresult.Counts{Total: 2, Succeeded: 1, Failed: 1}
	var wg sync.WaitGroup
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, ResultSummary: &counts})
			response := convertTaskProgressToResponse(info)
			if response.ResultSummary == nil || response.Status != TaskStatusCompleted || *response.ResultSummary != counts {
				t.Error("inconsistent response")
			}
			response.ResultSummary.Total = 0
		}()
	}
	wg.Wait()
}

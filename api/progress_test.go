package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

func TestTerminalProgressDoesNotRegress(t *testing.T) {
	for _, status := range []TaskStatus{TaskStatusCompleted, TaskStatusFailed, TaskStatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			info := &TaskProgressInfo{Status: TaskStatusQueued}
			info.UpdateStatus(status)
			_, _, _, _, _, _, _, updated := info.snapshot()
			info.Emit(taskevent.Event{Phase: taskevent.PhaseStart})
			info.Emit(taskevent.Event{Phase: taskevent.PhaseProgress, DownloadedBytes: 99})
			info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Err: context.Canceled})
			info.SetError("late error")
			info.UpdateStatus(TaskStatusRunning)
			got, _, bytes, _, _, _, errMsg, last := info.snapshot()
			if got != status || bytes != 0 || errMsg != "" || !last.Equal(updated) {
				t.Fatalf("terminal changed: %s bytes=%d error=%q", got, bytes, errMsg)
			}
		})
	}
	info := &TaskProgressInfo{Status: TaskStatusRunning}
	info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Err: fmt.Errorf("interrupted: %w", context.Canceled)})
	if got, _, _, _, _, _, _, _ := info.snapshot(); got != TaskStatusCancelled {
		t.Fatalf("cancellation became %s", got)
	}
}

func TestServiceCancellationStopsWebhookRequestAndRetryWait(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.Copy(io.Discard, r.Body); err != nil {
			t.Error(err)
		}
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	info := RegisterTask(t.Name(), "tgfiles", "store", "", "test", server.URL)
	t.Cleanup(func() { DeleteTask(t.Name()) })
	info.mu.Lock()
	info.webhookContext = ctx
	info.mu.Unlock()
	info.UpdateStatus(TaskStatusFailed)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("webhook did not start")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel webhook request")
	}
	if waitWebhookRetry(ctx, 0) {
		t.Fatal("retry wait ignored cancellation")
	}
}

func TestQueuedCancellationWebhookOnce(t *testing.T) {
	received := make(chan WebhookPayload, 20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		received <- payload
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	info := RegisterTask(t.Name(), "tgfiles", "store", "", "test", server.URL)
	t.Cleanup(func() { DeleteTask(t.Name()) })
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			info.UpdateStatus(TaskStatusCancelled)
			info.Emit(taskevent.Event{Phase: taskevent.PhaseDone, Err: context.Canceled})
		}()
	}
	wg.Wait()
	select {
	case payload := <-received:
		if payload.Status != TaskStatusCancelled || payload.CompletedAt == nil {
			t.Fatalf("payload=%+v", payload)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("missing queued cancellation notification")
	}
	select {
	case <-received:
		t.Fatal("duplicate notification")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestCreateTaskRequestSizeLimit(t *testing.T) {
	handlers, _ := setupTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", strings.NewReader(`{"padding":"`+strings.Repeat("x", 1<<20)+`"}`))
	resp := httptest.NewRecorder()
	handlers.CreateTaskHandler(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d", resp.Code)
	}
}

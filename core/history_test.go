package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
)

func TestTaskHistoryAdmissionCancellationAndWorkerOutcome(t *testing.T) {
	resetCoreQueue(t)
	old := taskhistory.Default
	taskhistory.Default = taskhistory.NewStore(20, time.Hour)
	t.Cleanup(func() { taskhistory.Default = old })
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte(`workers=1
[admin]
enable=true
password='isolated-history-password'
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lifecycleConfig(t, "") })
	Prepare()
	qe := currentQueue()
	t.Cleanup(qe.CloseAndCancel)
	cancelled := lifecycleTask{id: "cancelled-history", run: func(context.Context) error { t.Error("cancelled queued task executed"); return nil }}
	failed := lifecycleTask{id: "failed-history", run: func(context.Context) error { return errors.New("isolated failure") }}
	if err := AddTask(t.Context(), cancelled); err != nil {
		t.Fatal(err)
	}
	if err := AddTasks(t.Context(), failed, cancelled); err == nil {
		t.Fatal("duplicate batch accepted")
	}
	records := taskhistory.Default.Snapshots()
	if len(records) != 1 || records[0].ID != cancelled.id {
		t.Fatalf("rejected batch damaged history: %+v", records)
	}
	if err := CancelTask(t.Context(), cancelled.id); err != nil {
		t.Fatal(err)
	}
	if err := AddTask(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	raced := lifecycleTask{id: "racing-history", run: func(context.Context) error { return nil }}
	for range 32 {
		wg.Go(func() {
			if AddTask(t.Context(), raced) == nil {
				accepted.Add(1)
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 1 {
		t.Fatal("same-ID admissions did not have one winner")
	}
	observed := false
	for _, record := range taskhistory.Default.Snapshots() {
		if record.ID == raced.id && record.Status == "queued" {
			observed = true
		}
	}
	if !observed {
		t.Fatal("rejected contender removed the accepted history")
	}
	qe.Close()
	worker(t.Context(), qe)
	deadline := time.Now().Add(time.Second)
	for {
		records = taskhistory.Default.Snapshots()
		done := 0
		for _, r := range records {
			if r.ID == cancelled.id && r.Status == "cancelled" {
				done++
			}
			if r.ID == failed.id && r.Status == "failed" && r.Error == "isolated failure" && r.Source == "bot" {
				done++
			}
		}
		if done == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal observations missing: %+v", records)
		}
		time.Sleep(time.Millisecond)
	}
}

package taskresult

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestTrackerClassifiesOutcomesAndDetachesSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		parentErr error
		want      State
	}{
		{name: "success", want: Succeeded},
		{name: "failure", err: errors.New("unavailable"), want: Failed},
		{name: "parent cancel", err: fmt.Errorf("wrapped: %w", context.Canceled), parentErr: context.Canceled, want: Cancelled},
		{name: "parent deadline", err: context.DeadlineExceeded, parentErr: context.DeadlineExceeded, want: Cancelled},
		{name: "internal cancellation", err: context.Canceled, want: Interrupted},
		{name: "internal deadline", err: context.DeadlineExceeded, want: Interrupted},
		{name: "saved before parent cancel", parentErr: context.Canceled, want: Succeeded},
		{name: "failure before parent cancel", err: errors.New("unavailable"), parentErr: context.Canceled, want: Failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker Tracker
			input := []Element{{ID: "same", Name: "first.bin"}, {ID: "same", Name: "second.bin"}}
			tracker.Reset(input)
			input[0].Name = "mutated"
			before := tracker.Snapshot()
			if before.Total != 2 || before.Pending != 2 {
				t.Fatal("initial files were not pending")
			}
			tracker.Start(0)
			if tracker.Snapshot().Running != 1 {
				t.Fatal("running file was counted as complete")
			}
			tracker.Finish(0, tc.err, tc.parentErr)
			result := tracker.Snapshot()
			if result.Elements[0].State != tc.want || result.Elements[0].Name != "first.bin" || result.Elements[1].State != Pending {
				t.Fatalf("incorrect per-file result: %+v", result)
			}
			if tc.err != nil && result.Elements[0].Error != tc.err.Error() {
				t.Fatal("original error was lost")
			}
			result.Elements[0].Name = "mutated snapshot"
			if tracker.Snapshot().Elements[0].Name != "first.bin" || before.Elements[0].State != Pending {
				t.Fatal("snapshot aliases active state")
			}
		})
	}
}

func TestTrackerConcurrentResultsRemainConsistent(t *testing.T) {
	const count = 100
	var tracker Tracker
	tracker.Reset(make([]Element, count))
	var wg sync.WaitGroup
	for index := range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tracker.Start(index)
			tracker.Finish(index, nil, nil)
			result := tracker.Snapshot()
			if result.Total != result.Pending+result.Running+result.Succeeded+result.Failed+result.Cancelled+result.Interrupted {
				t.Error("snapshot counts are inconsistent")
			}
		}()
	}
	wg.Wait()
	if result := tracker.Snapshot(); result.Succeeded != count {
		t.Fatalf("results lost: %+v", result)
	}
	tracker.Reset([]Element{{ID: "new"}})
	if result := tracker.Snapshot(); result.Total != 1 || result.Pending != 1 || result.Succeeded != 0 {
		t.Fatal("new execution retained old outcomes")
	}
}

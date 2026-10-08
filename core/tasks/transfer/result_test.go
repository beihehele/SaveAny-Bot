package transfer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

func TestResultSummaryPreservesTransferReturnPolicy(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers=1\nstream=true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("source unavailable")
	for _, tc := range []struct {
		name          string
		firstError    error
		cancelParent  bool
		success, fail int
		wantError     error
	}{
		{name: "all saved", success: 2},
		{name: "mixed outcome still returns nil", firstError: failure, success: 1, fail: 1},
		{name: "cancel is still returned", firstError: context.Canceled, cancelParent: true, wantError: context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			source := &executionStorage{openError: tc.firstError}
			if tc.cancelParent {
				source.open = func(context.Context) { cancel() }
			}
			second := &executionStorage{}
			if tc.cancelParent {
				second.openError = context.Canceled
			}
			elems := []TaskElement{
				{ID: "first", SourceStorage: source, TargetStorage: &executionStorage{}, FileInfo: storagetypes.FileInfo{Name: "first.bin", Size: 4}},
				{ID: "second", SourceStorage: second, TargetStorage: &executionStorage{}, FileInfo: storagetypes.FileInfo{Name: "second.bin", Size: 4}},
			}
			task := NewTransferTask("summary-test", ctx, elems, nil, true)
			if task.ResultSummary().Pending != 2 {
				t.Fatal("unprocessed files were counted as complete")
			}
			if err := task.Execute(ctx); !errors.Is(err, tc.wantError) {
				t.Fatalf("legacy transfer return changed: %v", err)
			}
			result := task.ResultSummary()
			if result.Succeeded != tc.success || result.Failed != tc.fail || result.Pending != 0 || result.Running != 0 || result.Interrupted != 0 {
				t.Fatalf("incorrect summary: %+v", result)
			}
			if tc.cancelParent && result.Cancelled != 2 {
				t.Fatalf("parent cancellation was misclassified: %+v", result)
			}
			if tc.fail == 1 && (len(task.FailedFiles()) != 1 || task.FailedFiles()[0] != "first.bin") {
				t.Fatal("legacy failed files were changed")
			}
			if task.Uploaded() != int64(tc.success*4) {
				t.Fatal("byte accounting was changed")
			}
		})
	}
}

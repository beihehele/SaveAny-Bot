package transfer

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type skippingDestination struct{ executionStorage }

func (*skippingDestination) Save(context.Context, io.Reader, string) error {
	return storagetypes.ErrSaveSkipped
}

func TestTransferSkipRemainsUnsavedUnderLegacyAndStrict(t *testing.T) {
	for _, stream := range []bool{false, true} {
		temp := initTransferConfig(t, stream)
		source := &integritySource{body: "data", size: 4}
		elem := *NewTaskElement(source, storagetypes.FileInfo{Name: "large.bin", Size: 4}, &skippingDestination{}, "")
		task := NewTransferTask("skip-test", t.Context(), []TaskElement{elem}, nil, true)
		if err := task.Execute(t.Context()); err != nil {
			t.Fatalf("legacy IgnoreErrors changed: %v", err)
		}
		summary := task.ResultSummary()
		counts := summary.Counts()
		if summary.Skipped != 1 || summary.Succeeded != 0 || task.Uploaded() != 0 || source.closed.Load() != 1 || !counts.Valid() || counts.Failed != 1 {
			t.Fatalf("skipped file counted as saved or leaked source: %+v %+v bytes=%d closed=%d", summary, counts, task.Uploaded(), source.closed.Load())
		}
		if err := taskresult.CompletionError(taskresult.WithPolicy(t.Context(), taskresult.Strict), nil, &counts); !errors.Is(err, taskresult.ErrIncomplete) {
			t.Fatalf("strict accepted a skipped save: %v", err)
		}
		if entries, err := os.ReadDir(temp); err != nil || len(entries) != 0 {
			t.Fatalf("skip leaked staging files: %v %v", entries, err)
		}
	}
}

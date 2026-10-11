package batchtfile

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestResultSummaryPreservesBatchFailurePolicy(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers=1\nthreads=1\nretry=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("target unavailable")
	for _, tc := range []struct {
		name                   string
		failIndex              int
		succeeded, interrupted int
	}{
		{name: "all saved", failIndex: -1, succeeded: 2},
		{name: "one saved then failed", failIndex: 1, succeeded: 1},
		{name: "first failed interrupts sibling", failIndex: 0, interrupted: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			elems := make([]TaskElement, 2)
			for index := range elems {
				elems[index] = TaskElement{ID: "same-id", Path: "file.bin", stream: true,
					File: filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, downloadClient{}, 4, "file.bin"),
					Storage: streamTestStorage{save: func(ctx context.Context, r io.Reader) error {
						if index == tc.failIndex {
							return failure
						}
						if err := ctx.Err(); err != nil {
							return err
						}
						_, err := io.Copy(io.Discard, r)
						return err
					}},
				}
			}
			// Sequential processing lets duplicate IDs finish one at a time;
			// results must still account for each distinct submitted element.
			task := NewBatchTGFileTask("summary-test", t.Context(), elems, nil)
			if task.ResultSummary().Pending != 2 {
				t.Fatal("constructor counted unprocessed files as successful")
			}
			err := task.Execute(t.Context())
			if tc.failIndex == -1 && err != nil || tc.failIndex != -1 && !errors.Is(err, failure) {
				t.Fatalf("batch return changed: %v", err)
			}
			result := task.ResultSummary()
			wantFailed := 0
			if tc.failIndex >= 0 {
				wantFailed = 1
				if result.Elements[tc.failIndex].State != taskresult.Failed {
					t.Fatal("file failure was lost")
				}
			}
			if result.Total != 2 || result.Succeeded != tc.succeeded || result.Failed != wantFailed || result.Interrupted != tc.interrupted || result.Pending != 0 || result.Running != 0 || result.Cancelled != 0 {
				t.Fatalf("incorrect final summary: %+v", result)
			}
		})
	}
}

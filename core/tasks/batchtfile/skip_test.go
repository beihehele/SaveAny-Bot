package batchtfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type skipTestStorage struct {
	storage.Storage
	skip  bool
	calls int
}

func (s *skipTestStorage) Save(_ context.Context, reader io.Reader, _ string) error {
	s.calls++
	if s.skip {
		return fmt.Errorf("oversized: %w", storagetypes.ErrSaveSkipped)
	}
	_, err := io.Copy(io.Discard, reader)
	return err
}

func TestBatchSkipDoesNotCancelRemainingFilesOrRetry(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("workers=1\nthreads=1\nretry=5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream_%t", stream), func(t *testing.T) {
			first := &skipTestStorage{skip: true}
			second := &skipTestStorage{}
			elems := make([]TaskElement, 2)
			for index, saver := range []*skipTestStorage{first, second} {
				name := fmt.Sprintf("file-%d.bin", index)
				elems[index] = TaskElement{ID: name, Path: name, Storage: saver, stream: stream,
					localPath: filepath.Join(t.TempDir(), name),
					File:      filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, downloadClient{}, 4, name)}
			}
			task := NewBatchTGFileTask("skip-test", t.Context(), elems, nil, false)
			if err := task.Execute(t.Context()); err != nil {
				t.Fatal(err)
			}
			summary := task.ResultSummary()
			if summary.Skipped != 1 || summary.Succeeded != 1 || summary.Interrupted != 0 || first.calls != 1 || second.calls != 1 {
				t.Fatalf("skip interrupted or retried: %+v calls=%d/%d", summary, first.calls, second.calls)
			}
			counts := summary.Counts()
			if err := taskresult.CompletionError(taskresult.WithPolicy(t.Context(), taskresult.Strict), nil, &counts); !errors.Is(err, taskresult.ErrIncomplete) {
				t.Fatalf("strict accepted a skipped input: %v", err)
			}
			i18n.Init("en")
			text, _, err := batchCompletionMessage(t.Context(), task, nil, []string{"existing.bin"})
			if err != nil || !strings.Contains(text, "Not saved (skipped by storage policy):\nfile-0.bin") || !strings.Contains(text, "Skipped conflicting files:\nexisting.bin") {
				t.Fatalf("skip information missing: %q %v", text, err)
			}
		})
	}
}

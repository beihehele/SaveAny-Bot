package batchtfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestQueuedShardReelMemberDoesNotCancelAlbumSiblings(t *testing.T) {
	root := t.TempDir()
	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte(fmt.Sprintf("workers=2\nthreads=1\nretry=1\nstream=false\n[temp]\nbase_path=%q\n", filepath.ToSlash(filepath.Join(root, "cache")))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	var saved atomic.Int32
	storage := streamTestStorage{save: func(ctx context.Context, reader io.Reader) error {
		data, err := io.ReadAll(reader)
		if err != nil {
			return err
		}
		if string(data) != "data" {
			return errors.New("member bytes changed")
		}
		saved.Add(1)
		return storagetypes.ErrSaveQueued
	}}
	var elements []TaskElement
	for index := 0; index < 3; index++ {
		file := filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, downloadClient{}, 4, fmt.Sprintf("photo-%d.bin", index))
		element, err := NewTaskElement(storage, file.Name(), file)
		if err != nil {
			t.Fatal(err)
		}
		elements = append(elements, *element)
	}
	task := NewBatchTGFileTask("album-queued", elements, nil)
	if err := task.Execute(t.Context()); !errors.Is(err, storagetypes.ErrSaveQueued) {
		t.Fatalf("result: %v", err)
	}
	summary := task.ResultSummary()
	if saved.Load() != 3 || summary.Pending != 3 || summary.Failed+summary.Cancelled+summary.Interrupted != 0 || summary.Succeeded != 0 {
		t.Fatalf("members=%d summary=%+v", saved.Load(), summary)
	}
}

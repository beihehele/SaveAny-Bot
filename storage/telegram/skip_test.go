package telegram

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/celestix/gotgproto/ext"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type unreadSkippedInput struct{ reads int }

func (r *unreadSkippedInput) Read([]byte) (int, error) { r.reads++; return 0, io.EOF }

func TestSkipLargeCannotCountAsSaved(t *testing.T) {
	ctx := tgutil.ExtWithContext(t.Context(), &ext.Context{Context: t.Context()})
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(MaxUploadFileSize+1))
	ctx = taskresult.WithPolicy(ctx, taskresult.Strict)
	reader := &unreadSkippedInput{}
	saver := &Telegram{config: storconfig.TelegramStorageConfig{SkipLarge: true}}
	err := saver.Save(ctx, reader, "large.bin")
	if !errors.Is(err, storagetypes.ErrSaveSkipped) || reader.reads != 0 {
		t.Fatalf("skip=%v consumed=%d", err, reader.reads)
	}
	var tracker taskresult.Tracker
	tracker.Reset([]taskresult.Element{{Name: "large.bin"}})
	tracker.Start(0)
	tracker.Finish(0, err, nil)
	summary := tracker.Snapshot()
	counts := summary.Counts()
	if summary.Skipped != 1 || summary.Succeeded != 0 || counts.Failed != 1 || !counts.Valid() {
		t.Fatalf("skip was counted as saved: %+v %+v", summary, counts)
	}
	if err := taskresult.CompletionError(ctx, nil, &counts); !errors.Is(err, taskresult.ErrIncomplete) {
		t.Fatalf("strict completion accepted unsaved input: %v", err)
	}
}

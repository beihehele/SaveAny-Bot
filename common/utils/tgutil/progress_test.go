package tgutil_test

import (
	"context"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
	"github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type progressValueKey struct{}
type progressFileInfo struct{}

func (progressFileInfo) TaskID() string      { return "test-progress" }
func (progressFileInfo) FileName() string    { return "test.bin" }
func (progressFileInfo) FileSize() int64     { return 100 << 20 }
func (progressFileInfo) StoragePath() string { return "folder/test.bin" }
func (progressFileInfo) StorageName() string { return "local" }

type progressBatchInfo struct{}

func (progressBatchInfo) TaskID() string                           { return "test-progress" }
func (progressBatchInfo) TotalSize() int64                         { return 100 << 20 }
func (progressBatchInfo) Downloaded() int64                        { return 50 << 20 }
func (progressBatchInfo) Count() int                               { return 2 }
func (progressBatchInfo) Processing() []batchtfile.TaskElementInfo { return nil }

func TestRetainedTaskProgressUsesBoundedTaskContext(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		for _, stage := range []string{"single start", "single update", "single done", "batch start", "batch update", "batch done", "copy scan", "copy done"} {
			isTerminal := stage == "single done" || stage == "batch done" || stage == "copy done"
			if isTerminal != terminal {
				continue
			}
			t.Run(stage, func(t *testing.T) {
				calls := 0
				peer := peerstore.NewPeerStorage(nil, true)
				peer.AddPeer(42, 1, peerstore.TypeUser, "test")
				invoker := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
					calls++
					if terminal {
						markup, ok := input.(*tg.MessagesEditMessageRequest).ReplyMarkup.(*tg.ReplyInlineMarkup)
						if !ok || len(markup.Rows) != 0 {
							t.Error("terminal feedback kept an inactive cancel button")
						}
					}
					deadline, ok := ctx.Deadline()
					if !ok || time.Until(deadline) > tgutil.ProgressReplyTimeout+time.Second || ctx.Err() != nil || ctx.Value(progressValueKey{}) != "task" {
						t.Error("progress RPC lost its task values or bounded independent lifetime")
					}
					return tgerr.New(400, "MESSAGE_NOT_MODIFIED")
				})
				shared := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peer}
				caller, cancel := context.WithCancel(context.WithValue(t.Context(), progressValueKey{}, "task"))
				defer cancel()
				caller = tgutil.ExtWithContext(caller, shared)
				if terminal {
					cancel()
				}
				single := tfile.NewProgressTrack(7, 42)
				batch := batchtfile.NewProgressTracker(7, 42)
				copy := copyfwd.NewProgressTracker(7, 42, "test-progress", "", 0)
				switch stage {
				case "single start":
					single.OnStart(caller, progressFileInfo{})
				case "single update":
					single.OnProgress(caller, progressFileInfo{}, 50<<20, 100<<20)
				case "single done":
					single.OnDone(caller, progressFileInfo{}, context.Canceled)
				case "batch start":
					batch.OnStart(caller, progressBatchInfo{})
				case "batch update":
					batch.OnProgress(caller, progressBatchInfo{})
				case "batch done":
					batch.OnDone(caller, progressBatchInfo{}, context.Canceled)
				case "copy scan":
					copy.OnScan(caller, 1, 1, 123)
				case "copy done":
					copy.OnDone(caller, taskresult.Counts{}, context.Canceled)
				}
				if calls != 1 || shared.Err() != nil || !terminal && caller.Err() != nil {
					t.Fatalf("progress calls=%d shared=%v caller=%v", calls, shared.Err(), caller.Err())
				}
			})
		}
	}
}

func TestProgressClientPreservesCallerDeadlineAndSharedLifetime(t *testing.T) {
	shared := &ext.Context{Context: t.Context()}
	caller, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	client, stop := tgutil.ProgressClient(tgutil.ExtWithContext(caller, shared), false)
	if client == shared || client == nil {
		t.Fatal("progress must derive a client")
	}
	stop()
	if client.Err() != context.Canceled || shared.Err() != nil || caller.Err() != nil {
		t.Fatal("progress cancelled a shared lifetime")
	}
	short, stopShort := context.WithTimeout(t.Context(), time.Millisecond)
	defer stopShort()
	client, stop = tgutil.ProgressClient(tgutil.ExtWithContext(short, shared), false)
	defer stop()
	actual, _ := client.Deadline()
	expected, _ := short.Deadline()
	if !actual.Equal(expected) {
		t.Fatal("progress extended caller deadline")
	}
	nilClient, stopNil := tgutil.ProgressClient(t.Context(), true)
	stopNil()
	if nilClient != nil {
		t.Fatal("missing client should skip feedback")
	}
}

func TestTerminalSaveProgressFallbackPreservesStylingAndBudget(t *testing.T) {
	var calls int
	var deadline time.Time
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "test")
	invoker := telegram.InvokeFunc(func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
		calls++
		req := input.(*tg.MessagesEditMessageRequest)
		d, ok := ctx.Deadline()
		if !ok {
			t.Error("missing progress deadline")
		}
		if calls == 1 {
			deadline = d
		} else if !d.Equal(deadline) {
			t.Error("fallback restarted progress deadline")
		}
		if req.Message != "terminal" || len(req.Entities) != 1 || req.Entities[0].(*tg.MessageEntityBold).Length != 8 {
			t.Error("fallback lost terminal styling")
		}
		if calls == 1 {
			return tgerr.New(400, "REPLY_MARKUP_INVALID")
		}
		if req.ReplyMarkup != nil {
			t.Error("fallback repeated rejected markup")
		}
		return tgerr.New(400, "MESSAGE_NOT_MODIFIED")
	})
	shared := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers}
	req := &tg.MessagesEditMessageRequest{ID: 7}
	req.SetMessage("terminal")
	req.SetEntities([]tg.MessageEntityClass{&tg.MessageEntityBold{Length: 8}})
	tgutil.EditProgress(tgutil.ExtWithContext(t.Context(), shared), 42, req, true)
	if calls != 2 || req.ReplyMarkup != nil || shared.Err() != nil {
		t.Fatal("terminal fallback changed the request or shared client")
	}
}

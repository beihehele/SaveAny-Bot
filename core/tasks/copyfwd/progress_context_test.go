package copyfwd

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

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

func progressContextClient(t *testing.T, invoke telegram.InvokeFunc) *ext.Context {
	t.Helper()
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "test")
	return &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoke), PeerStorage: peers}
}

func TestCopyProgressCancellationInterruptsEdit(t *testing.T) {
	started := make(chan context.Context, 1)
	done := make(chan struct{})
	client := progressContextClient(t, func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
		started <- ctx
		<-ctx.Done()
		return ctx.Err()
	})
	caller, cancel := context.WithCancel(context.WithValue(t.Context(), taskContextKey{}, "copy-task"))
	defer cancel()
	caller = tgutil.ExtWithContext(caller, client)
	p := &Progress{MessageID: 7, ChatID: 42, TaskID: "copy-task"}
	go func() { p.OnScan(caller, 1, 1, 123); close(done) }()
	var rpcCtx context.Context
	select {
	case rpcCtx = <-started:
	case <-time.After(time.Second):
		t.Fatal("progress edit did not start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("progress edit ignored task cancellation")
	}
	if rpcCtx.Value(taskContextKey{}) != "copy-task" || client.Err() != nil {
		t.Fatal("progress lost task values or cancelled the shared client")
	}
	if deadline, ok := rpcCtx.Deadline(); !ok || time.Until(deadline) > tgutil.ProgressReplyTimeout+time.Second {
		t.Fatal("progress edit has no bounded lifetime")
	}
}

func TestCopyTerminalProgressKeepsOneBoundedBudgetAfterCancellation(t *testing.T) {
	var calls int
	var deadline time.Time
	client := progressContextClient(t, func(ctx context.Context, input bin.Encoder, _ bin.Decoder) error {
		calls++
		d, ok := ctx.Deadline()
		if !ok || time.Until(d) > tgutil.ProgressReplyTimeout+time.Second || ctx.Err() != nil || ctx.Value(taskContextKey{}) != "copy-task" {
			t.Error("terminal progress lost its independent bounded context")
		}
		if calls == 1 {
			deadline = d
		} else if !deadline.Equal(d) {
			t.Error("fallback extended the terminal feedback budget")
		}
		req := input.(*tg.MessagesEditMessageRequest)
		if req.Message != i18n.T(i18nk.BotMsgProgressCopyCanceled) {
			t.Error("terminal feedback changed the cancellation outcome")
		}
		if calls == 1 {
			markup, ok := req.ReplyMarkup.(*tg.ReplyInlineMarkup)
			if !ok || len(markup.Rows) != 0 {
				t.Error("terminal progress kept the cancel button")
			}
			return tgerr.New(400, "REPLY_MARKUP_INVALID")
		}
		return tgerr.New(400, "MESSAGE_NOT_MODIFIED")
	})
	caller, cancel := context.WithCancel(context.WithValue(t.Context(), taskContextKey{}, "copy-task"))
	caller = tgutil.ExtWithContext(caller, client)
	cancel()
	(&Progress{MessageID: 7, ChatID: 42, TaskID: "copy-task"}).OnDone(caller, taskresult.Counts{}, context.Canceled)
	if calls != 2 || client.Err() != nil || caller.Err() != context.Canceled {
		t.Fatalf("unexpected fallback or context mutation: calls=%d shared=%v caller=%v", calls, client.Err(), caller.Err())
	}
}

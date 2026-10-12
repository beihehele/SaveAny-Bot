package copyfwd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type executionCancelProgress struct {
	cancel context.CancelFunc
	mode   string
	done   int
	copied int
	failed int
	err    error
}

func (*executionCancelProgress) OnScan(context.Context, int, int, int) {}
func (p *executionCancelProgress) OnScanDone(context.Context, int, int, int) {
	if p.mode == "empty_scan_feedback" {
		p.cancel()
	}
}
func (p *executionCancelProgress) OnForward(_ context.Context, done, total int) {
	if p.mode == "last_feedback" && done == total && done > 0 {
		p.cancel()
	}
}
func (p *executionCancelProgress) OnDone(_ context.Context, copied, failed int, err error) {
	p.done++
	p.copied, p.failed, p.err = copied, failed, err
}

func TestCopyCancellationAtLastBoundary(t *testing.T) {
	const env = "SAVEANY_COPY_LAST_CANCEL_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestCopyCancellationAtLastBoundary$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated copy last cancellation: %v\n%s", err, output)
		}
		return
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("workers=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	cache.Init()
	for index, mode := range []string{"last_rpc", "copied_then_cancel", "last_feedback", "empty_scan_feedback", "ordinary_failure", "success"} {
		t.Run(mode, func(t *testing.T) {
			caller, cancel := context.WithCancel(context.WithValue(t.Context(), taskContextKey{}, "copy-task"))
			defer cancel()
			progress := &executionCancelProgress{cancel: cancel, mode: mode}
			forwardCalls, confirmed := 0, 0
			msg := &tg.Message{ID: 42, Message: "caption", Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}
			invoker := func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
				if ctx.Value(taskContextKey{}) != "copy-task" {
					return errors.New("RPC lost task context")
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				switch input.(type) {
				case *tg.MessagesGetHistoryRequest:
					messages := []tg.MessageClass{msg}
					if mode == "empty_scan_feedback" {
						messages = nil
					}
					output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
				case *tg.ChannelsGetMessagesRequest:
					output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: []tg.MessageClass{msg}}
				case *tg.MessagesForwardMessagesRequest:
					forwardCalls++
					if mode == "last_rpc" {
						cancel()
						return ctx.Err()
					}
					if mode == "ordinary_failure" {
						return errors.New("mock forwarding failure")
					}
					confirmed++
					output.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 142, Message: "caption"}}}}
					if mode == "copied_then_cancel" {
						cancel()
					}
				case *tg.MessagesEditMessageRequest:
					output.(*tg.UpdatesBox).Updates = &tg.Updates{}
				default:
					return fmt.Errorf("unexpected RPC %T", input)
				}
				return nil
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
			peers.AddPeer(222, 2, peerstore.TypeChannel, "target")
			client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(telegram.InvokeFunc(invoker)), PeerStorage: peers, Self: &tg.User{ID: int64(1000 + index)}}
			var source, target constant.TDLibPeerID
			source.Channel(111)
			target.Channel(222)
			task := NewTask(mode, 42, int64(source), int64(target), 3, "", 1, progress)
			if !TryBegin(42, task.ID) {
				t.Fatal("copy slot unavailable")
			}
			t.Cleanup(func() { End(42, task.ID); End(42, "next") })
			err := task.executeWithClient(caller, func() *ext.Context { return client })
			cancelled := mode != "ordinary_failure" && mode != "success"
			if cancelled && (!errors.Is(err, context.Canceled) || !errors.Is(progress.err, context.Canceled) || taskresult.ClassifyOutcome(caller, err) != taskresult.OutcomeCancelled) {
				t.Fatalf("last-boundary cancellation returned success: err=%v progress=%v", err, progress.err)
			}
			if !cancelled && (err != nil || progress.err != nil) {
				t.Fatalf("ordinary completion policy changed: %v %v", err, progress.err)
			}
			wantCounts := taskresult.Counts{Total: 1, Succeeded: 1}
			switch mode {
			case "last_rpc":
				wantCounts = taskresult.Counts{Total: 1, Cancelled: 1}
			case "ordinary_failure":
				wantCounts = taskresult.Counts{Total: 1, Failed: 1}
			case "empty_scan_feedback":
				wantCounts = taskresult.Counts{}
			}
			if summary := task.ResultSummary(); summary.Counts() != wantCounts || !summary.Counts().Valid() || len(summary.Elements) != 0 {
				t.Fatalf("incorrect logical item counts: %+v want %+v", summary, wantCounts)
			}
			wantCalls := 1
			if mode == "empty_scan_feedback" {
				wantCalls = 0
			}
			if forwardCalls != wantCalls || progress.done != 1 || progress.copied != confirmed || progress.failed != forwardCalls-confirmed || client.Err() != nil {
				t.Fatalf("lost counts, repeated forward/feedback or cancelled shared client: calls=%d confirmed=%d progress=%+v client=%v", forwardCalls, confirmed, progress, client.Err())
			}
			if !TryBegin(42, "next") {
				t.Fatal("terminal cancellation did not release copy ownership")
			}
			End(42, "next")
		})
	}
}

package copyfwd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskhistory"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

type resultCopyTask struct {
	*Task
	client *ext.Context
}

func (t *resultCopyTask) Execute(ctx context.Context) error {
	return t.executeWithClient(ctx, func() *ext.Context { return t.client })
}

type resultCopyProgress struct {
	*executionCancelProgress
	task *Task
	test *testing.T
}

func (p *resultCopyProgress) OnScanDone(_ context.Context, _, _, _ int) {
	if p.mode == "cancel_after_scan" {
		if counts := p.task.ResultSummary().Counts(); counts != (taskresult.Counts{Total: 2, Pending: 2}) {
			p.test.Errorf("selection counts not available at scan feedback: %+v", counts)
		}
		p.cancel()
	}
}

// Exercise the real scan, album expansion, forwarding, queue worker, terminal
// event and history store. Only the Telegram transport and input client are fake.
func TestCopyLogicalCountsReachWorkerHistory(t *testing.T) {
	const env = "SAVEANY_COPY_COUNTS_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestCopyLogicalCountsReachWorkerHistory$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated copy counts: %v\n%s", err, output)
		}
		return
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("workers=1\n[admin]\nenable=true\npassword='isolated-copy-counts-password'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	cache.Init()
	runCtx, stop := context.WithCancel(t.Context())
	done := core.Run(runCtx)
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	})
	for index, tc := range []struct {
		mode    string
		want    taskresult.Counts
		calls   int
		outcome taskresult.Outcome
	}{
		{"success", taskresult.Counts{Total: 2, Succeeded: 2}, 2, taskresult.OutcomeSuccess},
		{"partial", taskresult.Counts{Total: 2, Succeeded: 1, Failed: 1}, 2, taskresult.OutcomeSuccess},
		{"all_failed", taskresult.Counts{Total: 2, Failed: 2}, 2, taskresult.OutcomeSuccess},
		{"internal_deadline", taskresult.Counts{Total: 2, Succeeded: 1, Interrupted: 1}, 2, taskresult.OutcomeSuccess},
		{"cancel_first_rpc", taskresult.Counts{Total: 2, Cancelled: 1, Pending: 1}, 1, taskresult.OutcomeCancelled},
		{"confirmed_then_cancel", taskresult.Counts{Total: 2, Succeeded: 1, Pending: 1}, 1, taskresult.OutcomeCancelled},
		{"ordinary_then_cancel", taskresult.Counts{Total: 2, Failed: 1, Pending: 1}, 1, taskresult.OutcomeCancelled},
		{"cancel_after_scan", taskresult.Counts{Total: 2, Pending: 2}, 0, taskresult.OutcomeCancelled},
		{"scan_failure", taskresult.Counts{}, 0, taskresult.OutcomeFailed},
		{"empty", taskresult.Counts{}, 0, taskresult.OutcomeSuccess},
		{"unavailable", taskresult.Counts{}, 0, taskresult.OutcomeFailed},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			caller, cancel := context.WithCancel(t.Context())
			defer cancel()
			var logs bytes.Buffer
			logger := log.New(&logs)
			logger.SetFormatter(log.JSONFormatter)
			caller = log.WithContext(caller, logger)
			album := &tg.Message{ID: 42, Message: "album caption", Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}}
			album.SetGroupedID(99)
			member := &tg.Message{ID: 41, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 2}}}
			member.SetGroupedID(99)
			single := &tg.Message{ID: 40, Message: "single", Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 3}}}
			messages := []tg.MessageClass{album, member, single}
			calls := 0
			invoker := func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				switch req := input.(type) {
				case *tg.MessagesGetHistoryRequest:
					if tc.mode == "scan_failure" {
						return errors.New("synthetic scan failure")
					}
					if tc.mode == "empty" {
						output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{}
					} else {
						output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
					}
				case *tg.ChannelsGetMessagesRequest:
					// Telegram returns only requested IDs. A cached representative is
					// excluded by the real range reader before this request.
					var selected []tg.MessageClass
					for _, requested := range req.ID {
						for _, msg := range messages {
							if msg.(*tg.Message).ID == requested.(*tg.InputMessageID).ID {
								selected = append(selected, msg)
							}
						}
					}
					output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: selected}
				case *tg.MessagesForwardMessagesRequest:
					calls++
					wantIDs := []int{40}
					if calls == 1 {
						wantIDs = []int{41, 42}
					}
					from, fromOK := req.FromPeer.(*tg.InputPeerChannel)
					to, toOK := req.ToPeer.(*tg.InputPeerChannel)
					if !reflect.DeepEqual(req.ID, wantIDs) || !req.DropAuthor || req.TopMsgID != 3 || !fromOK || from.ChannelID != 111 || !toOK || to.ChannelID != 222 {
						t.Error("copy forwarding contract changed")
						return fmt.Errorf("copy forwarding contract changed: %+v", req)
					}
					if tc.mode == "cancel_first_rpc" {
						cancel()
						return ctx.Err()
					}
					if tc.mode == "ordinary_then_cancel" {
						cancel()
						return errors.New("ordinary failure during cancellation")
					}
					if tc.mode == "all_failed" || tc.mode == "partial" && calls == 2 {
						return errors.New("synthetic forwarding failure")
					}
					if tc.mode == "internal_deadline" && calls == 1 {
						return context.DeadlineExceeded
					}
					output.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 100 + calls, Message: "album caption"}}}}
					if tc.mode == "confirmed_then_cancel" {
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
			client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(telegram.InvokeFunc(invoker)), PeerStorage: peers, Self: &tg.User{ID: int64(2000 + index)}}
			var source, target constant.TDLibPeerID
			source.Channel(111)
			target.Channel(222)
			task := NewTask("counts-"+tc.mode, 42, int64(source), int64(target), 3, "", 2, nil)
			progress := &resultCopyProgress{executionCancelProgress: &executionCancelProgress{cancel: cancel, mode: tc.mode}, task: task, test: t}
			task.Progress = progress
			if tc.mode == "unavailable" {
				client = nil
			}
			// Poll while the real worker runs; every snapshot must be consistent,
			// and no per-message array may appear even when observers race writes.
			readCtx, stopReading := context.WithCancel(t.Context())
			var readers sync.WaitGroup
			readers.Add(1)
			go func() {
				defer readers.Done()
				ticker := time.NewTicker(time.Millisecond)
				defer ticker.Stop()
				for {
					select {
					case <-readCtx.Done():
						return
					case <-ticker.C:
						summary := task.ResultSummary()
						if !summary.Counts().Valid() || len(summary.Elements) != 0 {
							t.Error("inconsistent or unbounded snapshot")
						}
					}
				}
			}()
			defer func() { stopReading(); readers.Wait() }()
			terminal := make(chan taskevent.Event, 1)
			caller = taskevent.WithSink(caller, taskevent.SinkFunc(func(e taskevent.Event) {
				if e.Phase == taskevent.PhaseDone {
					terminal <- e
				}
			}))
			if !TryBegin(42, task.ID) {
				t.Fatal("copy ownership not released")
			}
			t.Cleanup(func() { End(42, task.ID) })
			if err := core.AddTask(caller, &resultCopyTask{Task: task, client: client}); err != nil {
				t.Fatal(err)
			}
			select {
			case e := <-terminal:
				if e.Outcome != tc.outcome || e.ResultSummary == nil || *e.ResultSummary != tc.want {
					t.Fatalf("terminal event=%+v summary=%+v want=%+v", e, e.ResultSummary, tc.want)
				}
			case <-time.After(15 * time.Second):
				t.Fatal("copy worker did not finish")
			}
			hasErrorLog := strings.Contains(logs.String(), `"level":"error"`)
			if tc.mode == "cancel_first_rpc" && hasErrorLog {
				t.Fatalf("normal forward cancellation logged as failure: %s", logs.String())
			}
			if (tc.mode == "ordinary_then_cancel" || tc.mode == "internal_deadline") && !hasErrorLog {
				t.Fatalf("real forward failure lost error log: %s", logs.String())
			}
			// Event sinks run in order. Shutdown joins the worker at test cleanup;
			// wait for its history sink without fixed sleeps or racing observations.
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			poll := time.NewTicker(time.Millisecond)
			defer poll.Stop()
			for {
				for _, record := range taskhistory.Default.Snapshots() {
					if record.ID == task.ID && record.ResultSummary != nil {
						if *record.ResultSummary != tc.want || record.Source != "bot" || record.Type != "copy" {
							t.Fatalf("history lost copy units: %+v", record)
						}
						if calls != tc.calls || progress.done != 1 || progress.copied != tc.want.Succeeded || progress.failed != tc.want.Failed+tc.want.Cancelled+tc.want.Interrupted {
							t.Fatalf("forward/progress changed: calls=%d progress=%+v", calls, progress)
						}
						if client != nil && client.Err() != nil {
							t.Fatal("shared client cancelled")
						}
						if task.ResultSummary().Counts() != tc.want {
							t.Fatal("provider and worker disagree")
						}
						return
					}
				}
				select {
				case <-poll.C:
				case <-deadline.C:
					t.Fatal("copy result not retained")
				}
			}
		})
	}
}

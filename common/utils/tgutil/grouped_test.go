package tgutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
)

func groupMessage(id int, group int64) *tg.Message {
	m := &tg.Message{ID: id}
	m.SetGroupedID(group)
	return m
}

func TestMessageRPCStopsAliasRetriesOnCancellationDeadlineAndFloodWait(t *testing.T) {
	const env = "SAVEANY_GROUP_LOOKUP_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestMessageRPCStopsAliasRetriesOnCancellationDeadlineAndFloodWait$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated lookup: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	cache.Init()
	for _, kind := range []string{"single", "range"} {
		for _, cause := range []error{context.Canceled, context.DeadlineExceeded, tgerr.New(420, "FLOOD_WAIT_10"), tgerr.New(400, "CHANNEL_PRIVATE"), tgerr.New(400, "CHANNEL_INVALID"), errors.New("connection lost")} {
			calls := 0
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
			ctx := &ext.Context{Context: t.Context(), Self: &tg.User{ID: 9000}, PeerStorage: peers, Raw: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, _ bin.Decoder) error { calls++; return cause }))}
			var err error
			if kind == "single" {
				_, err = GetMessageByID(ctx, 111, 500)
			} else {
				_, err = GetMessagesRange(ctx, 111, 500, 501)
			}
			if !errors.Is(err, cause) || calls != 1 || ctx.Err() != nil {
				t.Fatalf("%s retried/masked cause or cancelled shared client: calls=%d err=%v cause=%v", kind, calls, err, cause)
			}
		}
	}
}

func TestGroupedLookupFindsSparseMembersWithinOneBoundedRange(t *testing.T) {
	ctx := &ext.Context{Context: t.Context()}
	calls := 0
	msgs, err := groupedMessages(ctx, 111, groupMessage(100, 77), func(_ *ext.Context, chat int64, lo, hi int) ([]*tg.Message, error) {
		calls++
		if chat != 111 || lo != 51 || hi != 149 || hi-lo+1 > 100 {
			t.Fatalf("unbounded/incorrect range: %d %d %d", chat, lo, hi)
		}
		return []*tg.Message{groupMessage(140, 77), groupMessage(100, 77), groupMessage(70, 77), groupMessage(99, 88), nil, groupMessage(70, 77), groupMessage(150, 77)}, nil
	})
	ids := []int{}
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
	}
	if err != nil || calls != 1 || !reflect.DeepEqual(ids, []int{70, 100, 140}) {
		t.Fatalf("sparse album selection: %v %d %v", ids, calls, err)
	}
	for _, id := range []int{1, 2147483647} {
		_, err := groupedMessages(ctx, 111, groupMessage(id, 77), func(_ *ext.Context, _ int64, lo, hi int) ([]*tg.Message, error) {
			if lo < 1 || hi > 2147483647 || hi-lo+1 > 100 || hi < lo {
				t.Fatalf("invalid edge range %d %d", lo, hi)
			}
			return nil, nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestGroupedLookupDoesNotPublishPartialResultsOnCancellationOrError(t *testing.T) {
	for _, mode := range []string{"cancel_before", "cancel_after", "flood", "rpc"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel_before" {
				cancel()
			}
			calls := 0
			items, err := groupedMessages(&ext.Context{Context: ctx}, 111, groupMessage(100, 77), func(_ *ext.Context, _ int64, _ int, _ int) ([]*tg.Message, error) {
				calls++
				if mode == "cancel_after" {
					cancel()
					return []*tg.Message{groupMessage(100, 77)}, nil
				}
				if mode == "flood" {
					return []*tg.Message{groupMessage(100, 77)}, tgerr.New(420, "FLOOD_WAIT_10")
				}
				return nil, errors.New("rpc failed")
			})
			if err == nil || items != nil || mode == "cancel_before" && calls != 0 || ctx.Err() == nil && (mode == "cancel_before" || mode == "cancel_after") {
				t.Fatalf("lookup leaked results or ignored cancellation: %v %v %d", items, err, calls)
			}
			if mode == "flood" && !stopMessageLookup(&ext.Context{Context: t.Context()}, err) {
				t.Fatal("rate limit would retry aliases")
			}
		})
	}
}

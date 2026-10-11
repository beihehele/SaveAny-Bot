package copyfwd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
)

type stalledPageInvoker struct {
	calls   int
	partial bool
}

func (i *stalledPageInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	i.calls++
	if ctx.Value(taskContextKey{}) != "copy-task" {
		return fmt.Errorf("lost caller values")
	}
	if _, ok := ctx.Deadline(); !ok {
		return fmt.Errorf("scan RPC has no deadline")
	}
	if i.partial && i.calls == 1 {
		var msgs []tg.MessageClass
		for id := 100; id > 0; id-- {
			msgs = append(msgs, &tg.Message{ID: id, Message: "tag", PeerID: &tg.PeerUser{UserID: 123}})
		}
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: msgs}
		return nil
	}
	<-ctx.Done()
	return ctx.Err()
}

func TestCopyScanTimeoutAbortsWithoutPartialResultsOrSharedCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[telegram]\nscan_timeout='80ms'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{"", "msgre:tag"} {
		for _, partial := range []bool{false, true} {
			t.Run(fmt.Sprintf("filter=%s/partial=%v", filter, partial), func(t *testing.T) {
				invoker := &stalledPageInvoker{partial: partial}
				peers := peerstore.NewPeerStorage(nil, true)
				peers.AddPeer(123, 456, peerstore.TypeUser, "")
				client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers}
				caller := context.WithValue(t.Context(), taskContextKey{}, "copy-task")
				ids, err := collectMatchedIDs(caller, client, 123, filter, 101, nil)
				if !errors.Is(err, context.DeadlineExceeded) || len(ids) != 0 {
					t.Fatalf("partial scan leaked results: ids=%v error=%v", ids, err)
				}
				expected := 1
				if partial {
					expected = 2
				}
				if invoker.calls != expected || client.Err() != nil || caller.Err() != nil {
					t.Fatalf("calls=%d shared=%v caller=%v", invoker.calls, client.Err(), caller.Err())
				}
			})
		}
	}
	t.Run("caller deadline is preserved", func(t *testing.T) {
		invoker := &stalledPageInvoker{}
		peers := peerstore.NewPeerStorage(nil, true)
		peers.AddPeer(123, 456, peerstore.TypeUser, "")
		client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers}
		caller, cancel := context.WithTimeout(context.WithValue(t.Context(), taskContextKey{}, "copy-task"), 10*time.Millisecond)
		defer cancel()
		if _, err := collectMatchedIDs(caller, client, 123, "", 1, nil); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		if client.Err() != nil {
			t.Fatal("page deadline cancelled the shared client")
		}
	})
}

package copyfwd

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
)

type taskContextKey struct{}

type cancellingScanInvoker struct {
	cancel context.CancelFunc
	calls  int
}

func (i *cancellingScanInvoker) Invoke(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
	i.calls++
	if ctx.Value(taskContextKey{}) != "copy-task" {
		return fmt.Errorf("RPC lost task context")
	}
	i.cancel()
	return ctx.Err()
}

func TestCopyScanRPCUsesTaskContext(t *testing.T) {
	for _, filter := range []string{"", "msgre:tag"} {
		t.Run(fmt.Sprintf("filter_%s", filter), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), taskContextKey{}, "copy-task"))
			defer cancel()
			invoker := &cancellingScanInvoker{cancel: cancel}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(123, 456, peerstore.TypeUser, "")
			client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers}
			if _, err := collectMatchedIDs(ctx, client, 123, filter, 1, nil); !errors.Is(err, context.Canceled) {
				t.Fatalf("scan did not preserve cancellation: %v", err)
			}
			if invoker.calls != 1 || client.Err() != nil {
				t.Fatalf("unexpected RPCs or cancelled shared client: calls=%d service=%v", invoker.calls, client.Err())
			}
		})
	}
}

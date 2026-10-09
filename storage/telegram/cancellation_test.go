package telegram

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
)

type rejectTelegramRPC struct {
	calls int
}

func (r *rejectTelegramRPC) Invoke(context.Context, bin.Encoder, bin.Decoder) error {
	r.calls++
	return errors.New("unexpected Telegram RPC")
}

func TestSplitSavePreservesTaskCancellation(t *testing.T) {
	serviceCtx := t.Context()
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(123, 456, peerstore.TypeUser, "")
	invoker := &rejectTelegramRPC{}
	client := &ext.Context{Context: serviceCtx, Raw: tg.NewClient(invoker), PeerStorage: peers}
	ctx, cancel := context.WithCancel(tgutil.ExtWithContext(serviceCtx, client))
	defer cancel()
	payload := make([]byte, 2<<20)
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(len(payload)))
	saver := &Telegram{}
	if err := saver.Init(ctx, &storconfig.TelegramStorageConfig{ChatID: 123, SplitSizeMB: 1}); err != nil {
		t.Fatal(err)
	}
	// MIME detection happens after peer resolution and rate-limit admission.
	// Cancel there to exercise Save's actual handoff into splitUpload.
	reader := &cancelMediaReader{Reader: bytes.NewReader(payload), cancel: cancel}
	if err := saver.Save(ctx, reader, "large.bin"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Save lost task cancellation: %v", err)
	}
	if serviceCtx.Err() != nil || invoker.calls != 0 {
		t.Fatalf("client was cancelled or task reached RPC: service=%v calls=%d", serviceCtx.Err(), invoker.calls)
	}
}

package retry

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/krau/SaveAny-Bot/client/middleware/recovery"
)

func TestRetryWithOuterRecovery(t *testing.T) {
	for _, mode := range []string{"exhausted", "eventually_succeeds", "business_error", "transport_error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			var last error
			raw := telegram.InvokeFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
				calls++
				if err := ctx.Err(); err != nil {
					return err
				}
				switch mode {
				case "business_error":
					last = tgerr.New(403, "CHAT_WRITE_FORBIDDEN")
				case "transport_error":
					if calls == 1 {
						return errors.New("connection lost")
					}
					return nil
				case "eventually_succeeds":
					if calls == 3 {
						return nil
					}
				case "cancelled":
					cancel()
					return ctx.Err()
				}
				if mode != "business_error" {
					last = tgerr.New(500, "RPC_CALL_FAIL")
				}
				return fmt.Errorf("mock RPC: %w", last)
			})
			// Match the production order, with finite, immediate network recovery
			// so an accidentally repeated retry batch fails without sleeping.
			invoke := recovery.New(ctx, func() backoff.BackOff {
				return backoff.WithMaxRetries(backoff.NewConstantBackOff(0), 2)
			}).Handle(New(5).Handle(raw))
			err := invoke(ctx, &tg.MessagesForwardMessagesRequest{}, &tg.UpdatesBox{})
			wantCalls := map[string]int{"exhausted": 5, "eventually_succeeds": 3, "business_error": 1, "transport_error": 2, "cancelled": 1}[mode]
			if calls != wantCalls {
				t.Fatalf("calls=%d want=%d error=%v", calls, wantCalls, err)
			}
			switch mode {
			case "exhausted", "business_error":
				if !errors.Is(err, last) {
					t.Fatalf("original RPC error lost: %v", err)
				}
				var rpc *tgerr.Error
				if !errors.As(err, &rpc) {
					t.Fatalf("RPC type lost: %v", err)
				}
			case "cancelled":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation lost: %v", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

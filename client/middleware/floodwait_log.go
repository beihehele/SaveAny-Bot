package middleware

import (
	"context"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// NewLoggingFloodWait returns a flood-wait middleware that logs waits and
// respects context cancellation (so task cancel can interrupt long waits).
// Wait duration follows Telegram (same as gotd SimpleWaiter with no maxWait).
func NewLoggingFloodWait() telegram.Middleware {
	return telegram.MiddlewareFunc(func(next tg.Invoker) telegram.InvokeFunc {
		return func(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
			var retries uint
			for {
				err := next.Invoke(ctx, input, output)
				if err == nil {
					return nil
				}
				d, ok := tgerr.AsFloodWait(err)
				if !ok {
					return err
				}
				retries++
				if d <= 0 {
					d = time.Second
				}
				log.FromContext(ctx).Warnf("FLOOD_WAIT: sleeping %v (retry %d)", d, retries)
				timer := time.NewTimer(d)
				select {
				case <-timer.C:
					continue
				case <-ctx.Done():
					if !timer.Stop() {
						<-timer.C
					}
					return ctx.Err()
				}
			}
		}
	})
}

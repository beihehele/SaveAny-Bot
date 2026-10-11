package recovery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

type signaledBackoff struct {
	backoff.BackOff
	waiting chan struct{}
	once    sync.Once
}

func (b *signaledBackoff) NextBackOff() time.Duration {
	b.once.Do(func() { close(b.waiting) })
	return b.BackOff.NextBackOff()
}

func TestRecoveryCancellationInterruptsBackoff(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "caller", true: "service"}[shutdown], func(t *testing.T) {
			service, stopService := context.WithCancel(t.Context())
			defer stopService()
			caller, stopCaller := context.WithCancel(t.Context())
			defer stopCaller()
			waiting := make(chan struct{})
			mw := New(service, func() backoff.BackOff {
				return &signaledBackoff{BackOff: backoff.NewConstantBackOff(time.Hour), waiting: waiting}
			})
			var calls atomic.Int32
			done := make(chan error, 1)
			go func() {
				done <- mw.Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
					calls.Add(1)
					return errors.New("connection lost")
				}))(caller, &tg.MessagesGetHistoryRequest{}, &tg.MessagesMessagesBox{})
			}()
			select {
			case <-waiting:
			case <-time.After(time.Second):
				t.Fatal("recovery did not reach backoff")
			}
			if shutdown {
				stopService()
			} else {
				stopCaller()
			}
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) || calls.Load() != 1 {
					t.Fatalf("cancelled recovery: error=%v calls=%d", err, calls.Load())
				}
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt backoff")
			}
		})
	}
}

func TestRecoveryServiceShutdownInterruptsInvocation(t *testing.T) {
	service, stop := context.WithCancel(t.Context())
	defer stop()
	started := make(chan struct{})
	done := make(chan error, 1)
	mw := New(service, func() backoff.BackOff { return &backoff.StopBackOff{} })
	go func() {
		done <- mw.Handle(telegram.InvokeFunc(func(ctx context.Context, _ bin.Encoder, _ bin.Decoder) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		}))(t.Context(), &tg.MessagesGetHistoryRequest{}, &tg.MessagesMessagesBox{})
	}()
	<-started
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service shutdown did not cancel invocation")
	}
}

func TestRecoveryRetriesTransportButNotTelegramErrors(t *testing.T) {
	for _, business := range []bool{false, true} {
		t.Run(map[bool]string{false: "transport", true: "business"}[business], func(t *testing.T) {
			calls, factories := 0, 0
			mw := New(t.Context(), func() backoff.BackOff {
				factories++
				return backoff.WithMaxRetries(backoff.NewConstantBackOff(0), 2)
			})
			invoke := mw.Handle(telegram.InvokeFunc(func(context.Context, bin.Encoder, bin.Decoder) error {
				calls++
				if business {
					return tgerr.New(400, "CHAT_ADMIN_REQUIRED")
				}
				if calls%2 != 0 {
					return errors.New("connection lost")
				}
				return nil
			}))
			for range 2 {
				err := invoke(t.Context(), &tg.MessagesGetHistoryRequest{}, &tg.MessagesMessagesBox{})
				if business && !tgerr.Is(err, "CHAT_ADMIN_REQUIRED") || !business && err != nil {
					t.Fatal(err)
				}
			}
			wantCalls := 4
			if business {
				wantCalls = 2
			}
			if factories != 2 || calls != wantCalls {
				t.Fatalf("factories=%d calls=%d", factories, calls)
			}
		})
	}
}

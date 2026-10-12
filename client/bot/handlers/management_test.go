package handlers

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/database"
)

func TestForumPaginationRejectsCyclesAndHasFiniteBudget(t *testing.T) {
	for _, mode := range []string{"cycle", "budget", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			client := &ext.Context{Context: ctx, Raw: tg.NewClient(telegram.InvokeFunc(func(_ context.Context, _ bin.Encoder, out bin.Decoder) error {
				calls++
				if mode == "cancel" {
					cancel()
					return context.Canceled
				}
				page := 1
				if mode == "budget" {
					page = calls
				}
				topics := make([]tg.ForumTopicClass, 100)
				for i := range topics {
					topics[i] = &tg.ForumTopic{ID: page*100 + i, TopMessage: page*100 + i, Date: page, Title: fmt.Sprint(i)}
				}
				*out.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{Topics: topics}
				return nil
			}))}
			err := forEachForumTopicsPage(client, &tg.InputPeerChannel{ChannelID: 111}, func([]tg.ForumTopicClass, map[int]int) (bool, error) { return false, nil })
			want := 2
			if mode == "budget" {
				want = 100
			}
			if mode == "cancel" {
				want = 1
			}
			if err == nil || calls != want {
				t.Fatalf("pagination failed to bound: %s calls=%d err=%v", mode, calls, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation cause lost")
			}
		})
	}
}

func TestWatchShutdownJoinDoesNotStartOrCancelSharedClient(t *testing.T) {
	previous := watchForwards
	t.Cleanup(func() { watchForwards = previous })
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
	watchForwards = newWatchForwardQueue(1, 1, gate, func() *ext.Context { t.Fatal("join started client"); return nil }, nil)
	if err := WaitWatchForwards(t.Context()); err != nil || watchForwards.started {
		t.Fatal("join started idle queue")
	}
	entered := make(chan struct{})
	q, stop := watchQueueForTest(t, 1, 1, gate, func(ctx context.Context, _ *ext.Context, _ int64, _ int64, _ int, _ int) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	})
	watchForwards = q
	if err := q.enqueue(t.Context(), watchStopRoute(1), 1); err != nil {
		t.Fatal(err)
	}
	watchQueueReceive(t, entered)
	waitCtx, stopWaiting := context.WithCancel(t.Context())
	stopWaiting()
	if err := WaitWatchForwards(waitCtx); !errors.Is(err, context.Canceled) || q.shared.Err() != nil {
		t.Fatal("join cancelled shared client")
	}
	stop()
	if err := WaitWatchForwards(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s := q.snapshot(); s.Running != 0 || !s.Closed {
		t.Fatal("join returned with workers active")
	}
}

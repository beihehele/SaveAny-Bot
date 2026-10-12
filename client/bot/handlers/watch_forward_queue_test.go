package handlers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"

	"github.com/krau/SaveAny-Bot/database"
)

func watchQueueForTest(t *testing.T, workers, capacity int, gate *watchRouteAdmission, forward watchForwardFunc) (*watchForwardQueue, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	client := &ext.Context{Context: ctx}
	q := newWatchForwardQueue(workers, capacity, gate, func() *ext.Context { return client }, forward)
	t.Cleanup(func() {
		cancel()
		q.shutdown()
		select {
		case <-q.done:
		case <-time.After(10 * time.Second):
			t.Error("forward workers did not stop")
		}
	})
	return q, cancel
}

func watchQueueWait(t *testing.T, q *watchForwardQueue, predicate func(WatchForwardStats) bool) WatchForwardStats {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		s := q.snapshot()
		if s.Accepted != uint64(s.Pending+s.Running)+s.Succeeded+s.Failed+s.Cancelled+s.Interrupted+s.DiscardedStopped+s.DiscardedShutdown || s.Pending > s.Capacity || s.Running > s.Workers {
			t.Fatalf("inconsistent or unbounded forward snapshot: %+v", s)
		}
		if predicate(s) {
			return s
		}
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatalf("forward queue did not reach expected state: %+v", s)
		}
	}
}

func watchQueueReceive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(10 * time.Second):
		t.Fatal("forward did not reach expected boundary")
		var empty T
		return empty
	}
}

func TestWatchForwardQueueBoundsTargetOrderAndOtherTargets(t *testing.T) {
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
	type call struct {
		source, target int64
		message, topic int
	}
	entered := make(chan call, 10)
	releases := map[int]chan struct{}{}
	for _, id := range []int{1, 2, 3, 4, 5} {
		releases[id] = make(chan struct{})
	}
	q, _ := watchQueueForTest(t, 2, 3, gate, func(ctx context.Context, _ *ext.Context, source, target int64, msg, topic int) error {
		entered <- call{source, target, msg, topic}
		select {
		case <-releases[msg]:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	route := watchStopRoute(1)
	add := func(ctx context.Context, msg int, target int64, topic int) {
		t.Helper()
		r := route
		r.TargetID = target
		r.TargetTopicID = topic
		if err := q.enqueue(ctx, r, msg); err != nil {
			t.Fatal(err)
		}
	}
	add(t.Context(), 1, 20, 1)
	if got := watchQueueReceive(t, entered); got != (call{10, 20, 1, 1}) {
		t.Fatal(got)
	}
	request, cancelRequest := context.WithCancel(t.Context())
	var alias constant.TDLibPeerID
	alias.Channel(20)
	add(request, 2, int64(alias), 2)
	cancelRequest() // The shared-client lifetime owns already-enqueued work.
	add(t.Context(), 4, 30, 3)
	if got := watchQueueReceive(t, entered); got != (call{10, 30, 4, 3}) {
		t.Fatalf("busy target blocked another chat: %+v", got)
	}
	add(t.Context(), 3, 20, 1)
	add(t.Context(), 5, 40, 4)
	watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Running == 2 && s.Pending == 3 })
	var callers sync.WaitGroup
	for range 50 {
		callers.Go(func() {
			r := route
			r.TargetID = 99
			if err := q.enqueue(t.Context(), r, 99); !errors.Is(err, errWatchForwardFull) {
				t.Errorf("full queue blocked or accepted work: %v", err)
			}
		})
	}
	completed := make(chan struct{})
	go func() { callers.Wait(); close(completed) }()
	watchQueueReceive(t, completed) // Both RPCs are still blocked here.
	s := q.snapshot()
	if s.Accepted != 5 || s.RejectedFull != 50 || s.Started != 2 {
		t.Fatalf("wrong overload accounting: %+v", s)
	}
	close(releases[1])
	if got := watchQueueReceive(t, entered); got != (call{10, int64(alias), 2, 2}) {
		t.Fatalf("topic FIFO or caller lifetime changed: %+v", got)
	}
	close(releases[2])
	if got := watchQueueReceive(t, entered); got != (call{10, 20, 3, 1}) {
		t.Fatalf("same-target order changed: %+v", got)
	}
	close(releases[3])
	if got := watchQueueReceive(t, entered); got != (call{10, 40, 5, 4}) {
		t.Fatal(got)
	}
	close(releases[4])
	close(releases[5])
	s = watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Pending == 0 && s.Running == 0 })
	if s.Succeeded != 5 || s.Started != 5 || s.Failed+s.Cancelled+s.Interrupted != 0 {
		t.Fatalf("accepted work lost or repeated: %+v", s)
	}
}

func TestWatchForwardQueueStopRecreatePreservesStartedWork(t *testing.T) {
	active := map[uint]bool{1: true, 2: true}
	gate := &watchRouteAdmission{active: func(_ context.Context, r database.WatchChat) (bool, error) { return active[r.ID], nil }}
	entered := make(chan int, 5)
	release := make(chan struct{})
	q, _ := watchQueueForTest(t, 1, 4, gate, func(ctx context.Context, _ *ext.Context, _, _ int64, msg, _ int) error {
		entered <- msg
		if msg == 1 {
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	old, survivor := watchStopRoute(1), watchStopRoute(2)
	if err := q.enqueue(t.Context(), old, 1); err != nil {
		t.Fatal(err)
	}
	if got := watchQueueReceive(t, entered); got != 1 {
		t.Fatal(got)
	}
	for _, job := range []watchForwardJob{{old, 2}, {survivor, 3}} {
		if err := q.enqueue(t.Context(), job.route, job.messageID); err != nil {
			t.Fatal(err)
		}
	}
	stopDone := make(chan error, 1)
	go func() {
		stopDone <- gate.stop(t.Context(), func(context.Context) error { active[1] = false; return nil }, func() { q.dropRoute(1) })
	}()
	if err := watchQueueReceive(t, stopDone); err != nil {
		t.Fatal(err)
	}
	if s := q.snapshot(); s.Pending != 1 || s.Running != 1 || s.DiscardedStopped != 1 || q.shared.Err() != nil {
		t.Fatalf("stop affected another route or launched RPC: %+v", s)
	}
	if err := q.enqueue(t.Context(), old, 9); !errors.Is(err, errWatchRouteStopped) {
		t.Fatal(err)
	}
	replacement := watchStopRoute(3)
	gate.mu.Lock()
	active[3] = true
	gate.mu.Unlock()
	if err := q.enqueue(t.Context(), replacement, 4); err != nil {
		t.Fatal(err)
	}
	close(release)
	if got := watchQueueReceive(t, entered); got != 3 {
		t.Fatalf("stopped pending job or reordered survivor: %d", got)
	}
	if got := watchQueueReceive(t, entered); got != 4 {
		t.Fatalf("replacement inherited old work: %d", got)
	}
	s := watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Running == 0 && s.Pending == 0 })
	if s.Accepted != 4 || s.Started != 3 || s.Succeeded != 3 || s.DiscardedStopped != 1 {
		t.Fatal(s)
	}
}

func TestWatchForwardQueueRechecksReservedWorkAfterStop(t *testing.T) {
	active := true
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return active, nil }}
	entered := make(chan int, 2)
	releaseRPC := make(chan struct{})
	q, _ := watchQueueForTest(t, 1, 2, gate, func(ctx context.Context, _ *ext.Context, _, _ int64, msg, _ int) error {
		entered <- msg
		select {
		case <-releaseRPC:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	route := watchStopRoute(1)
	if err := q.enqueue(t.Context(), route, 1); err != nil {
		t.Fatal(err)
	}
	watchQueueReceive(t, entered)
	if err := q.enqueue(t.Context(), route, 2); err != nil {
		t.Fatal(err)
	}
	removing, allowRemove, stopped := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(allowRemove) }) }
	defer unblock()
	go func() {
		stopped <- gate.stop(t.Context(), func(context.Context) error { close(removing); <-allowRemove; active = false; return nil }, func() { q.dropRoute(1) })
	}()
	watchQueueReceive(t, removing) // Stop owns the gate, not the queue mutex.
	close(releaseRPC)
	watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Pending == 0 && s.Running == 1 && s.Succeeded == 1 })
	unblock()
	if err := watchQueueReceive(t, stopped); err != nil {
		t.Fatal(err)
	}
	s := watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Running == 0 })
	if s.Started != 1 || s.DiscardedStopped != 1 || s.Accepted != 2 {
		t.Fatalf("reserved item crossed stop: %+v", s)
	}
	select {
	case msg := <-entered:
		t.Fatalf("stopped item reached transport: %d", msg)
	default:
	}
}

func TestWatchForwardQueueShutdownAndFailureRelease(t *testing.T) {
	t.Run("final database check fails closed and releases slot", func(t *testing.T) {
		checks := 0
		gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) {
			checks++
			if checks == 2 {
				return false, errors.New("synthetic database unavailable")
			}
			return true, nil
		}}
		calls := 0
		q, _ := watchQueueForTest(t, 1, 2, gate, func(context.Context, *ext.Context, int64, int64, int, int) error { calls++; return nil })
		if err := q.enqueue(t.Context(), watchStopRoute(1), 1); err != nil {
			t.Fatal(err)
		}
		watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Failed == 1 && s.Running == 0 })
		if err := q.enqueue(t.Context(), watchStopRoute(2), 2); err != nil {
			t.Fatal(err)
		}
		s := watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Succeeded == 1 && s.Running == 0 })
		if calls != 1 || s.Accepted != 2 || s.Started != 1 || q.shared.Err() != nil {
			t.Fatalf("failed check reached transport or retained slot: %+v", s)
		}
	})
	t.Run("shutdown clears waiting jobs", func(t *testing.T) {
		gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
		entered := make(chan struct{}, 1)
		q, cancel := watchQueueForTest(t, 1, 3, gate, func(ctx context.Context, _ *ext.Context, _, _ int64, _, _ int) error {
			entered <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		})
		route := watchStopRoute(1)
		if err := q.enqueue(t.Context(), route, 1); err != nil {
			t.Fatal(err)
		}
		watchQueueReceive(t, entered)
		for _, id := range []int{2, 3} {
			if err := q.enqueue(t.Context(), route, id); err != nil {
				t.Fatal(err)
			}
		}
		cancel()
		watchQueueReceive(t, q.done)
		s := q.snapshot()
		if !s.Closed || s.Pending != 0 || s.Running != 0 || s.Accepted != 3 || s.DiscardedShutdown != 2 || s.Cancelled != 1 {
			t.Fatalf("shutdown leaked/replayed jobs: %+v", s)
		}
		if err := q.enqueue(t.Context(), route, 4); !errors.Is(err, errWatchForwardClosed) {
			t.Fatal(err)
		}
	})
	t.Run("internal and ordinary failures do not block next item", func(t *testing.T) {
		gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
		var calls int
		q, _ := watchQueueForTest(t, 1, 3, gate, func(_ context.Context, _ *ext.Context, _, _ int64, msg, _ int) error {
			calls++
			switch msg {
			case 1:
				return context.DeadlineExceeded
			case 2:
				return errors.New("synthetic RPC failure")
			default:
				return nil
			}
		})
		for _, id := range []int{1, 2, 3} {
			if err := q.enqueue(t.Context(), watchStopRoute(1), id); err != nil {
				t.Fatal(err)
			}
		}
		s := watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Accepted == 3 && s.Running == 0 && s.Pending == 0 })
		if s.Interrupted != 1 || s.Failed != 1 || s.Succeeded != 1 || s.Started != 3 || calls != 3 || q.shared.Err() != nil {
			t.Fatalf("failure retained slot or retried: %+v calls=%d", s, calls)
		}
	})
	t.Run("unavailable client and observation do not launch workers", func(t *testing.T) {
		gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
		q := newWatchForwardQueue(4, 100, gate, func() *ext.Context { return nil }, nil)
		defer q.shutdown()
		if err := q.enqueue(t.Context(), watchStopRoute(1), 1); !errors.Is(err, errWatchForwardUnavailable) {
			t.Fatal(err)
		}
		if s := q.snapshot(); s.Accepted+s.Started != 0 || s.RejectedUnavailable != 1 || q.started {
			t.Fatal(s)
		}
	})
}

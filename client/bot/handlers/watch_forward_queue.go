package handlers

import (
	"context"
	"errors"
	"sync"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/constant"

	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/database"
)

const (
	watchForwardWorkers  = 4
	watchForwardCapacity = 100
)

var (
	errWatchForwardFull        = errors.New("watch forward queue full")
	errWatchForwardClosed      = errors.New("watch forward queue closed")
	errWatchForwardUnavailable = errors.New("watch forward userbot unavailable")
)

// WatchForwardStats counts logical messages/albums, not media files. Running
// includes slots performing the final route check. Values are an atomic,
// in-memory snapshot; success does not prove album completeness or caption edit.
type WatchForwardStats struct {
	Pending, Running, Capacity, Workers                          int
	Accepted, Started, Succeeded, Failed, Cancelled, Interrupted uint64
	RejectedFull, RejectedClosed, RejectedUnavailable            uint64
	DiscardedStopped, DiscardedShutdown                          uint64
	Closed                                                       bool
}

type watchForwardJob struct {
	route     database.WatchChat
	messageID int
}

type watchForwardFunc func(context.Context, *ext.Context, int64, int64, int, int) error

// watchForwardQueue reserves at most workers slots and capacity waiting jobs.
// Only watch forwards participate; copy and the local-save queue stay separate.
// The route gate always precedes mu. Workers release mu before taking the gate
// and release the gate before any RPC, so neither stop nor ingress waits on RPC.
type watchForwardQueue struct {
	mu          sync.Mutex
	changed     *sync.Cond
	gate        *watchRouteAdmission
	client      func() *ext.Context
	forward     watchForwardFunc
	shared      *ext.Context
	pending     []watchForwardJob
	busy        map[int64]bool
	stats       WatchForwardStats
	started     bool
	workersLeft int
	done        chan struct{}
}

func newWatchForwardQueue(workers, capacity int, gate *watchRouteAdmission, client func() *ext.Context, forward watchForwardFunc) *watchForwardQueue {
	q := &watchForwardQueue{gate: gate, client: client, forward: forward,
		busy: make(map[int64]bool), done: make(chan struct{}),
		stats: WatchForwardStats{Workers: max(1, workers), Capacity: max(1, capacity)}}
	q.changed = sync.NewCond(&q.mu)
	return q
}

var watchForwards = newWatchForwardQueue(watchForwardWorkers, watchForwardCapacity, watchRoutes, userclient.GetCtx, userclient.ForwardMessage)

// GetWatchForwardStats observes the queue without starting workers or RPCs.
func GetWatchForwardStats() WatchForwardStats { return watchForwards.snapshot() }

// WaitWatchForwards joins already started forwarding workers at shutdown. It
// does not start workers or cancel the shared Telegram client.
func WaitWatchForwards(ctx context.Context) error {
	q := watchForwards
	q.mu.Lock()
	started, done := q.started, q.done
	q.mu.Unlock()
	if !started {
		return nil
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *watchForwardQueue) snapshot() WatchForwardStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	s := q.stats
	s.Pending = len(q.pending)
	return s
}

// enqueue is non-blocking with respect to capacity and forwarding. The caller
// context controls admission only; accepted work uses the shared client lifetime.
func (q *watchForwardQueue) enqueue(ctx context.Context, route database.WatchChat, messageID int) error {
	return q.gate.run(ctx, route, func() error {
		q.mu.Lock()
		defer q.mu.Unlock()
		if q.stats.Closed {
			q.stats.RejectedClosed++
			return errWatchForwardClosed
		}
		if !q.started {
			client := q.client()
			if client == nil || client.Context == nil {
				q.stats.RejectedUnavailable++
				return errWatchForwardUnavailable
			}
			if err := client.Err(); err != nil {
				q.stats.RejectedClosed++
				return err
			}
			q.shared = client
			q.started = true
			q.workersLeft = q.stats.Workers
			for range q.stats.Workers {
				go q.worker()
			}
			context.AfterFunc(client, q.shutdown)
		}
		if err := q.shared.Err(); err != nil {
			q.stats.RejectedClosed++
			return err
		}
		if len(q.pending) >= q.stats.Capacity {
			q.stats.RejectedFull++
			return errWatchForwardFull
		}
		q.pending = append(q.pending, watchForwardJob{route: route, messageID: messageID})
		q.stats.Accepted++
		q.changed.Broadcast()
		return nil
	})
}

func (q *watchForwardQueue) next() (watchForwardJob, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for !q.stats.Closed {
		if q.shared.Err() != nil {
			q.shutdownLocked()
			return watchForwardJob{}, false
		}
		for i, job := range q.pending {
			// Serialize the entire target chat, including distinct Topics. Skip
			// busy targets so they do not occupy workers needed by other chats.
			key := watchForwardTargetKey(job.route.TargetID)
			if q.busy[key] {
				continue
			}
			copy(q.pending[i:], q.pending[i+1:])
			q.pending[len(q.pending)-1] = watchForwardJob{}
			q.pending = q.pending[:len(q.pending)-1]
			q.busy[key] = true
			q.stats.Running++
			return job, true
		}
		q.changed.Wait()
	}
	return watchForwardJob{}, false
}

func (q *watchForwardQueue) worker() {
	defer func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		q.workersLeft--
		if q.workersLeft == 0 {
			close(q.done)
		}
	}()
	for {
		job, ok := q.next()
		if !ok {
			return
		}
		err := q.gate.run(q.shared, job.route, func() error {
			q.mu.Lock()
			defer q.mu.Unlock()
			if q.stats.Closed {
				return errWatchForwardClosed
			}
			// This marks the start boundary under the same lock as /unwatch.
			q.stats.Started++
			return nil
		})
		if err == nil {
			err = q.forward(q.shared, q.shared, job.route.ChatID, job.route.TargetID, job.messageID, job.route.TargetTopicID)
		}
		q.finish(job, err)
		if err != nil {
			logger := log.FromContext(q.shared)
			if errors.Is(err, errWatchRouteStopped) || errors.Is(err, errWatchForwardClosed) || q.shared.Err() != nil {
				logger.Debug("Watch forward stopped before or during execution", "route_id", job.route.ID, "message_id", job.messageID, "error", err)
			} else {
				logger.Error("Watch forward or final route check failed", "route_id", job.route.ID,
					"source_chat_id", job.route.ChatID, "target_chat_id", job.route.TargetID,
					"topic_id", job.route.TargetTopicID, "message_id", job.messageID, "error", err)
			}
		}
	}
}

func (q *watchForwardQueue) finish(job watchForwardJob, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.busy, watchForwardTargetKey(job.route.TargetID))
	q.stats.Running--
	switch {
	case err == nil:
		q.stats.Succeeded++
	case errors.Is(err, errWatchRouteStopped):
		q.stats.DiscardedStopped++
	case errors.Is(err, errWatchForwardClosed):
		q.stats.DiscardedShutdown++
	case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		if q.shared.Err() != nil {
			q.stats.Cancelled++
		} else {
			q.stats.Interrupted++
		}
	default:
		q.stats.Failed++
	}
	q.changed.Broadcast()
}

// Legacy routes can retain plain IDs while newer routes use Bot API IDs.
// Conservatively serialize equal plain numbers across peer namespaces rather
// than resolving peers/RPCs under the queue lock. The original IDs still reach
// ForwardMessage unchanged; unrelated equal-number peers may share a slot.
func watchForwardTargetKey(id int64) int64 { return constant.TDLibPeerID(id).ToPlain() }

func (q *watchForwardQueue) dropRoute(id uint) {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := 0
	for _, job := range q.pending {
		if job.route.ID == id {
			q.stats.DiscardedStopped++
			continue
		}
		q.pending[kept] = job
		kept++
	}
	clear(q.pending[kept:])
	q.pending = q.pending[:kept]
	q.changed.Broadcast()
}

func (q *watchForwardQueue) shutdown() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.shutdownLocked()
}

func (q *watchForwardQueue) shutdownLocked() {
	if q.stats.Closed {
		return
	}
	q.stats.Closed = true
	q.stats.DiscardedShutdown += uint64(len(q.pending))
	clear(q.pending)
	q.pending = nil
	q.changed.Broadcast()
	if !q.started {
		close(q.done)
	}
}

func enqueueWatchForward(ctx context.Context, route database.WatchChat, messageID int) {
	err := watchForwards.enqueue(ctx, route, messageID)
	if errors.Is(err, errWatchForwardFull) {
		log.FromContext(ctx).Warn("Watch forward queue full; message or album rejected",
			"route_id", route.ID, "source_chat_id", route.ChatID, "target_chat_id", route.TargetID,
			"topic_id", route.TargetTopicID, "message_id", messageID, "rejected_total", watchForwards.snapshot().RejectedFull)
	} else {
		logWatchAdmissionError(ctx, route, err)
	}
}

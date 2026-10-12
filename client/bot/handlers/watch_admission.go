package handlers

import (
	"context"
	"errors"
	"sync"

	"github.com/charmbracelet/log"

	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
)

var errWatchRouteStopped = errors.New("watch route stopped")

// watchRouteAdmission serializes deletion with buffer insertion and final
// admission. Callbacks must only insert buffers, enqueue tasks or launch work;
// file preparation and Telegram RPCs must run outside this lock.
type watchRouteAdmission struct {
	mu     sync.Mutex
	active func(context.Context, database.WatchChat) (bool, error)
}

var watchRoutes = &watchRouteAdmission{active: database.WatchRouteActive}

func (a *watchRouteAdmission) run(ctx context.Context, route database.WatchChat, accept func() error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	active, err := a.active(ctx, route)
	if err != nil {
		return err
	}
	if !active {
		return errWatchRouteStopped
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return accept()
}

func (a *watchRouteAdmission) stop(ctx context.Context, remove func(context.Context) error, drop func()) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := remove(ctx); err != nil {
		return err
	}
	// Clear only after successful deletion, before a waiting admission can run.
	drop()
	return nil
}

func stopWatchRoute(ctx context.Context, user *database.User, id uint) error {
	return watchRoutes.stop(ctx, func(ctx context.Context) error {
		return user.UnwatchByID(ctx, id)
	}, func() {
		watchForwardAlbumBuf.dropRoute(id)
		watchMediaGroupMgr.dropRoute(id)
	})
}

func watchRouteEnqueue(route database.WatchChat) watchAlbumEnqueue {
	return watchRoutes.enqueue(route, core.AddTasks)
}

func (a *watchRouteAdmission) enqueue(route database.WatchChat, enqueue watchAlbumEnqueue) watchAlbumEnqueue {
	return func(ctx context.Context, tasks ...core.Executable) error {
		return a.run(ctx, route, func() error { return enqueue(ctx, tasks...) })
	}
}

func logWatchAdmissionError(ctx context.Context, route database.WatchChat, err error) {
	if errors.Is(err, errWatchRouteStopped) {
		log.FromContext(ctx).Debug("Skip stopped watch route", "route_id", route.ID)
	} else if err != nil {
		log.FromContext(ctx).Error("Watch route admission failed", "route_id", route.ID, "error", err)
	}
}

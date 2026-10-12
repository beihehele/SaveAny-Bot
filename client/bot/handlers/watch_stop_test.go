package handlers

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func watchStopRoute(id uint) database.WatchChat {
	return database.WatchChat{Model: gorm.Model{ID: id}, UserID: 1, ChatID: 10, TargetID: 20, TargetTopicID: 2}
}

func watchStopBuffers(t *testing.T) (*forwardAlbumBuffer, *watchMediaGroupHandler) {
	t.Helper()
	fwd := newForwardAlbumBuffer(func(database.WatchChat, []int) { t.Error("unexpected flush") })
	local := &watchMediaGroupHandler{groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup), timers: make(map[watchLocalAlbumKey]*mediaTimer)}
	t.Cleanup(func() {
		for _, id := range []uint{1, 2, 3} {
			fwd.dropRoute(id)
			local.dropRoute(id)
		}
	})
	return fwd, local
}

func TestWatchStopIsolatesRoutesAndObsoleteTimers(t *testing.T) {
	fwd, local := watchStopBuffers(t)
	for _, id := range []uint{1, 2} {
		route := watchStopRoute(id) // Same source, target, topic, user and album.
		for _, msg := range []int{3, 1, 2} {
			fwd.add(route, 99, msg, msg == 3, time.Hour)
			local.addFile(route, 99, nil, msg == 3, time.Hour, func([]tfile.TGFileMessage) { t.Error("unexpected flush") })
		}
	}
	fkey := forwardAlbumKey{RouteID: 1, SourceID: 10, TargetID: 20, TargetTopicID: 2, GroupedID: 99}
	lkey := watchLocalAlbumKey{RouteID: 1, ChatID: 10, UserID: 1, GroupedID: 99}
	oldF, oldL := fwd.timers[fkey], local.timers[lkey]
	fwd.dropRoute(1)
	local.dropRoute(1)
	if len(fwd.groups) != 1 || len(fwd.timers) != 1 || len(local.groups) != 1 || len(local.timers) != 1 {
		t.Fatal("stop removed other route or left stopped buffer")
	}
	// Reuse the original key to exercise an already running obsolete timer.
	fwd.add(watchStopRoute(1), 99, 9, false, time.Hour)
	local.addFile(watchStopRoute(1), 99, nil, false, time.Hour, func([]tfile.TGFileMessage) { t.Error("unexpected flush") })
	if fwd.takeGroup(fkey, oldF) != nil || local.takeGroup(lkey, oldL) != nil {
		t.Fatal("old timer consumed replacement buffer")
	}
	fwd.timers[fkey].Stop()
	local.timers[lkey].Stop()
	fg := fwd.takeGroup(fkey, fwd.timers[fkey])
	lg := local.takeGroup(lkey, local.timers[lkey])
	if fg == nil || !reflect.DeepEqual(fg.ids, []int{9}) || fg.matched || lg == nil || len(lg.files) != 1 || lg.matched {
		t.Fatal("replacement inherited old members/filter")
	}
	fkey.RouteID, lkey.RouteID = 2, 2
	fwd.timers[fkey].Stop()
	local.timers[lkey].Stop()
	fg = fwd.takeGroup(fkey, fwd.timers[fkey])
	lg = local.takeGroup(lkey, local.timers[lkey])
	if fg == nil || !reflect.DeepEqual(fg.ids, []int{3, 1, 2}) || !fg.matched || lg == nil || len(lg.files) != 3 || !lg.matched {
		t.Fatal("stop changed surviving route members/filter/order")
	}
	// takeGroup removes ownership but not the scheduled timer itself.
	oldF.Stop()
	oldL.Stop()
}

func TestWatchStopRejectsTakenAlbumAndStaleSnapshot(t *testing.T) {
	for _, mode := range []string{"forward", "local"} {
		t.Run(mode, func(t *testing.T) {
			active := true // Only accessed under the admission lock.
			gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return active, nil }}
			route := watchStopRoute(1)
			taken, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			done := make(chan error, 1)
			var accepted atomic.Int32
			flush := func() {
				close(taken) // Timer has removed the group, but admission has not started.
				<-release
				done <- gate.run(t.Context(), route, func() error { accepted.Add(1); return nil })
			}
			var drop func()
			if mode == "forward" {
				buf := newForwardAlbumBuffer(func(database.WatchChat, []int) { flush() })
				buf.add(route, 99, 1, true, 0)
				drop = func() { buf.dropRoute(route.ID) }
			} else {
				mgr := &watchMediaGroupHandler{groups: make(map[watchLocalAlbumKey]*watchLocalAlbumGroup), timers: make(map[watchLocalAlbumKey]*mediaTimer)}
				mgr.addFile(route, 99, nil, true, 0, func([]tfile.TGFileMessage) { flush() })
				drop = func() { mgr.dropRoute(route.ID) }
			}
			select {
			case <-taken:
			case <-time.After(3 * time.Second):
				t.Fatal("timer did not take group")
			}
			if err := gate.stop(t.Context(), func(context.Context) error { active = false; return nil }, drop); err != nil {
				t.Fatal(err)
			}
			unblock()
			select {
			case err := <-done:
				if !errors.Is(err, errWatchRouteStopped) || accepted.Load() != 0 {
					t.Fatalf("taken album was admitted after stop: %v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("flush did not finish")
			}
			if err := gate.run(t.Context(), route, func() error { t.Error("stale snapshot inserted buffer"); return nil }); !errors.Is(err, errWatchRouteStopped) {
				t.Fatalf("stale snapshot: %v", err)
			}
		})
	}
}

func TestWatchStopFailuresAndCancellation(t *testing.T) {
	route := watchStopRoute(1)
	wantErr := errors.New("storage unavailable")
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return true, nil }}
	if err := gate.stop(t.Context(), func(context.Context) error { return wantErr }, func() { t.Error("failed removal dropped buffers") }); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	if err := gate.run(t.Context(), route, func() error { return nil }); err != nil {
		t.Fatalf("failed removal disabled route: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := gate.stop(ctx, func(context.Context) error { t.Error("cancelled removal ran"); return nil }, func() { t.Error("cancelled removal dropped buffers") }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := gate.run(ctx, route, func() error { t.Error("cancelled admission ran"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	gate.active = func(context.Context, database.WatchChat) (bool, error) { return false, wantErr }
	if err := gate.run(t.Context(), route, func() error { t.Error("failed lookup admitted work"); return nil }); !errors.Is(err, wantErr) {
		t.Fatal(err)
	}
	gate.active = func(context.Context, database.WatchChat) (bool, error) { return true, nil }
	if err := gate.run(t.Context(), route, func() error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatal("enqueue failure hidden")
	}
}

func TestWatchStopPreservesAdmittedTasksAndRejectsPreparedAlbum(t *testing.T) {
	ctx, _ := watchAdmissionContext(t)
	qe, enqueue, groups := watchAdmissionQueue(t, ctx, false)
	active := true
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return active, nil }}
	route := watchStopRoute(1)
	guarded := gate.enqueue(route, enqueue)
	user := &database.User{ChatID: 123}
	files := func() []tfile.TGFileMessage {
		return []tfile.TGFileMessage{watchAdmissionFile(t, 1, 99, "a.mp4", "album"), watchAdmissionFile(t, 2, 99, "b.mp4", "")}
	}
	processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "", files(), guarded)
	if qe.ActiveLength() != 2 || len(*groups) != 1 || len((*groups)[0]) != 2 {
		t.Fatal("normal album admission changed")
	}
	running, err := qe.Get()
	if err != nil {
		t.Fatal(err)
	}
	// Preparation completes before this callback; stop must guard final admission.
	processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "", files(), func(parent context.Context, tasks ...core.Executable) error {
		if len(tasks) != 2 {
			t.Fatal("prepared album was split")
		}
		if err := gate.stop(t.Context(), func(context.Context) error { active = false; return nil }, func() {}); err != nil {
			t.Fatal(err)
		}
		return guarded(parent, tasks...)
	})
	if qe.ActiveLength() != 1 || len(qe.RunningTasks()) != 1 || len(*groups) != 1 || running.Context().Err() != nil || ctx.Err() != nil {
		t.Fatal("stop cancelled accepted work, admitted late work, or cancelled shared context")
	}
	qe.Done(running.ID)
	queued, err := qe.Get()
	if err != nil || queued.Context().Err() != nil {
		t.Fatalf("queued task cancelled: %v", err)
	}
	qe.Done(queued.ID)
	if qe.ActiveLength() != 0 {
		t.Fatal("accepted tasks failed to finish normally")
	}
}

func TestWatchStopDoesNotWaitForOrCancelLaunchedForward(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	active := true
	gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return active, nil }}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	if err := gate.run(ctx, watchStopRoute(1), func() error {
		go func() {
			close(started)
			<-release // Model a blocked RPC without touching Telegram.
			finished <- ctx.Err()
		}()
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("forward did not start")
	}
	stopped := make(chan error, 1)
	go func() {
		stopped <- gate.stop(ctx, func(context.Context) error { active = false; return nil }, func() {})
	}()
	select {
	case err := <-stopped:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop waited for launched RPC")
	}
	unblock()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("stop cancelled launched RPC/shared context: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("forward failed to finish")
	}
}

func TestWatchAdmissionRacingStop(t *testing.T) {
	for range 50 {
		fwd, local := watchStopBuffers(t)
		active := true
		gate := &watchRouteAdmission{active: func(context.Context, database.WatchChat) (bool, error) { return active, nil }}
		route := watchStopRoute(1)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range 8 {
			wg.Go(func() {
				<-start
				err := gate.run(t.Context(), route, func() error {
					fwd.add(route, 99, i, true, time.Hour)
					local.addFile(route, 99, nil, true, time.Hour, func([]tfile.TGFileMessage) { t.Error("unexpected flush") })
					return nil
				})
				if err != nil && !errors.Is(err, errWatchRouteStopped) {
					t.Error(err)
				}
			})
		}
		wg.Go(func() {
			<-start
			if err := gate.stop(t.Context(), func(context.Context) error { active = false; return nil }, func() {
				fwd.dropRoute(1)
				local.dropRoute(1)
			}); err != nil {
				t.Error(err)
			}
		})
		close(start)
		wg.Wait()
		if len(fwd.groups)+len(fwd.timers)+len(local.groups)+len(local.timers) != 0 {
			t.Fatal("buffer insertion survived successful stop")
		}
	}
}

func TestWatchStopWithDatabaseAndApplicationQueue(t *testing.T) {
	const env = "SAVEANY_WATCH_STOP_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestWatchStopWithDatabaseAndApplicationQueue$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated watch stop integration: %v\n%s", err, output)
		}
		return
	}
	// Exercise the real singleton wiring in a separate process with a private
	// database and config-based users, without Bot/Userbot sessions or network.
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[[users]]\nid=42\n[[users]]\nid=43\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	core.Prepare()
	user, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.GetUserByChatID(t.Context(), 43)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []*database.User{user, other} {
		if err := owner.WatchChat(t.Context(), database.WatchChat{ChatID: 10, TargetID: 20, TargetTopicID: 2}); err != nil {
			t.Fatal(err)
		}
		watchForwardAlbumBuf.add(owner.WatchChats[0], 99, 1, true, time.Hour)
		defer watchForwardAlbumBuf.dropRoute(owner.WatchChats[0].ID)
	}
	fwd := user.WatchChats[0]
	if err := stopWatchRoute(t.Context(), other, fwd.ID); !errors.Is(err, gorm.ErrRecordNotFound) || len(watchForwardAlbumBuf.groups) != 2 {
		t.Fatalf("foreign stop changed buffers: %v", err)
	}
	if err := stopWatchRoute(t.Context(), user, fwd.ID); err != nil || len(watchForwardAlbumBuf.groups) != 1 {
		t.Fatalf("forward stop wiring: %v", err)
	}
	if err := watchRoutes.run(t.Context(), fwd, func() error { t.Error("stale real route admitted work"); return nil }); !errors.Is(err, errWatchRouteStopped) {
		t.Fatal(err)
	}
	if err := user.WatchChat(t.Context(), database.WatchChat{ChatID: 10}); err != nil {
		t.Fatal(err)
	}
	local := user.WatchChats[len(user.WatchChats)-1]
	watchMediaGroupMgr.addFile(local, 99, nil, true, time.Hour, func([]tfile.TGFileMessage) { t.Error("unexpected local flush") })
	defer watchMediaGroupMgr.dropRoute(local.ID)
	ctx, _ := watchAdmissionContext(t)
	files := []tfile.TGFileMessage{watchAdmissionFile(t, 1, 99, "a.mp4", "caption"), watchAdmissionFile(t, 2, 99, "b.mp4", "")}
	processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "", files, watchRouteEnqueue(local))
	if core.GetLength(ctx) != 2 {
		t.Fatal("actual application queue did not admit complete album")
	}
	if err := stopWatchRoute(ctx, user, local.ID); err != nil || len(watchMediaGroupMgr.groups) != 0 {
		t.Fatalf("local stop wiring: %v", err)
	}
	if err := watchRouteEnqueue(local)(ctx); !errors.Is(err, errWatchRouteStopped) {
		t.Fatal("removed local route could still submit")
	}
	if tasks := core.GetQueuedTasks(ctx); len(tasks) != 2 || tasks[0].Cancelled || tasks[1].Cancelled {
		t.Fatal("actual stop cancelled previously admitted tasks")
	}
	for _, task := range core.GetQueuedTasks(ctx) {
		if err := core.CancelTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := user.WatchChat(t.Context(), database.WatchChat{ChatID: 10}); err != nil {
		t.Fatal(err)
	}
	replacement := user.WatchChats[len(user.WatchChats)-1]
	if replacement.ID == local.ID {
		t.Fatal("replacement reused identity")
	}
	if err := watchRoutes.run(ctx, replacement, func() error {
		watchMediaGroupMgr.addFile(replacement, 99, nil, false, time.Hour, func([]tfile.TGFileMessage) { t.Error("unexpected replacement flush") })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	defer watchMediaGroupMgr.dropRoute(replacement.ID)
	if err := stopWatchRoute(ctx, user, local.ID); !errors.Is(err, gorm.ErrRecordNotFound) || len(watchMediaGroupMgr.groups) != 1 {
		t.Fatalf("old stop removed replacement: %v", err)
	}
}

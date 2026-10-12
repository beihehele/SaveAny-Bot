package handlers

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
)

func TestWatchIngressSnapshotSurvivesStopAndRecreate(t *testing.T) {
	const env = "SAVEANY_WATCH_INGRESS_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestWatchIngressSnapshotSurvivesStopAndRecreate$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated watch ingress: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	// Long timers let the test take groups deterministically, without sleeps.
	configuration := "[telegram]\nmedia_group_timeout=3600\n[[storages]]\nname='local'\ntype='local'\nenable=true\nbase_path='downloads'\n[[users]]\nid=42\nstorages=['local']\n[[users]]\nid=43\nstorages=['local']\n"
	if err := os.WriteFile(path, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	storage.LoadStorages(t.Context())
	core.Prepare()
	ctx, _ := watchAdmissionContext(t)
	owner, err := database.GetUserByChatID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.GetUserByChatID(ctx, 43)
	if err != nil {
		t.Fatal(err)
	}
	owner.DefaultStorage = "local"
	if err := database.UpdateUserPreferences(ctx, owner.ChatID, map[string]any{"default_storage": owner.DefaultStorage}); err != nil {
		t.Fatal(err)
	}
	add := func(user *database.User, target int64, topic int) database.WatchChat {
		t.Helper()
		if err := user.WatchChat(ctx, database.WatchChat{ChatID: 100, TargetID: target, TargetTopicID: topic, Filter: "msgre:album-match"}); err != nil {
			t.Fatal(err)
		}
		route := user.WatchChats[len(user.WatchChats)-1]
		t.Cleanup(func() { watchForwardAlbumBuf.dropRoute(route.ID); watchMediaGroupMgr.dropRoute(route.ID) })
		return route
	}
	oldLocal, oldForward, surviving := add(owner, 0, 0), add(owner, 200, 2), add(other, 200, 3)
	capture := func() []database.WatchChat {
		t.Helper()
		routes, err := database.GetWatchChatsByChatID(ctx, 100)
		if err != nil {
			t.Fatal(err)
		}
		snapshot := make([]database.WatchChat, 0, len(routes))
		for _, route := range routes {
			snapshot = append(snapshot, *route)
		}
		return snapshot
	}
	event := func(id int, group int64, caption string, routes []database.WatchChat) userclient.MediaMessageEvent {
		return userclient.MediaMessageEvent{Ctx: ctx, ChatID: 100, MessageID: id,
			File: watchAdmissionFile(t, id, group, "media.mp4", caption), Routes: routes}
	}
	// These complete album members and a single file wait upstream of routing.
	pending := []userclient.MediaMessageEvent{event(42, 77, "album-match", capture()), event(44, 77, "", capture()), event(45, 0, "album-match", capture())}
	for _, route := range []database.WatchChat{oldLocal, oldForward} {
		if err := stopWatchRoute(ctx, owner, route.ID); err != nil {
			t.Fatal(err)
		}
	}
	newLocal, newForward := add(owner, 0, 0), add(owner, 200, 2)
	for _, ev := range pending {
		processWatchMediaMessageEvent(ev)
	}
	if len(watchMediaGroupMgr.groups) != 0 || core.GetLength(ctx) != 0 || len(watchForwardAlbumBuf.groups) != 1 {
		t.Fatal("pending ingress event reached a recreated route or stopped another recipient")
	}
	key := forwardAlbumKey{RouteID: surviving.ID, SourceID: 100, TargetID: 200, TargetTopicID: 3, GroupedID: 77}
	group := watchForwardAlbumBuf.groups[key]
	if group == nil || !group.matched || !reflect.DeepEqual(group.ids, []int{42, 44}) {
		t.Fatal("surviving route lost whole-album filtering, members, topic or order")
	}
	// Fresh events see the replacement and still form a complete album.
	fresh := []userclient.MediaMessageEvent{event(51, 88, "album-match", capture()), event(52, 88, "", capture())}
	for _, ev := range fresh {
		processWatchMediaMessageEvent(ev)
	}
	for _, route := range []database.WatchChat{newForward, surviving} {
		key := forwardAlbumKey{RouteID: route.ID, SourceID: 100, TargetID: 200, TargetTopicID: route.TargetTopicID, GroupedID: 88}
		group := watchForwardAlbumBuf.groups[key]
		if group == nil || !group.matched || !reflect.DeepEqual(group.ids, []int{51, 52}) {
			t.Fatal("fresh album routing or topic isolation changed")
		}
	}
	localKey := watchLocalAlbumKey{RouteID: newLocal.ID, ChatID: 100, UserID: owner.ID, GroupedID: 88}
	timer := watchMediaGroupMgr.timers[localKey]
	if timer == nil {
		t.Fatal("fresh local album missing")
	}
	timer.Stop()
	localGroup := watchMediaGroupMgr.takeGroup(localKey, timer)
	if localGroup == nil || !localGroup.matched || len(localGroup.files) != 2 {
		t.Fatal("fresh local album lost members or filter match")
	}
	processWatchLocalAlbum(ctx, newLocal, localGroup.files)
	if core.GetLength(ctx) != 2 || ctx.Err() != nil || fresh[1].File.Message().Message != "" {
		t.Fatal("local album admission, shared lifetime or original caption changed")
	}
	processWatchMediaMessageEvent(event(53, 0, "album-match", capture()))
	if core.GetLength(ctx) != 3 {
		t.Fatal("fresh single-file admission failed")
	}
	// Missing recipients fail closed rather than querying replacement routes.
	processWatchMediaMessageEvent(event(54, 99, "album-match", nil))
	wrongSource := newLocal
	wrongSource.ChatID = 999
	processWatchMediaMessageEvent(event(55, 100, "album-match", []database.WatchChat{wrongSource}))
	if core.GetLength(ctx) != 3 || len(watchMediaGroupMgr.groups) != 0 {
		t.Fatal("uncaptured/foreign-source event was admitted")
	}
	for _, task := range core.GetQueuedTasks(ctx) {
		if task.Cancelled {
			t.Fatal("route lifecycle cancelled admitted tasks")
		}
		if err := core.CancelTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
}

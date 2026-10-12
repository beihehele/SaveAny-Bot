package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"gorm.io/gorm"

	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

func TestWatchForwardQueueIngressAlbumAndLocalIsolation(t *testing.T) {
	const env = "SAVEANY_WATCH_FORWARD_QUEUE_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestWatchForwardQueueIngressAlbumAndLocalIsolation$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated watch forward pipeline: %v\n%s", err, output)
		}
		return
	}
	// Exercise production routing/flush/stop with a private database and real
	// forwarding function. Only the client/Telegram transport is synthetic.
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[telegram]\nmedia_group_timeout=3600\n[[storages]]\nname='local'\ntype='local'\nenable=true\nbase_path='downloads'\n[[users]]\nid=42\nstorages=['local']\n[[users]]\nid=43\nstorages=['local']\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	cache.Init()
	storage.LoadStorages(t.Context())
	core.Prepare()
	ctx, _ := watchAdmissionContext(t)
	owner, err := database.GetUserByChatID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	owner.DefaultStorage = "local"
	if err := database.UpdateUserPreferences(ctx, owner.ChatID, map[string]any{"default_storage": owner.DefaultStorage}); err != nil {
		t.Fatal(err)
	}
	var source, target constant.TDLibPeerID
	source.Channel(111)
	target.Channel(222)
	addRoute := func(targetID int64, topic int) database.WatchChat {
		t.Helper()
		if err := owner.WatchChat(ctx, database.WatchChat{ChatID: int64(source), TargetID: targetID, TargetTopicID: topic, Filter: "msgre:album-match"}); err != nil {
			t.Fatal(err)
		}
		route := owner.WatchChats[len(owner.WatchChats)-1]
		t.Cleanup(func() { watchForwardAlbumBuf.dropRoute(route.ID); watchMediaGroupMgr.dropRoute(route.ID) })
		return route
	}
	local, old := addRoute(0, 0), addRoute(int64(target), 3)
	files := map[int]tfile.TGFileMessage{}
	for _, id := range []int{50, 51, 52, 54, 55} {
		group := int64(0)
		caption := "album-match held"
		if id >= 54 {
			group = 99
			caption = ""
			if id == 54 {
				caption = "album-match 😀"
			}
		}
		file := watchAdmissionFile(t, id, group, fmt.Sprintf("%d.mp4", id), caption)
		file.Message().PeerID = &tg.PeerChannel{ChannelID: 111}
		if id == 54 {
			file.Message().Entities = []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 11}}
		}
		files[id] = file
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var forwards [][]int
	attributionEdits := 0
	invoker := func(rpcCtx context.Context, input bin.Encoder, output bin.Decoder) error {
		if err := rpcCtx.Err(); err != nil {
			return err
		}
		switch req := input.(type) {
		case *tg.ChannelsGetMessagesRequest:
			var messages []tg.MessageClass
			for _, requested := range req.ID {
				if file := files[requested.(*tg.InputMessageID).ID]; file != nil {
					messages = append(messages, file.Message())
				}
			}
			output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
		case *tg.MessagesForwardMessagesRequest:
			from, fromOK := req.FromPeer.(*tg.InputPeerChannel)
			to, toOK := req.ToPeer.(*tg.InputPeerChannel)
			if !fromOK || !toOK || from.ChannelID != 111 || to.ChannelID != 222 || req.TopMsgID != 3 || !req.DropAuthor {
				t.Errorf("forward route/topic contract changed: %+v", req)
				return errors.New("invalid route")
			}
			forwards = append(forwards, append([]int(nil), req.ID...))
			if len(forwards) == 1 {
				if !reflect.DeepEqual(req.ID, []int{50}) {
					t.Errorf("unexpected first forward: %v", req.ID)
				}
				entered <- struct{}{}
				select {
				case <-release:
				case <-rpcCtx.Done():
					return rpcCtx.Err()
				}
			} else if len(forwards) != 2 || !reflect.DeepEqual(req.ID, []int{54, 55}) {
				t.Errorf("album changed, duplicated or stopped work forwarded: %v", forwards)
			}
			var updates []tg.UpdateClass
			for _, id := range req.ID {
				original := files[id].Message()
				updates = append(updates, &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 100 + id, Message: original.Message, Entities: original.Entities}})
			}
			output.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: updates}
		case *tg.MessagesEditMessageRequest:
			attributionEdits++
			if req.ID == 154 {
				if req.Message != "[转] album-match 😀" || len(req.Entities) != 2 {
					t.Errorf("caption/entities changed: %+v", req)
				} else {
					link, linkOK := req.Entities[0].(*tg.MessageEntityTextURL)
					bold, boldOK := req.Entities[1].(*tg.MessageEntityBold)
					if !linkOK || link.URL != "https://t.me/source/54" || !boldOK || bold.Offset != 4 || bold.Length != 11 {
						t.Errorf("source link/format changed: %+v", req.Entities)
					}
				}
			} else if req.ID != 150 || !strings.HasPrefix(req.Message, "[转] ") {
				t.Errorf("unexpected edit: %+v", req)
			}
			output.(*tg.UpdatesBox).Updates = &tg.Updates{}
		default:
			return fmt.Errorf("unexpected watch RPC %T", input)
		}
		return nil
	}
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
	peers.AddPeer(222, 2, peerstore.TypeChannel, "target")
	service, stopService := context.WithCancel(t.Context())
	client := &ext.Context{Context: service, Raw: tg.NewClient(telegram.InvokeFunc(invoker)), PeerStorage: peers, Self: &tg.User{ID: 9000}}
	previous := watchForwards
	t.Cleanup(func() { watchForwards = previous })
	q := newWatchForwardQueue(2, 1, watchRoutes, func() *ext.Context { return client }, userclient.ForwardMessage)
	watchForwards = q
	t.Cleanup(func() { stopService(); q.shutdown(); watchQueueReceive(t, q.done) })
	if err := q.enqueue(ctx, old, 50); err != nil {
		t.Fatal(err)
	}
	watchQueueReceive(t, entered) // Actual ForwardMessages RPC is blocked.
	if err := q.enqueue(ctx, old, 51); err != nil {
		t.Fatal(err)
	}
	foreign, err := database.GetUserByChatID(ctx, 43)
	if err != nil {
		t.Fatal(err)
	}
	if err := stopWatchRoute(ctx, foreign, old.ID); !errors.Is(err, gorm.ErrRecordNotFound) || q.snapshot().Pending != 1 || q.snapshot().DiscardedStopped != 0 {
		t.Fatalf("foreign/failed deletion removed pending forwards: %v %+v", err, q.snapshot())
	}
	capture := func() []database.WatchChat {
		t.Helper()
		routes, err := database.GetWatchChatsByChatID(ctx, int64(source))
		if err != nil {
			t.Fatal(err)
		}
		values := make([]database.WatchChat, 0, len(routes))
		for _, r := range routes {
			values = append(values, *r)
		}
		return values
	}
	event := func(id int) userclient.MediaMessageEvent {
		return userclient.MediaMessageEvent{Ctx: ctx, ChatID: int64(source), MessageID: id, File: files[id], Routes: capture()}
	}
	processed := make(chan struct{})
	blockedEvent := event(52)
	go func() { processWatchMediaMessageEvent(blockedEvent); close(processed) }()
	watchQueueReceive(t, processed)
	if core.GetLength(ctx) != 1 || q.snapshot().RejectedFull != 1 {
		t.Fatalf("full forwarding queue stalled local save: tasks=%d stats=%+v", core.GetLength(ctx), q.snapshot())
	}
	if err := stopWatchRoute(ctx, owner, old.ID); err != nil {
		t.Fatal(err)
	}
	if s := q.snapshot(); s.Pending != 0 || s.Running != 1 || s.DiscardedStopped != 1 || service.Err() != nil {
		t.Fatalf("actual stop damaged started forward: %+v", s)
	}
	replacement := addRoute(int64(target), 3)
	if replacement.ID == old.ID {
		t.Fatal("route identity reused")
	}
	// Reverse arrivals exercise any-member filtering and unchanged representative
	// selection; the actual RPC must still sort and forward both album members.
	processWatchMediaMessageEvent(event(55))
	processWatchMediaMessageEvent(event(54))
	key := forwardAlbumKey{RouteID: replacement.ID, SourceID: int64(source), TargetID: int64(target), TargetTopicID: 3, GroupedID: 99}
	timer := watchForwardAlbumBuf.timers[key]
	if timer == nil {
		t.Fatal("new album missing")
	}
	timer.Stop()
	group := watchForwardAlbumBuf.takeGroup(key, timer)
	if group == nil || !group.matched || !reflect.DeepEqual(group.ids, []int{55, 54}) {
		t.Fatalf("whole-album filter or arrival order changed: %+v", group)
	}
	watchForwardAlbumBuf.onFlush(replacement, group.ids)
	lkey := watchLocalAlbumKey{RouteID: local.ID, ChatID: int64(source), UserID: owner.ID, GroupedID: 99}
	ltimer := watchMediaGroupMgr.timers[lkey]
	if ltimer == nil {
		t.Fatal("local album missing")
	}
	ltimer.Stop()
	lg := watchMediaGroupMgr.takeGroup(lkey, ltimer)
	if lg == nil || !lg.matched || len(lg.files) != 2 {
		t.Fatal("local album filtering changed")
	}
	processWatchLocalAlbum(ctx, local, lg.files)
	if core.GetLength(ctx) != 3 {
		t.Fatal("local album lost or split at admission")
	}
	unblock()
	s := watchQueueWait(t, q, func(s WatchForwardStats) bool { return s.Running == 0 && s.Pending == 0 })
	if s.Accepted != 3 || s.Succeeded != 2 || s.Started != 2 || s.DiscardedStopped != 1 || s.RejectedFull != 1 || !reflect.DeepEqual(forwards, [][]int{{50}, {54, 55}}) || attributionEdits != 2 {
		t.Fatalf("actual transport/counts changed: %+v forwards=%v edits=%d", s, forwards, attributionEdits)
	}
	if files[55].Message().Message != "" || files[54].Message().Entities[0].(*tg.MessageEntityBold).Offset != 0 || service.Err() != nil || ctx.Err() != nil {
		t.Fatal("shared lifetime or source caption/entities changed")
	}
	if err := stopWatchRoute(ctx, owner, replacement.ID); err != nil {
		t.Fatal(err)
	}
	for _, task := range core.GetQueuedTasks(ctx) {
		if task.Cancelled {
			t.Fatal("unwatch cancelled local save")
		}
		if err := core.CancelTask(ctx, task.ID); err != nil {
			t.Fatal(err)
		}
	}
}

package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	botHandlers "github.com/krau/SaveAny-Bot/client/bot/handlers"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/copyfwd"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/storage"
)

func TestManagementHTTPPermissionsLifecycleAndPreview(t *testing.T) {
	const env = "SAVEANY_ADMIN_MANAGEMENT_TEST"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestManagementHTTPPermissionsLifecycleAndPreview$", "-test.count=1", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated management: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	cfg := "[admin]\nenable=true\npassword='isolated-test-password'\n[[storages]]\nname='local'\ntype='local'\nenable=true\nbase_path='downloads'\n[[users]]\nid=42\nstorages=['local']\n[[users]]\nid=43\nstorages=['local']\n"
	if err := os.WriteFile(path, []byte(cfg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	storage.LoadStorages(t.Context())
	core.Prepare()
	root, stop := context.WithCancel(t.Context())
	defer stop()
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
	peers.AddPeer(222, 2, peerstore.TypeChannel, "target")
	peers.AddPeer(333, 3, peerstore.TypeChannel, "source2")
	rpcCalls := 0
	targetManaged := true
	dialogTail := "private"
	failArchive := false
	failTopics := false
	var onDialog func()
	client := &ext.Context{Context: root, Self: &tg.User{ID: 9000}, PeerStorage: peers, Raw: tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, req bin.Encoder, out bin.Decoder) error {
		rpcCalls++
		if err := ctx.Err(); err != nil {
			return err
		}
		switch req := req.(type) {
		case *tg.ChannelsGetChannelsRequest:
			id := req.ID[0].(*tg.InputChannel).ChannelID
			channel := &tg.Channel{ID: id, Title: fmt.Sprint(id), Creator: id == 222 && targetManaged}
			channel.SetForum(true)
			out.(*tg.MessagesChatsBox).Chats = &tg.MessagesChats{Chats: []tg.ChatClass{channel}}
		case *tg.MessagesGetForumTopicsByIDRequest:
			if len(req.Topics) != 1 || req.Topics[0] != 2 {
				return fmt.Errorf("topic must use stable ID")
			}
			*out.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 2, TopMessage: 999, Title: "topic"}}}
		case *tg.MessagesGetForumTopicsRequest:
			if failTopics {
				return fmt.Errorf("topic sync failed")
			}
			*out.(*tg.MessagesForumTopics) = tg.MessagesForumTopics{Topics: []tg.ForumTopicClass{&tg.ForumTopic{ID: 2, TopMessage: 999, Title: "<script>topic</script>"}}}
		case *tg.MessagesGetDialogsRequest:
			if onDialog != nil {
				fn := onDialog
				onDialog = nil
				fn()
			}
			folder, ok := req.GetFolderID()
			if !ok {
				return fmt.Errorf("missing folder")
			}
			if dialogTail == "cycle" || dialogTail == "budget" {
				messageID := 1
				if dialogTail == "budget" {
					messageID += req.OffsetID
				}
				peer := &tg.PeerUser{UserID: 99}
				out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogsSlice{Count: 20000,
					Dialogs:  []tg.DialogClass{&tg.Dialog{Peer: peer, TopMessage: messageID}},
					Users:    []tg.UserClass{&tg.User{ID: 99, AccessHash: 99}},
					Messages: []tg.MessageClass{&tg.Message{ID: messageID, Date: 123 + messageID, PeerID: peer}}}
				return nil
			}
			channel := func(id int64, managed bool) *tg.Channel {
				c := &tg.Channel{ID: id, Title: fmt.Sprint(id), Creator: managed}
				c.SetAccessHash(id)
				c.SetBroadcast(true)
				c.SetForum(id == 222)
				return c
			}
			if folder == 1 {
				if failArchive {
					return fmt.Errorf("archive sync failed")
				}
				out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChat{ChatID: 555}}}, Chats: []tg.ChatClass{&tg.Chat{ID: 555, Title: "archive", Creator: true}}}
				return nil
			}
			if req.OffsetID != 0 {
				validPeer := false
				switch dialogTail {
				case "left":
					peer, ok := req.OffsetPeer.(*tg.InputPeerChannel)
					validPeer = ok && peer.ChannelID == 556
				case "forbidden":
					peer, ok := req.OffsetPeer.(*tg.InputPeerChannel)
					validPeer = ok && peer.ChannelID == 557
				case "left_group":
					peer, ok := req.OffsetPeer.(*tg.InputPeerChat)
					validPeer = ok && peer.ChatID == 558
				default:
					peer, ok := req.OffsetPeer.(*tg.InputPeerUser)
					validPeer = ok && peer.UserID == 99
				}
				if !req.ExcludePinned || req.OffsetID != 90 || req.OffsetDate != 123 || !validPeer {
					return fmt.Errorf("invalid private-dialog pagination cursor")
				}
				out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogs{Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 222}}, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 333}}}, Chats: []tg.ChatClass{channel(222, true), channel(333, false)}}
				return nil
			}
			var tail tg.PeerClass = &tg.PeerUser{UserID: 99}
			var excluded tg.ChatClass
			switch dialogTail {
			case "left":
				tail = &tg.PeerChannel{ChannelID: 556}
				c := channel(556, false)
				c.SetLeft(true)
				excluded = c
			case "forbidden":
				tail = &tg.PeerChannel{ChannelID: 557}
				excluded = &tg.ChannelForbidden{ID: 557, AccessHash: 557, Title: "inaccessible"}
			case "left_group":
				tail = &tg.PeerChat{ChatID: 558}
				excluded = &tg.Chat{ID: 558, Left: true, Title: "left group"}
			}
			entities := []tg.ChatClass{channel(111, false), channel(222, true), &tg.Chat{ID: 111, Title: "same numeric id"}}
			if excluded != nil {
				entities = append(entities, excluded)
			}
			out.(*tg.MessagesDialogsBox).Dialogs = &tg.MessagesDialogsSlice{Count: 150,
				Dialogs: []tg.DialogClass{&tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 111}}, &tg.Dialog{Peer: &tg.PeerChannel{ChannelID: 222}}, &tg.Dialog{Peer: &tg.PeerChat{ChatID: 111}}, &tg.Dialog{Peer: tail, TopMessage: 90}},
				Chats:   entities, Users: []tg.UserClass{&tg.User{ID: 99, AccessHash: 99}}, Messages: []tg.MessageClass{&tg.MessageService{ID: 90, Date: 123, PeerID: tail}}}
		default:
			return fmt.Errorf("unexpected management RPC %T", req)
		}
		return nil
	}))}
	s := testServer(t)
	s.ctx = root
	s.management = botHandlers.ManagementService{Client: func() *ext.Context { return client }}
	cookie, csrf := signIn(t, s)
	post := func(method, endpoint, body string, want int) *httptest.ResponseRecorder {
		t.Helper()
		resp := perform(s, method, "/admin/api/v1/"+endpoint, body, cookie, csrf, "http://console.test")
		if resp.Code != want {
			t.Fatalf("%s %s status=%d want=%d %s", method, endpoint, resp.Code, want, resp.Body.String())
		}
		return resp
	}
	for _, endpoint := range []string{"routes", "copies", "chats", "chats/refresh", "topics", "topics/refresh", "preview", "preferences"} {
		method := "POST"
		if endpoint == "preferences" {
			method = "PATCH"
		}
		for _, auth := range []struct {
			cookie        *http.Cookie
			token, origin string
			code          int
		}{{nil, "", "", 401}, {cookie, "", "http://console.test", 403}, {cookie, csrf, "http://foreign.test", 403}} {
			resp := perform(s, method, "/admin/api/v1/"+endpoint, `{"user_chat_id":42}`, auth.cookie, auth.token, auth.origin)
			if resp.Code != auth.code {
				t.Fatalf("write bypassed authentication/CSRF: %s %d", endpoint, resp.Code)
			}
		}
	}
	if rpcCalls != 0 {
		t.Fatal("unauthorized requests made Telegram calls")
	}
	post("POST", "routes", `{"user_chat_id":999,"source":"source","target":"target"}`, 400)
	post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"0","count":1}`, 400)
	post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"target","count":5001}`, 400)
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"0:2"}`, 400)
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"target","filter":"msgre:("}`, 400)
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"target","webhook":"https://example.test"}`, 400)
	if rpcCalls != 0 {
		t.Fatal("invalid requests made Telegram calls")
	}
	response := post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"target:2","filter":"msgre:caption"}`, 200)
	var created struct {
		ID uint `json:"id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.ID == 0 {
		t.Fatal("missing persisted route", err)
	}
	u, err := database.GetUserByChatID(root, 42)
	if err != nil || len(u.WatchChats) != 1 || u.WatchChats[0].TargetTopicID != 2 || u.WatchChats[0].Filter != "msgre:caption" {
		t.Fatalf("invalid persisted route %+v %v", u, err)
	}
	old := u.WatchChats[0]
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"target:2"}`, 409)
	post("POST", fmt.Sprintf("routes/%d/stop", created.ID), `{"user_chat_id":43}`, 400)
	if active, err := database.WatchRouteActive(root, old); err != nil || !active {
		t.Fatal("foreign-user stop deleted route")
	}
	post("POST", fmt.Sprintf("routes/%d/stop", created.ID), `{"user_chat_id":42}`, 200)
	if active, err := database.WatchRouteActive(root, old); err != nil || active {
		t.Fatal("stop did not invalidate captured identity")
	}
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"target:2"}`, 200)
	if active, err := database.WatchRouteActive(root, old); err != nil || active || root.Err() != nil {
		t.Fatal("old event rebound or shared client cancelled")
	}
	post("PATCH", "preferences", `{"user_chat_id":42,"default_storage":"local","silent":true,"filename_template":"{{.msgid}}_{{.origname}}"}`, 200)
	post("PATCH", "preferences", `{"user_chat_id":42,"apply_rule":true}`, 200)
	if err := database.CreateDirForUser(root, u.ID, "local", "albums"); err != nil {
		t.Fatal(err)
	}
	u, err = database.GetUserByChatID(root, 42)
	if err != nil || len(u.Dirs) != 1 {
		t.Fatal("missing directory fixture")
	}
	directoryID := u.Dirs[0].ID
	post("PATCH", "preferences", fmt.Sprintf(`{"user_chat_id":42,"default_dir":%d}`, directoryID), 200)
	post("PATCH", "preferences", `{"user_chat_id":42,"default_storage":"local"}`, 200)
	u, err = database.GetUserByChatID(root, 42)
	if err != nil || u.DefaultDir != directoryID {
		t.Fatal("reselecting unchanged storage cleared default directory")
	}
	u, err = database.GetUserByChatID(root, 42)
	if err != nil || !u.Silent || !u.ApplyRule || u.DefaultStorage != "local" || len(u.WatchChats) != 1 {
		t.Fatal("partial preference update damaged other fields")
	}
	post("PATCH", "preferences", `{"user_chat_id":42,"silent":false,"filename_template":""}`, 200)
	post("PATCH", "preferences", `{"user_chat_id":42,"default_storage":"missing"}`, 400)
	post("PATCH", "preferences", `{"user_chat_id":42,"filename_strategy":"bad"}`, 400)
	post("PATCH", "preferences", `{"user_chat_id":42,"default_dir":999}`, 400)
	post("PATCH", "preferences", `{"user_chat_id":42}`, 400)
	post("POST", "routes", `{"user_chat_id":42,"source":"source","target":"0"}`, 200)
	before := rpcCalls
	response = post("POST", "preview", `{"user_chat_id":42,"filter":"msgre:caption","text":"caption <script>","template":"{{.msgid}}_{{.origname}}","original_name":"sample.mp4"}`, 200)
	badFilter := post("POST", "preview", `{"user_chat_id":42,"filter":"msgre:舞团&(king8|)","text":"舞团 k8","template":"{{.origname}}","original_name":"sample"}`, 400)
	if !strings.Contains(badFilter.Body.String(), `"invalid_filter"`) {
		t.Fatal("invalid expression did not get a clear filter error")
	}
	combined := post("POST", "preview", `{"user_chat_id":42,"filter":"msgre:舞团&(king8|k8)","text":"舞团 K8","template":"{{.origname}}","original_name":"sample"}`, 200)
	if !strings.Contains(combined.Body.String(), `"matches":true`) {
		t.Fatal("combined preview disagrees with parser")
	}
	var preview struct {
		Matches  bool   `json:"matches"`
		Filename string `json:"filename"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil || !preview.Matches || preview.Filename != "123_sample.mp4" || rpcCalls != before {
		t.Fatalf("preview mismatch/side effect %s %v", response.Body.String(), err)
	}
	post("POST", "preview", `{"user_chat_id":42,"template":"{{printf \"%1000000000s\" .msgraw}}"}`, 400)
	before = rpcCalls
	response = post("POST", "topics", `{"user_chat_id":42,"target":"-1000000000222"}`, 200)
	var emptyTopics botHandlers.TopicList
	if err := json.Unmarshal(response.Body.Bytes(), &emptyTopics); err != nil || len(emptyTopics.Topics) != 0 || emptyTopics.UpdatedAt != nil || rpcCalls != before {
		t.Fatal("uncached topic lookup made network requests", err)
	}
	response = post("POST", "topics/refresh", `{"user_chat_id":42,"target":"-1000000000222"}`, 200)
	var topics struct {
		Topics []struct {
			ID int `json:"id"`
		} `json:"topics"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &topics); err != nil || len(topics.Topics) != 1 || topics.Topics[0].ID != 2 {
		t.Fatal("topics use unstable ID", err)
	}
	before = rpcCalls
	response = post("POST", "topics", `{"user_chat_id":42,"target":"-1000000000222"}`, 200)
	var cachedTopics botHandlers.TopicList
	if err := json.Unmarshal(response.Body.Bytes(), &cachedTopics); err != nil || len(cachedTopics.Topics) != 1 || cachedTopics.UpdatedAt == nil || rpcCalls != before {
		t.Fatal("cached topics made network requests", err)
	}
	failTopics = true
	post("POST", "topics/refresh", `{"user_chat_id":42,"target":"-1000000000222"}`, 400)
	failTopics = false
	response = post("POST", "topics", `{"user_chat_id":42,"target":"-1000000000222"}`, 200)
	var afterTopics botHandlers.TopicList
	if err := json.Unmarshal(response.Body.Bytes(), &afterTopics); err != nil || afterTopics.UpdatedAt == nil || !afterTopics.UpdatedAt.Equal(*cachedTopics.UpdatedAt) || len(afterTopics.Topics) != 1 {
		t.Fatal("failed topic refresh replaced its cache", err)
	}
	response = post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"target:2","count":2}`, 200)
	var copy struct {
		ID string `json:"task_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &copy); err != nil || copy.ID == "" {
		t.Fatal("missing copy task", err)
	}
	post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"target:2","count":1}`, 409)
	queued := core.GetQueuedTasks(root)
	if len(queued) != 1 || queued[0].Cancelled {
		t.Fatal("request lifetime cancelled admitted copy")
	}
	rows := s.taskRows(root)
	found := false
	for _, row := range rows {
		if row.TaskID == copy.ID {
			found = true
			if row.Type != "copy" || row.Source != "admin" || !row.CanCancel {
				t.Fatalf("copy missing shared history/cancel %+v", row)
			}
		}
	}
	if !found {
		t.Fatal("copy missing shared task list")
	}
	post("POST", "tasks/"+copy.ID+"/cancel", "", 200)
	if !copyfwd.TryBegin(42, "next") {
		t.Fatal("queued admin cancellation left copy ownership")
	}
	copyfwd.End(42, "next")
	post("POST", "copies", `{"user_chat_id":42,"source":"target","target":"target","count":1}`, 400)
	targetManaged = false
	post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"target","count":1}`, 400)
	targetManaged = true
	var list botHandlers.ChatList
	before = rpcCalls
	response = post("POST", "chats", `{"user_chat_id":42}`, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Chats) != 0 || list.UpdatedAt != nil || rpcCalls != before {
		t.Fatal("uncached chats lookup made RPCs", err)
	}
	onDialog = func() {
		post("POST", "chats", `{"user_chat_id":42}`, 200)
		post("POST", "chats/refresh", `{"user_chat_id":42}`, 429)
	}
	response = post("POST", "chats/refresh", `{"user_chat_id":42}`, 200)
	if strings.Contains(response.Body.String(), "access_hash") {
		t.Fatal("cache exposed access hashes")
	}
	if err := json.Unmarshal(response.Body.Bytes(), &list); err != nil || len(list.Chats) != 5 || list.UpdatedAt == nil {
		t.Fatalf("incomplete chat snapshot: %s %v", response.Body.String(), err)
	}
	if !list.Chats[1].Managed || list.Chats[0].Managed || list.Chats[2].ID != -111 || list.Chats[4].ID != -555 {
		t.Fatal("membership, canonical IDs or archived chats mixed")
	}
	before = rpcCalls
	post("POST", "chats", `{"user_chat_id":42}`, 200)
	post("POST", "chats", `{"user_chat_id":43}`, 200)
	post("POST", "topics", `{"user_chat_id":42,"target":"-1000000000222"}`, 200)
	if rpcCalls != before {
		t.Fatal("local lists made network requests")
	}
	client.Self.ID = 9001
	response = post("POST", "chats", `{"user_chat_id":42}`, 200)
	var another botHandlers.ChatList
	if err := json.Unmarshal(response.Body.Bytes(), &another); err != nil || len(another.Chats) != 0 || another.UpdatedAt != nil || rpcCalls != before {
		t.Fatal("cache crossed Userbot account boundary", err)
	}
	client.Self.ID = 9000
	failArchive = true
	post("POST", "chats/refresh", `{"user_chat_id":42}`, 400)
	failArchive = false
	response = post("POST", "chats", `{"user_chat_id":42}`, 200)
	var after botHandlers.ChatList
	if err := json.Unmarshal(response.Body.Bytes(), &after); err != nil || after.UpdatedAt == nil || !after.UpdatedAt.Equal(*list.UpdatedAt) || len(after.Chats) != 5 {
		t.Fatal("failed/partial chat sync overwrote the complete cache", err)
	}
	for _, mode := range []string{"left", "forbidden", "left_group"} {
		dialogTail = mode
		response = post("POST", "chats/refresh", `{"user_chat_id":42}`, 200)
		if err := json.Unmarshal(response.Body.Bytes(), &after); err != nil || len(after.Chats) != 5 || after.UpdatedAt == nil {
			t.Fatalf("excluded dialog broke sync (%s): %s %v", mode, response.Body.String(), err)
		}
	}
	for _, mode := range []string{"cycle", "budget"} {
		dialogTail = mode
		before = rpcCalls
		post("POST", "chats/refresh", `{"user_chat_id":42}`, 400)
		want := 2
		if mode == "budget" {
			want = 100
		}
		if rpcCalls-before != want {
			t.Fatalf("chat scan did not stop at %s bound: calls=%d want=%d", mode, rpcCalls-before, want)
		}
		response = post("POST", "chats", `{"user_chat_id":42}`, 200)
		var retained botHandlers.ChatList
		if err := json.Unmarshal(response.Body.Bytes(), &retained); err != nil || retained.UpdatedAt == nil || !retained.UpdatedAt.Equal(*after.UpdatedAt) || len(retained.Chats) != len(after.Chats) {
			t.Fatalf("%s failure damaged complete snapshot: %s %v", mode, response.Body.String(), err)
		}
	}
	dialogTail = "private"
	targetManaged = false
	post("POST", "copies", `{"user_chat_id":42,"source":"source","target":"target","count":1}`, 400)
	targetManaged = true
	if root.Err() != nil {
		t.Fatal("management cancelled shared service")
	}
	for range 2 {
		s.controls <- struct{}{}
	}
	post("POST", "topics", `{"user_chat_id":42,"target":"-1000000000222"}`, 429)
	<-s.controls
	<-s.controls
}

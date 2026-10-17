package shortcut

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/config"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

func TestShardReelSkipConflictFeedback(t *testing.T) {
	const rootEnv = "SAVEANY_SKIP_CONTRACT_TEST_ROOT"
	root := os.Getenv(rootEnv)
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		for _, state := range []string{"init", "directory", "ready", "missing"} {
			for _, mode := range []string{"single", "batch"} {
				t.Run(state+"/"+mode, func(t *testing.T) {
					cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestShardReelSkipConflictFeedback$", "-test.count=1")
					cmd.Env = append(os.Environ(), rootEnv+"="+t.TempDir(), "SAVEANY_SKIP_TEST_STATE="+state, "SAVEANY_SKIP_TEST_MODE="+mode)
					if output, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("%v\n%s", err, output)
					}
				})
			}
		}
		return
	}
	state := os.Getenv("SAVEANY_SKIP_TEST_STATE")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/storage/stat" {
			t.Errorf("unexpected HTTP: %s %s", r.Method, r.URL.Path)
		}
		var body any = map[string]any{"name": "file.bin", "is_dir": state == "directory", "status": state, "size": 4}
		if state == "missing" {
			w.WriteHeader(404)
			body = map[string]any{"detail": map[string]string{"code": "PATH_NOT_FOUND"}}
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	t.Chdir(root)
	cfg := filepath.Join(root, "config.toml")
	if err := os.WriteFile(cfg, []byte("[db]\npath='test.db'\n[[users]]\nid=42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	connection, err := database.ShardReelDB(t.Context()).DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	i18n.Init("en")
	core.Prepare()
	store := new(shardreel.Store)
	if err := store.Init(t.Context(), &storconfig.ShardReelStorageConfig{BaseConfig: storconfig.BaseConfig{Name: "remote"}, URL: server.URL, APIKey: "isolated-storage-integration-key-2026", SpoolPath: filepath.Join(root, "spool")}); err != nil {
		t.Fatal(err)
	}
	edited := ""
	raw := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesEditMessageRequest)
		if !ok {
			return fmt.Errorf("unexpected RPC %T", in)
		}
		edited = req.Message
		out.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: req.ID, Message: req.Message, PeerID: &tg.PeerUser{UserID: 42}}}}}
		return nil
	}))
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "owner")
	ctx := ext.NewContext(t.Context(), raw, peers, &tg.User{ID: 9000}, message.NewSender(raw), &tg.Entities{}, false)
	media := &tg.MessageMediaDocument{Document: &tg.Document{ID: 1, Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "file.bin"}}}}
	file, err := tfile.FromMediaMessage(media, raw, &tg.Message{ID: 1, Media: media, PeerID: &tg.PeerChannel{ChannelID: 111}})
	if err != nil {
		t.Fatal(err)
	}

	if os.Getenv("SAVEANY_SKIP_TEST_MODE") == "batch" {
		err = CreateAndAddBatchTGFileTaskWithEdit(ctx, 42, store, "/", []tfile.TGFileMessage{file}, 99, tcbdata.ConflictStrategySkip)
	} else {
		err = CreateAndAddTGFileTaskWithEdit(ctx, 42, store, "/", file, 99, tcbdata.ConflictStrategySkip)
	}
	if !errors.Is(err, dispatcher.EndGroups) {
		t.Fatalf("unexpected handler result: %v", err)
	}
	queued := len(core.GetQueuedTasks(t.Context()))
	switch state {
	case "init", "directory":
		if strings.Contains(strings.ToLower(edited), "skipped") || queued != 0 || !strings.Contains(edited, "not a completed file") {
			t.Fatalf("feedback=%q queued=%d", edited, queued)
		}
	case "ready":
		if !strings.Contains(strings.ToLower(edited), "skipped") || queued != 0 {
			t.Fatalf("feedback=%q queued=%d", edited, queued)
		}
	case "missing":
		if queued != 1 {
			t.Fatalf("feedback=%q queued=%d", edited, queued)
		}
	}
}

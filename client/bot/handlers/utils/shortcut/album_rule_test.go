package shortcut

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/krau/SaveAny-Bot/pkg/rule"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage/local"
)

type albumRuleStorage struct {
	*local.Local
	checked []string
}

func (s *albumRuleStorage) Exists(_ context.Context, path string) bool {
	s.checked = append(s.checked, path)
	return false
}

func TestAlbumDirectoryRuleKeepsSingleRemainingMember(t *testing.T) {
	const env = "SAVEANY_ALBUM_RULE_TEST_ROOT"
	root := os.Getenv(env)
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestAlbumDirectoryRuleKeepsSingleRemainingMember$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated album rule regression: %v\n%s", err, output)
		}
		return
	}
	configFile := filepath.Join(root, "config.toml")
	text := fmt.Sprintf("[db]\npath=%q\n[[users]]\nid=42\n", filepath.ToSlash(filepath.Join(root, "database.db")))
	if err := os.WriteFile(configFile, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configFile); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	i18n.Init("en")
	core.Prepare()
	u, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateRule(t.Context(), &database.Rule{UserID: u.ID, Type: rule.IsAlbum.String(), Data: "true", StorageName: rule.RuleStorNameChosen, DirPath: rule.RuleDirPathNewForAlbum}); err != nil {
		t.Fatal(err)
	}
	if err := database.UpdateUserApplyRule(t.Context(), 42, true); err != nil {
		t.Fatal(err)
	}
	stor := &albumRuleStorage{Local: new(local.Local)}
	if err := stor.Init(t.Context(), &storconfig.LocalStorageConfig{BasePath: filepath.Join(root, "downloads")}); err != nil {
		t.Fatal(err)
	}
	raw := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesEditMessageRequest)
		if !ok {
			return fmt.Errorf("unexpected RPC %T", in)
		}
		out.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateEditMessage{Message: &tg.Message{ID: req.ID, Message: req.Message, PeerID: &tg.PeerUser{UserID: 42}}}}}
		return nil
	}))
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "owner")
	ctx := ext.NewContext(t.Context(), raw, peers, &tg.User{ID: 9000}, message.NewSender(raw), &tg.Entities{}, false)
	var single tfile.TGFileMessage
	for count := 1; count <= 2; count++ {
		var files []tfile.TGFileMessage
		for id := 1; id <= count; id++ {
			media := &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(id), Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: fmt.Sprintf("%d.bin", id)}}}}
			msg := &tg.Message{ID: id, Media: media, PeerID: &tg.PeerChannel{ChannelID: 111}}
			msg.SetGroupedID(int64(count))
			file, err := tfile.FromMediaMessage(media, raw, msg)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, file)
		}
		single = files[0]
		stor.checked = nil
		if err := CreateAndAddBatchTGFileTaskWithEdit(ctx, 42, stor, "/", files, 99, tcbdata.ConflictStrategyAsk); err != nil && !errors.Is(err, dispatcher.EndGroups) {
			t.Fatal(err)
		}
		queued := core.GetQueuedTasks(t.Context())
		if len(queued) != count || !strings.Contains(queued[count-1].Title, fmt.Sprintf("%d files/", count)) {
			t.Fatalf("album directory rule discarded members: selected=%d queued=%+v", count, queued)
		}
		if len(stor.checked) != count || stor.checked[0] != "/1/1.bin" {
			t.Fatalf("album directory rule chose wrong paths: %v", stor.checked)
		}
	}
	stor.checked = nil
	if err := CreateAndAddTGFileTaskWithEdit(ctx, 42, stor, "/", single, 99, tcbdata.ConflictStrategyAsk); err != nil && !errors.Is(err, dispatcher.EndGroups) {
		t.Fatal(err)
	}
	if len(core.GetQueuedTasks(t.Context())) != 3 || len(stor.checked) != 1 || stor.checked[0] != "/1/1.bin" {
		t.Fatalf("single-file selection did not use its album directory: %v", stor.checked)
	}
}

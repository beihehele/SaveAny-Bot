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
	"time"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
)

type linkResultInvoker struct {
	sent           []string
	warningFailure bool
}

func (i *linkResultInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	switch req := in.(type) {
	case *tg.MessagesSendMessageRequest:
		i.sent = append(i.sent, req.Message)
		if len(i.sent) > 1 {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 6*time.Second {
				return errors.New("warning has no bounded context")
			}
			if i.warningFailure {
				return errors.New("synthetic feedback delivery failure")
			}
		}
		out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 100 + len(i.sent)}
		return nil
	case *tg.ChannelsGetMessagesRequest:
		var msgs []tg.MessageClass
		for _, id := range req.ID {
			mid := id.(*tg.InputMessageID).ID
			if mid != 40 && mid != 42 && mid != 44 && mid != 52 {
				continue
			}
			msg := &tg.Message{ID: mid, PeerID: &tg.PeerChannel{ChannelID: 111}}
			if mid != 52 {
				msg.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(mid), Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: fmt.Sprintf("%d.bin", mid)}}}}
				msg.SetGroupedID(700)
			}
			msgs = append(msgs, msg)
		}
		out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: msgs}
		return nil
	case *tg.ContactsResolveUsernameRequest:
		return errors.New("synthetic inaccessible source")
	default:
		return fmt.Errorf("unexpected RPC %T", in)
	}
}

func TestBotLinkWarningsPreserveSelectionEvenWhenDeliveryFails(t *testing.T) {
	const env = "SAVEANY_LINK_RESULTS_TEST_ROOT"
	root := os.Getenv(env)
	if root == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, stop := context.WithTimeout(t.Context(), 30*time.Second)
		defer stop()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestBotLinkWarningsPreserveSelectionEvenWhenDeliveryFails$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated link regression: %v\n%s", err, output)
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
	cache.Init()
	i18n.Init("en")
	for index, fail := range []bool{false, true} {
		invoker := &linkResultInvoker{warningFailure: fail}
		raw := tg.NewClient(invoker)
		peers := peerstore.NewPeerStorage(nil, true)
		peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
		peers.AddPeer(42, 1, peerstore.TypeUser, "owner")
		entities := &tg.Entities{Users: map[int64]*tg.User{42: {ID: 42}}}
		ctx := ext.NewContext(t.Context(), raw, peers, &tg.User{ID: int64(900 + index)}, message.NewSender(raw), entities, false)
		msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 42}, Message: strings.Join([]string{"https://t.me/c/111/42?single", "https://t.me/c/111/40", "https://t.me/c/111/42", "https://t.me/c/111/52?single", "https://t.me/missing/1"}, "\n")}
		update := &ext.Update{EffectiveMessage: &types.Message{Message: msg}, Entities: entities, UpdateClass: &tg.UpdateNewMessage{Message: msg}}
		replied, files, edit, err := GetFilesFromUpdateLinkMessageWithReplyEdit(ctx, update)
		if err != nil || replied == nil || edit == nil || len(files) != 3 || files[0].Message().ID != 42 || ctx.Err() != nil {
			t.Fatalf("warnings changed media selection: %v files=%d", err, len(files))
		}
		for _, file := range files {
			if file.Dler() != raw {
				t.Fatal("download client changed")
			}
		}
		if len(invoker.sent) != 2 || !strings.Contains(invoker.sent[1], "Link 3:") || !strings.Contains(invoker.sent[1], "Link 4:") || !strings.Contains(invoker.sent[1], "Link 5:") {
			t.Fatalf("missing skipped input positions: %+v", invoker.sent)
		}
	}
}

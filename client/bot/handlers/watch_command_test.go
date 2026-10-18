package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
)

func TestWatchCommandRejectsSameChat(t *testing.T) {
	const env = "SAVEANY_WATCH_COMMAND_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestWatchCommandRejectsSameChat$", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated watch regression: %v\n%s", err, out)
		}
		return
	}
	path := filepath.Join(dir, "config.toml")
	body := fmt.Sprintf("[db]\npath=%q\n[[users]]\nid=42\n", filepath.ToSlash(filepath.Join(dir, "database.db")))
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	if err := database.UpdateUserPreferences(t.Context(), 42, map[string]any{"default_storage": "chosen"}); err != nil {
		t.Fatal(err)
	}
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "owner")
	peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
	entities := &tg.Entities{Users: map[int64]*tg.User{42: {ID: 42}}}
	for _, lang := range []string{"zh-Hans", "en"} {
		i18n.Init(lang)
		for _, tc := range []struct {
			args string
			same bool
		}{
			{"-1000000000111 -1000000000111", true},
			{"-1000000000111 -1000000000111:2", true},
			{"@source -1000000000111:2", true},
			{"-1000000000111 @source", true},
			{"-1000000000111 -1000000000222", false},
			{"-1000000000111 0", false},
			{"-1000000000111", false},
		} {
			t.Run(lang+"/"+tc.args, func(t *testing.T) {
				var replies []string
				raw := tg.NewClient(telegram.InvokeFunc(func(_ context.Context, in bin.Encoder, out bin.Decoder) error {
					req, ok := in.(*tg.MessagesSendMessageRequest)
					if !ok {
						t.Fatalf("unexpected RPC %T", in)
					}
					replies = append(replies, req.Message)
					out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 10}
					return nil
				}))
				ctx := ext.NewContext(t.Context(), raw, peers, &tg.User{ID: 900}, message.NewSender(raw), entities, false)
				msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 42}, Message: "/watch " + tc.args}
				update := &ext.Update{EffectiveMessage: types.ConstructMessage(msg), Entities: entities, UpdateClass: &tg.UpdateNewMessage{Message: msg}}
				if err := handleWatchCmd(ctx, update); !errors.Is(err, dispatcher.EndGroups) {
					t.Fatal(err)
				}
				want := i18n.T(i18nk.BotMsgWatchErrorUserbotUnavailable)
				if tc.same {
					want = i18n.T(i18nk.BotMsgWatchErrorSameChat)
				}
				if len(replies) != 1 || replies[0] != want || want == string(i18nk.BotMsgWatchErrorSameChat) {
					t.Fatalf("replies=%q want=%q", replies, want)
				}
				routes, err := database.GetWatchChatsByChatID(t.Context(), -1000000000111)
				if err != nil || len(routes) != 0 {
					t.Fatalf("unexpected route write: %v %v", routes, err)
				}
			})
		}
	}
}

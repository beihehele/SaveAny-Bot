package tgutil

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
)

func TestMessageReadsReflectEditsAndDeletedAlbumMembers(t *testing.T) {
	const env = "SAVEANY_MESSAGE_FRESHNESS_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestMessageReadsReflectEditsAndDeletedAlbumMembers$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated freshness regression: %v\n%s", err, output)
		}
		return
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	cache.Init()
	for _, kind := range []string{"single", "album"} {
		t.Run(kind, func(t *testing.T) {
			for _, id := range []int{500, 501} {
				old := &tg.Message{ID: id, Message: "old caption", PeerID: &tg.PeerChannel{ChannelID: 111}}
				old.SetGroupedID(77)
				key := fmt.Sprintf("tgmsg:9000:-1000000000111:%d", id)
				if err := cache.Set(key, old); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(time.Second)
				for {
					if _, ok := cache.Get[*tg.Message](key); ok {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("cache fixture was not admitted")
					}
					time.Sleep(time.Millisecond)
				}
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
			calls := 0
			client := &ext.Context{Context: t.Context(), Self: &tg.User{ID: 9000}, PeerStorage: peers}
			client.Raw = tg.NewClient(telegram.InvokeFunc(func(_ context.Context, input bin.Encoder, output bin.Decoder) error {
				calls++
				req := input.(*tg.ChannelsGetMessagesRequest)
				var rows []tg.MessageClass
				for _, item := range req.ID {
					if item.(*tg.InputMessageID).ID == 500 {
						updated := &tg.Message{ID: 500, Message: "edited caption", PeerID: &tg.PeerChannel{ChannelID: 111}}
						updated.SetGroupedID(77)
						rows = append(rows, updated)
					}
				}
				output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesChannelMessages{Messages: rows}
				return nil
			}))
			if kind == "single" {
				msg, err := GetMessageByID(client, -1000000000111, 500)
				if err != nil || msg.Message != "edited caption" || calls != 1 {
					t.Fatalf("message read used stale metadata: msg=%v err=%v calls=%d", msg, err, calls)
				}
			} else {
				members, err := GetGroupedMessages(client, -1000000000111, groupMessage(500, 77))
				if err != nil || len(members) != 1 || members[0].ID != 500 || members[0].Message != "edited caption" || calls != 1 {
					t.Fatalf("album read retained deleted/edited metadata: members=%v err=%v calls=%d", members, err, calls)
				}
			}
		})
	}
}

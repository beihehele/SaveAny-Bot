package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
)

var sourceCacheOnce sync.Once
var sourceSelf atomic.Int64

type sourceContextKey struct{}
type sourceInvoker struct {
	queries, groups int
	fail            bool
	cancel          context.CancelFunc
	cancelAlbum     bool
}

func sourceMessage(id int) *tg.Message {
	msg := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 111}, Media: &tg.MessageMediaDocument{Document: &tg.Document{
		ID: int64(id), Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: fmt.Sprintf("%d.bin", id)}},
	}}}
	msg.SetGroupedID(700)
	return msg
}

func (i *sourceInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	i.queries++
	if ctx.Value(sourceContextKey{}) != "request" {
		return errors.New("RPC lost request context")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	request, ok := input.(*tg.ChannelsGetMessagesRequest)
	if !ok {
		return fmt.Errorf("unexpected RPC: %T", input)
	}
	album := len(request.ID) > 1
	if album {
		i.groups++
	}
	if i.cancel != nil && album == i.cancelAlbum {
		i.cancel()
		return ctx.Err()
	}
	if i.fail {
		return errors.New("source inaccessible")
	}
	var messages []tg.MessageClass
	for _, id := range request.ID {
		mid := id.(*tg.InputMessageID).ID
		if mid == 40 || mid == 42 || mid == 44 {
			msg := sourceMessage(mid)
			msg.PeerID = &tg.PeerChannel{ChannelID: request.Channel.(*tg.InputChannel).ChannelID}
			messages = append(messages, msg)
		}
	}
	output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
	return nil
}

func sourceClient(t *testing.T, invoker *sourceInvoker) *ext.Context {
	t.Helper()
	sourceCacheOnce.Do(func() {
		p := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(p, []byte("workers=1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := config.Init(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		cache.Init()
	})
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
	peers.AddPeer(222, 2, peerstore.TypeChannel, "other")
	return &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers, Self: &tg.User{ID: 90000 + sourceSelf.Add(1)}}
}

func sourceRequest(t *testing.T) (context.Context, context.CancelFunc) {
	return context.WithCancel(context.WithValue(t.Context(), sourceContextKey{}, "request"))
}

func TestSourceCancellationStopsBotAndAlbumFallback(t *testing.T) {
	for _, album := range []bool{false, true} {
		t.Run(fmt.Sprint(album), func(t *testing.T) {
			ctx, cancel := sourceRequest(t)
			defer cancel()
			invoker := &sourceInvoker{cancel: cancel, cancelAlbum: album}
			botClient := sourceClient(t, invoker)
			userInvoker := &sourceInvoker{}
			userClient := sourceClient(t, userInvoker)
			var err error
			if album {
				_, err = getGroupedMessagesWithContext(ctx, &MessageContext{Message: sourceMessage(42), Client: botClient}, -1000000000111)
			} else {
				_, err = getMessageFromClients(ctx, -1000000000111, 42, botClient, userClient)
			}
			if !errors.Is(err, context.Canceled) || invoker.queries == 0 || userInvoker.queries != 0 {
				t.Fatalf("cancelled query fell back: %v %d/%d", err, invoker.queries, userInvoker.queries)
			}
			if botClient.Err() != nil || userClient.Err() != nil {
				t.Fatal("shared client cancelled")
			}
		})
	}
}

func TestSourceFallbackKeepsSuccessfulClientForDownloading(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	botClient := sourceClient(t, &sourceInvoker{fail: true})
	userInvoker := &sourceInvoker{}
	userClient := sourceClient(t, userInvoker)
	source, err := getMessageFromClients(ctx, -1000000000111, 42, botClient, userClient)
	if err != nil || source.Client != userClient || userInvoker.queries == 0 {
		t.Fatalf("fallback failed: %v %+v", err, source)
	}
	msgs, err := getGroupedMessagesWithContext(ctx, source, -1000000000111)
	if err != nil || len(msgs) != 3 || userInvoker.groups != 1 {
		t.Fatalf("album lost successful client: %v %v", msgs, err)
	}
}

func TestLinkExtractionDeduplicatesOverlapAndPreservesSingle(t *testing.T) {
	for _, tc := range []struct {
		name  string
		links []string
		want  []int
	}{
		{"single", []string{"https://t.me/c/111/42?single"}, []int{42}},
		{"overlapping album", []string{"https://t.me/c/111/40", "https://t.me/c/111/42"}, []int{40, 42, 44}},
		{"duplicate single", []string{"https://t.me/c/111/42?single", "https://t.me/c/111/42?single"}, []int{42}},
		{"single then album", []string{"https://t.me/c/111/42?single", "https://t.me/c/111/40"}, []int{42, 40, 44}},
		{"cross chat same group and IDs", []string{"https://t.me/c/111/42?single", "https://t.me/c/222/42?single"}, []int{42, 42}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := sourceRequest(t)
			defer cancel()
			client := sourceClient(t, &sourceInvoker{})
			files, err := extractFilesFromLinks(ctx, tc.links, ParseMessageLink, func(ctx context.Context, chatID int64, msgID int) (*MessageContext, error) {
				return getMessageFromClients(ctx, chatID, msgID, client, nil)
			})
			if err != nil {
				t.Fatal(err)
			}
			var ids []int
			for _, file := range files {
				ids = append(ids, file.Message().ID)
				if file.Dler() != client.Raw {
					t.Fatal("download client changed")
				}
			}
			// Album queries can include cached messages in any order; membership and
			// first explicit ?single selection must survive, without duplicate files.
			if len(ids) != len(tc.want) {
				t.Fatalf("files duplicated: %v", ids)
			}
			if tc.name == "single then album" && ids[0] != 42 {
				t.Fatal("first selection lost")
			}
			gotCounts, wantCounts := map[int]int{}, map[int]int{}
			for _, id := range ids {
				gotCounts[id]++
			}
			for _, id := range tc.want {
				wantCounts[id]++
			}
			if !reflect.DeepEqual(gotCounts, wantCounts) {
				t.Fatalf("selection changed: %v", ids)
			}
		})
	}
}

func TestCancelledExtractionDiscardsPartialSelection(t *testing.T) {
	ctx, cancel := sourceRequest(t)
	defer cancel()
	client := sourceClient(t, &sourceInvoker{})
	calls := 0
	files, err := extractFilesFromLinks(ctx, []string{"https://t.me/c/111/40?single", "https://t.me/c/111/42?single"}, ParseMessageLink, func(ctx context.Context, chatID int64, msgID int) (*MessageContext, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, ctx.Err()
		}
		return &MessageContext{Message: sourceMessage(msgID), Client: client}, nil
	})
	if !errors.Is(err, context.Canceled) || files != nil {
		t.Fatalf("cancelled preparation returned partial work: %v %v", files, err)
	}
}

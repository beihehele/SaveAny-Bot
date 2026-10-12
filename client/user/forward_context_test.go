package user

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
)

var forwardTestCache sync.Once
var forwardTestSelf atomic.Int64

type forwardContextKey struct{}

type albumContextInvoker struct {
	cancelGroup context.CancelFunc
	failGroup   bool
	forward     *tg.MessagesForwardMessagesRequest
	edit        *tg.MessagesEditMessageRequest
	fetches     int
	groupReads  int
}

func (i *albumContextInvoker) Invoke(ctx context.Context, input bin.Encoder, output bin.Decoder) error {
	if ctx.Value(forwardContextKey{}) != "task" {
		return errors.New("RPC lost task context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	switch request := input.(type) {
	case *tg.ChannelsGetMessagesRequest:
		i.fetches++
		if len(request.ID) > 1 {
			i.groupReads++
			if i.cancelGroup != nil {
				i.cancelGroup()
				return ctx.Err()
			}
			if i.failGroup {
				return errors.New("album query unavailable")
			}
		}
		var messages []tg.MessageClass
		for _, id := range request.ID {
			mid := id.(*tg.InputMessageID).ID
			if mid != 40 && mid != 42 && mid != 44 {
				continue
			}
			msg := &tg.Message{ID: mid}
			msg.SetGroupedID(700)
			if mid == 42 {
				msg.Message = "caption"
			}
			messages = append(messages, msg)
		}
		// Return album members out of order to exercise sorting at the RPC boundary.
		for left, right := 0, len(messages)-1; left < right; left, right = left+1, right-1 {
			messages[left], messages[right] = messages[right], messages[left]
		}
		output.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessages{Messages: messages}
	case *tg.MessagesForwardMessagesRequest:
		i.forward = request
		output.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{
			&tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 142, Message: "caption", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 7}}}},
		}}
	case *tg.MessagesEditMessageRequest:
		i.edit = request
		output.(*tg.UpdatesBox).Updates = &tg.Updates{}
	default:
		return fmt.Errorf("unexpected request %T", input)
	}
	return nil
}

func TestForwardMessagePreservesAlbumTopicAndTaskContext(t *testing.T) {
	forwardTestCache.Do(func() {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte("workers=1\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := config.Init(t.Context(), path); err != nil {
			t.Fatal(err)
		}
		cache.Init()
	})
	for _, mode := range []string{"normal", "query_error", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			var output bytes.Buffer
			logger := log.New(&output)
			logger.SetFormatter(log.JSONFormatter)
			ctx, cancel := context.WithCancel(log.WithContext(context.WithValue(t.Context(), forwardContextKey{}, "task"), logger))
			defer cancel()
			invoker := &albumContextInvoker{failGroup: mode == "query_error"}
			if mode == "cancelled" {
				invoker.cancelGroup = cancel
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "source")
			peers.AddPeer(222, 2, peerstore.TypeChannel, "target")
			var source, target constant.TDLibPeerID
			source.Channel(111)
			target.Channel(222)
			client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers,
				Self: &tg.User{ID: 7000 + forwardTestSelf.Add(1)}}
			err := ForwardMessage(ctx, client, int64(source), int64(target), 42, 99)
			if mode == "cancelled" {
				if !errors.Is(err, context.Canceled) || invoker.forward != nil || invoker.edit != nil {
					t.Fatalf("cancelled album query fell back to sending: err=%v forward=%v edit=%v", err, invoker.forward, invoker.edit)
				}
			} else {
				if err != nil || invoker.forward == nil || invoker.edit == nil {
					t.Fatalf("album not forwarded/attributed: err=%v forward=%v edit=%v", err, invoker.forward, invoker.edit)
				}
				request := invoker.forward
				wantIDs := []int{40, 42, 44}
				if mode == "query_error" {
					wantIDs = []int{42}
				}
				if !request.DropAuthor || !reflect.DeepEqual(request.ID, wantIDs) || request.TopMsgID != 99 || len(request.RandomID) != len(wantIDs) {
					t.Fatalf("album or topic changed: %+v", request)
				}
				if invoker.edit.ID != 142 || invoker.edit.Message != "[转] caption" || len(invoker.edit.Entities) != 2 {
					t.Fatalf("caption attribution changed: %+v", invoker.edit)
				}
				link := invoker.edit.Entities[0].(*tg.MessageEntityTextURL)
				bold := invoker.edit.Entities[1].(*tg.MessageEntityBold)
				if link.URL != "https://t.me/source/42" || bold.Offset != utf16Len("[转] ") || bold.Length != 7 {
					t.Fatalf("caption link/entity changed: link=%+v bold=%+v", link, bold)
				}
			}
			if invoker.fetches == 0 || invoker.groupReads < 1 || client.Err() != nil {
				t.Fatalf("query coverage or shared context changed: fetches=%d groups=%d client=%v", invoker.fetches, invoker.groupReads, client.Err())
			}
			if mode != "query_error" && invoker.groupReads != 1 {
				t.Fatalf("normal/cancelled lookup repeated the album query: %d", invoker.groupReads)
			}
			if mode == "query_error" {
				if strings.Count(output.String(), "Media group lookup failed; forwarding only the selected message") != 1 ||
					!strings.Contains(output.String(), `"message_id":42`) || !strings.Contains(output.String(), `"group_id":700`) ||
					!strings.Contains(output.String(), `"reason":"query_error"`) {
					t.Fatalf("fallback was not reported once with source identity: %s", &output)
				}
			} else if output.Len() != 0 {
				t.Fatalf("normal/cancelled lookup incorrectly reported fallback: %s", &output)
			}
		})
	}
}

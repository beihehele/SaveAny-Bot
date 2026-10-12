package copyfwd

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestCopyPaginationUsesRawPagesAndRejectsStalledCursors(t *testing.T) {
	message := func(id int) tg.MessageClass {
		return &tg.Message{ID: id, Message: "needle", PeerID: &tg.PeerUser{UserID: 123}}
	}
	service := func(id int) tg.MessageClass {
		return &tg.MessageService{ID: id, Action: &tg.MessageActionPinMessage{}}
	}
	full := make([]tg.MessageClass, 0, 100)
	for id := 101; id >= 3; id-- {
		full = append(full, message(id))
	}
	mixed := append(append([]tg.MessageClass{}, full...), service(2))
	services := make([]tg.MessageClass, 100)
	for i := range services {
		services[i] = service(201 - i)
	}
	for _, filter := range []string{"", "msgre:needle"} {
		for _, tc := range []struct {
			name    string
			pages   [][]tg.MessageClass
			offsets []int
			count   int
			want    int
			fail    bool
		}{
			{"service at full-page boundary", [][]tg.MessageClass{mixed, {message(1)}}, []int{0, 2}, 100, 100, false},
			{"entire page is service messages", [][]tg.MessageClass{services, {message(101)}}, []int{0, 102}, 1, 1, false},
			{"repeated full page", [][]tg.MessageClass{mixed, mixed}, []int{0, 2}, 101, 0, true},
			{"duplicate within page", [][]tg.MessageClass{{message(10), message(10), message(1)}}, []int{0}, 100, 2, false},
		} {
			t.Run(fmt.Sprintf("%s/filter=%s", tc.name, filter), func(t *testing.T) {
				calls := 0
				peers := peerstore.NewPeerStorage(nil, true)
				peers.AddPeer(123, 456, peerstore.TypeUser, "")
				client := &ext.Context{Context: t.Context(), PeerStorage: peers, Raw: tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
					if calls >= len(tc.pages) {
						return fmt.Errorf("unexpected extra scan")
					}
					var offset int
					switch req := in.(type) {
					case *tg.MessagesGetHistoryRequest:
						offset = req.OffsetID
					case *tg.MessagesSearchRequest:
						offset = req.OffsetID
					default:
						return fmt.Errorf("unexpected RPC %T", req)
					}
					if offset != tc.offsets[calls] {
						return fmt.Errorf("offset=%d want=%d", offset, tc.offsets[calls])
					}
					out.(*tg.MessagesMessagesBox).Messages = &tg.MessagesMessagesSlice{Messages: tc.pages[calls], Count: 1000}
					calls++
					return nil
				}))}
				ids, err := collectMatchedIDs(t.Context(), client, 123, filter, tc.count, nil)
				if (err != nil) != tc.fail || len(ids) != tc.want || calls != len(tc.pages) {
					t.Fatalf("ids=%d want=%d calls=%d err=%v", len(ids), tc.want, calls, err)
				}
				if tc.fail && ids != nil {
					t.Fatal("failed scan exposed partial selections")
				}
				if tc.name == "duplicate within page" && !reflect.DeepEqual(ids, []int{10, 1}) {
					t.Fatalf("duplicates changed order: %v", ids)
				}
			})
		}
	}
}

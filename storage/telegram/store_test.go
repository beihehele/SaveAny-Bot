package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type archiveRPC struct {
	forwardErr error
	sendErr    error
	editErr    error
	cancel     context.CancelFunc
	forward    *tg.MessagesForwardMessagesRequest
	send       *tg.MessagesSendMultiMediaRequest
	single     *tg.MessagesSendMediaRequest
	media      []tg.InputMediaClass
	edits      int
	parts      int
	short      bool
	albums     []*tg.Message
	albumErr   error
	checks     int
	afterSend  context.CancelFunc
	afterQuery context.CancelFunc
	afterCopy  context.CancelFunc
	uploadErr  error
	failPartAt int
}

func (r *archiveRPC) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch request := in.(type) {
	case *tg.ChannelsGetMessagesRequest:
		r.checks++
		if r.albumErr != nil {
			return r.albumErr
		}
		members := r.albums
		if members == nil {
			members = []*tg.Message{{ID: 11, GroupedID: 55}, {ID: 12, GroupedID: 55}}
		}
		result := &tg.MessagesChannelMessages{}
		for _, member := range members {
			member.SetGroupedID(member.GroupedID)
			member.PeerID = &tg.PeerChannel{ChannelID: 111}
			result.Messages = append(result.Messages, member)
		}
		out.(*tg.MessagesMessagesBox).Messages = result
		if r.afterQuery != nil {
			r.afterQuery()
		}
	case *tg.MessagesForwardMessagesRequest:
		r.forward = request
		if r.forwardErr != nil {
			return r.forwardErr
		}
		out.(*tg.UpdatesBox).Updates = receipt(len(request.ID))
		if r.afterCopy != nil {
			r.afterCopy()
		}
	case *tg.UploadSaveFilePartRequest:
		r.parts++
		if r.parts == r.failPartAt {
			return r.uploadErr
		}
		if string(request.Bytes) != "data" {
			return fmt.Errorf("upload corrupt: %q", request.Bytes)
		}
		out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
	case *tg.MessagesUploadMediaRequest:
		r.media = append(r.media, request.Media)
		if r.cancel != nil {
			r.cancel()
		}
		switch m := request.Media.(type) {
		case *tg.InputMediaUploadedPhoto:
			out.(*tg.MessageMediaBox).MessageMedia = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 7}}
		case *tg.InputMediaUploadedDocument:
			out.(*tg.MessageMediaBox).MessageMedia = &tg.MessageMediaDocument{Document: &tg.Document{ID: 8, Attributes: m.Attributes}, Spoiler: m.Spoiler}
		default:
			return fmt.Errorf("unexpected uploaded media %T", m)
		}
	case *tg.MessagesSendMultiMediaRequest:
		r.send = request
		if r.sendErr != nil {
			return r.sendErr
		}
		out.(*tg.UpdatesBox).Updates = receipt(len(request.MultiMedia))
		if r.afterSend != nil {
			r.afterSend()
		}
	case *tg.MessagesSendMediaRequest:
		r.single = request
		if r.sendErr != nil {
			return r.sendErr
		}
		if r.short {
			out.(*tg.UpdatesBox).Updates = &tg.UpdateShortSentMessage{ID: 501}
		} else {
			out.(*tg.UpdatesBox).Updates = receipt(1)
		}
	case *tg.MessagesEditMessageRequest:
		r.edits++
		if r.editErr != nil {
			return r.editErr
		}
		out.(*tg.UpdatesBox).Updates = &tg.Updates{}
	default:
		return fmt.Errorf("unexpected RPC %T", request)
	}
	return nil
}

func receipt(n int) tg.UpdatesClass {
	updates := &tg.Updates{}
	for i := range n {
		updates.Updates = append(updates.Updates, &tg.UpdateNewChannelMessage{Message: &tg.Message{ID: 501 + i, PeerID: &tg.PeerChannel{ChannelID: 222}, Message: "caption", GroupedID: 99}})
	}
	return updates
}

type archiveDownload struct {
	downloader.Client
	err   error
	reads int
}

func (d *archiveDownload) UploadGetFile(ctx context.Context, _ *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	d.reads++
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.err != nil {
		return nil, d.err
	}
	return &tg.UploadFile{Bytes: []byte("data")}, nil
}

func archiveFile(id int, doc bool, d *archiveDownload) tfile.TGFileMessage {
	msg := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 111}, GroupedID: 55, Message: "舞团 🐱", Entities: []tg.MessageEntityClass{&tg.MessageEntityBold{Offset: 0, Length: 2}}}
	msg.SetGroupedID(55)
	var location tg.InputFileLocationClass = &tg.InputPhotoFileLocation{ID: int64(id)}
	msg.Media = &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(id)}, Spoiler: true}
	if doc {
		msg.Media = &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(id), MimeType: "video/mp4", Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeVideo{W: 1280, H: 720, Duration: 2}, &tg.DocumentAttributeFilename{FileName: "original.mp4"}}}}
		location = &tg.InputDocumentFileLocation{ID: int64(id)}
	}
	return tfile.NewTGFile(location, d, 4, "test.bin", tfile.WithMessage(msg)).(tfile.TGFileMessage)
}

func TestCollectionDelivery(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "config.toml")
	text := fmt.Sprintf("workers=1\nthreads=1\n[temp]\nbase_path=%q\n", filepath.ToSlash(filepath.Join(root, "cache")))
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	cache.Init()
	for _, mode := range []string{"copy", "protected_album", "protected_video", "protected_single", "network_error", "download_error", "cancel_before_send", "cancel_after_send", "send_error", "edit_error"} {
		t.Run(mode, func(t *testing.T) {
			rpc := &archiveRPC{}
			if mode != "copy" {
				rpc.forwardErr = &tgerr.Error{Code: 400, Type: "CHAT_FORWARDS_RESTRICTED"}
			}
			if mode == "network_error" {
				rpc.forwardErr = errors.New("network EOF")
			}
			if mode == "send_error" {
				rpc.sendErr = errors.New("ambiguous timeout")
			}
			if mode == "edit_error" {
				rpc.editErr = errors.New("caption too long")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel_before_send" {
				rpc.cancel = cancel
			}
			if mode == "cancel_after_send" {
				rpc.afterSend = cancel
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "")
			peers.AddPeer(222, 2, peerstore.TypeChannel, "")
			client := &ext.Context{Context: ctx, Raw: tg.NewClient(rpc), PeerStorage: peers, Self: &tg.User{ID: 77}}
			store := &Store{cfg: storcfg.TelegramStorageConfig{ChatID: -1000000000222, TopicID: 2}}
			d := &archiveDownload{}
			if mode == "download_error" {
				d.err = errors.New("protected download denied")
			}
			files := []tfile.TGFileMessage{archiveFile(12, mode == "protected_video", d), archiveFile(11, mode == "protected_video", d)}
			if mode == "protected_single" {
				files = files[:1]
				files[0].Message().GroupedID = 0
				rpc.short = true
			}
			var stages []string
			ctx = taskevent.WithSink(ctx, taskevent.SinkFunc(func(e taskevent.Event) {
				if e.DeliveryProgress != nil {
					stages = append(stages, e.DeliveryProgress.Stage)
				}
			}))
			links, err := store.saveMessages(ctx, client, files)
			failed := mode == "network_error" || mode == "download_error" || mode == "cancel_before_send" || mode == "send_error"
			if (err != nil) != failed {
				t.Fatalf("err=%v links=%v", err, links)
			}
			if !failed && (len(links) != 1 || !strings.Contains(links[0], "/222/501")) {
				t.Fatalf("missing receipt: %v", links)
			}
			if rpc.forward == nil || len(rpc.forward.ID) != len(files) || !rpc.forward.DropAuthor || rpc.forward.TopMsgID != 2 {
				t.Fatalf("incorrect copy request: %+v", rpc.forward)
			}
			if mode == "copy" || mode == "network_error" {
				if d.reads != 0 || rpc.parts != 0 || rpc.send != nil {
					t.Fatal("copy/network error unexpectedly downloaded/uploaded")
				}
			}
			if mode == "protected_album" || mode == "protected_video" || mode == "edit_error" {
				if rpc.send == nil || len(rpc.send.MultiMedia) != 2 || rpc.parts != 2 {
					t.Fatalf("not sent as one album: %+v", rpc)
				}
				if rpc.send.ReplyTo.(*tg.InputReplyToMessage).ReplyToMsgID != 2 {
					t.Fatal("topic lost")
				}
				for _, item := range rpc.send.MultiMedia {
					if item.RandomID == 0 || item.Message != "舞团 🐱" || len(item.Entities) != 1 {
						t.Fatalf("caption/entity/ID lost: %+v", item)
					}
					if mode != "protected_video" && !item.Media.(*tg.InputMediaPhoto).Spoiler {
						t.Fatal("source spoiler lost after uploadMedia")
					}
				}
				if mode == "protected_video" {
					media := rpc.media[0].(*tg.InputMediaUploadedDocument)
					if media.MimeType != "video/mp4" || len(media.Attributes) != 2 {
						t.Fatal("video attributes lost")
					}
				}
			}
			if mode == "protected_single" && rpc.single == nil {
				t.Fatal("single media not sent")
			}
			if mode == "cancel_before_send" && (rpc.send != nil || !errors.Is(err, context.Canceled)) {
				t.Fatalf("cancel sent content: %v", err)
			}
			if mode == "cancel_after_send" && (rpc.send == nil || len(links) != 1) {
				t.Fatal("confirmed album lost its receipt during cancellation")
			}
			if mode == "download_error" && (rpc.parts != 0 || rpc.send != nil) {
				t.Fatal("failed download still uploaded/sent")
			}
			entries, readErr := os.ReadDir(config.C().Temp.BasePath)
			if readErr != nil && !os.IsNotExist(readErr) || len(entries) != 0 {
				t.Fatalf("cache left behind: %v %v", entries, readErr)
			}
			if len(stages) == 0 {
				t.Fatal("missing delivery progress")
			}
		})
	}
}

func TestCollectionRejectsIncompleteAlbumBeforePublishing(t *testing.T) {
	for _, mode := range []string{"missing", "changed", "query_failed"} {
		t.Run(mode, func(t *testing.T) {
			rpc := &archiveRPC{albums: []*tg.Message{{ID: 11, GroupedID: 55}, {ID: 12, GroupedID: 55}, {ID: 13, GroupedID: 55}}}
			if mode == "changed" {
				rpc.albums = []*tg.Message{{ID: 11, GroupedID: 55}, {ID: 13, GroupedID: 55}}
			}
			if mode == "query_failed" {
				rpc.albumErr = errors.New("source unavailable")
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "")
			client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(rpc), PeerStorage: peers}
			d := &archiveDownload{}
			store := &Store{cfg: storcfg.TelegramStorageConfig{ChatID: -1000000000222}}
			_, err := store.saveMessages(t.Context(), client, []tfile.TGFileMessage{archiveFile(11, false, d), archiveFile(12, false, d)})
			if err == nil || rpc.checks != 1 || rpc.forward != nil || rpc.send != nil || rpc.parts != 0 || d.reads != 0 {
				t.Fatalf("partial album published or downloaded: err=%v rpc=%+v", err, rpc)
			}
		})
	}
}

func TestCollectionRejectsMixedAndDuplicateMessages(t *testing.T) {
	d := &archiveDownload{}
	a, b := archiveFile(11, false, d), archiveFile(12, false, d)
	b.Message().GroupedID = 66
	if _, _, err := orderedMessages([]tfile.TGFileMessage{a, b}); err == nil {
		t.Fatal("mixed groups accepted")
	}
	if _, _, err := orderedMessages([]tfile.TGFileMessage{a, a}); err == nil {
		t.Fatal("duplicate member accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := new(Store).saveMessages(ctx, nil, []tfile.TGFileMessage{a}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled task proceeded: %v", err)
	}
}

func TestCommandTargetDoesNotChangeConfiguredDestination(t *testing.T) {
	store := &Store{cfg: storcfg.TelegramStorageConfig{BaseConfig: storcfg.BaseConfig{Name: "TG收藏"}, ChatID: -1000000000222, TopicID: 2}}
	override, err := store.WithTarget(-1000000000333, 0)
	if err != nil || override.cfg.ChatID != -1000000000333 || override.cfg.TopicID != 0 {
		t.Fatalf("override: %+v %v", override, err)
	}
	if store.cfg.ChatID != -1000000000222 || store.cfg.TopicID != 2 {
		t.Fatal("command changed configured destination")
	}
	if _, err := store.WithTarget(0, 0); err == nil {
		t.Fatal("invalid override accepted")
	}
}

func TestBotPrivateAttachmentsDoNotForwardUserbotMessageIDs(t *testing.T) {
	d := &archiveDownload{}
	f := archiveFile(12, false, d)
	f.Message().GroupedID = 0
	f.Message().PeerID = &tg.PeerUser{UserID: 77}
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(222, 2, peerstore.TypeChannel, "")
	rpc := &archiveRPC{}
	client := &ext.Context{Context: t.Context(), Raw: tg.NewClient(rpc), PeerStorage: peers, Self: &tg.User{ID: 77}}
	store := &Store{cfg: storcfg.TelegramStorageConfig{ChatID: -1000000000222}}
	// Setup independently so this test can be run alone.
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte(fmt.Sprintf("threads=1\n[temp]\nbase_path=%q\n", filepath.ToSlash(t.TempDir()))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	if _, err := store.saveMessages(t.Context(), client, []tfile.TGFileMessage{f}); err != nil {
		t.Fatal(err)
	}
	if rpc.forward != nil || rpc.single == nil || rpc.edits != 0 {
		t.Fatal("private attachment forwarded wrong IDs or invented a source link")
	}
	header := tg.MessageFwdHeader{FromID: &tg.PeerChannel{ChannelID: 111}, ChannelPost: 5}
	f.Message().SetFwdFrom(header)
	if link := collectionSourceLink(client, []tfile.TGFileMessage{f}, 77, 12); link != "https://t.me/c/111/5" {
		t.Fatalf("forwarded origin lost: %s", link)
	}
}

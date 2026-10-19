package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestForwardedBotAlbumUsesVerifiedOriginals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cache := t.TempDir()
	if err := os.WriteFile(path, []byte(fmt.Sprintf("threads=1\n[temp]\nbase_path=%q\n", filepath.ToSlash(cache))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"photos", "videos", "ten_members", "single", "cancel_after_copy", "missing_header", "mixed_sources", "reordered", "duplicate_origin", "deleted_source", "extra_member", "missing_member", "changed_media", "changed_caption", "changed_entities", "changed_spoiler", "single_from_album", "cancel_lookup", "copy_network_error", "copy_restricted", "restricted_upload", "restricted_second_upload_fails"} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			downloadErr := errors.New("received attachment downloader")
			d := &archiveDownload{err: downloadErr}
			var files []tfile.TGFileMessage
			rpc := &archiveRPC{}
			count := 2
			if name == "ten_members" {
				count = 10
			}
			if name == "single" || name == "single_from_album" {
				count = 1
			}
			for i := range count {
				original := archiveFile(11+i, name == "videos", d).Message()
				rpc.albums = append(rpc.albums, original)
				file := archiveFile(11+i, name == "videos", d)
				msg := file.Message()
				msg.ID = 101 + i // Bot-private IDs deliberately differ from source IDs.
				msg.PeerID = &tg.PeerUser{UserID: 77}
				msg.SetFwdFrom(tg.MessageFwdHeader{FromID: &tg.PeerChannel{ChannelID: 111}, ChannelPost: original.ID})
				if count == 1 {
					msg.GroupedID = 0
					if name == "single" {
						original.GroupedID = 0
					}
				}
				files = append(files, file)
			}
			switch name {
			case "missing_header":
				files[1].Message().SetFwdFrom(tg.MessageFwdHeader{})
			case "mixed_sources":
				header := files[1].Message().FwdFrom
				header.FromID = &tg.PeerChannel{ChannelID: 333}
				files[1].Message().SetFwdFrom(header)
			case "reordered":
				files[0].Message().FwdFrom.ChannelPost, files[1].Message().FwdFrom.ChannelPost = 12, 11
			case "duplicate_origin":
				files[1].Message().FwdFrom.ChannelPost = 11
			case "deleted_source":
				rpc.albumErr = tgerr.New(400, "CHANNEL_PRIVATE")
			case "extra_member":
				rpc.albums = append(rpc.albums, archiveFile(13, false, d).Message())
			case "missing_member":
				rpc.albums = rpc.albums[:1]
			case "changed_media":
				rpc.albums[1].Media.(*tg.MessageMediaPhoto).Photo.(*tg.Photo).ID = 999
			case "changed_caption":
				rpc.albums[1].Message = "edited"
			case "changed_entities":
				rpc.albums[1].Entities = nil
			case "changed_spoiler":
				rpc.albums[1].Media.(*tg.MessageMediaPhoto).Spoiler = false
			case "cancel_lookup":
				rpc.afterQuery = cancel
			case "cancel_after_copy":
				rpc.afterCopy = cancel
			case "copy_network_error":
				rpc.forwardErr = errors.New("ambiguous copy EOF")
			case "copy_restricted":
				rpc.forwardErr = tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")
			case "restricted_upload", "restricted_second_upload_fails":
				rpc.forwardErr = tgerr.New(400, "CHAT_FORWARDS_RESTRICTED")
				d.err = nil
				if name == "restricted_second_upload_fails" {
					rpc.failPartAt = 2
					rpc.uploadErr = errors.New("second upload disconnected")
				}
			}
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(111, 1, peerstore.TypeChannel, "")
			peers.AddPeer(222, 2, peerstore.TypeChannel, "")
			client := &ext.Context{Context: ctx, Raw: tg.NewClient(rpc), PeerStorage: peers}
			store := &Store{cfg: storcfg.TelegramStorageConfig{ChatID: -1000000000222, TopicID: 2}}
			// Input sorting must use Bot IDs; original IDs are checked independently.
			if count > 1 {
				files[0], files[count-1] = files[count-1], files[0]
			}
			links, err := store.saveMessages(ctx, client, files)
			success := name == "photos" || name == "videos" || name == "ten_members" || name == "single" || name == "cancel_after_copy"
			if success {
				if err != nil || len(links) != 1 || d.reads != 0 || rpc.parts != 0 || rpc.send != nil {
					t.Fatalf("verified original was downloaded or not copied: %v rpc=%+v", err, rpc)
				}
				want := make([]int, count)
				for i := range want {
					want[i] = 11 + i
				}
				if rpc.forward == nil || !reflect.DeepEqual(rpc.forward.ID, want) || !rpc.forward.DropAuthor || rpc.forward.TopMsgID != 2 || rpc.forward.FromPeer.(*tg.InputPeerChannel).ChannelID != 111 {
					t.Fatalf("incorrect original IDs/peer/topic: %+v", rpc.forward)
				}
			} else if name == "cancel_lookup" {
				if !errors.Is(err, context.Canceled) || d.reads != 0 || rpc.forward != nil {
					t.Fatalf("cancelled lookup fell through: %v", err)
				}
			} else if name == "copy_network_error" {
				if !errors.Is(err, rpc.forwardErr) || d.reads != 0 || rpc.forward == nil {
					t.Fatalf("ambiguous send retried through upload: %v", err)
				}
			} else if name == "restricted_upload" || name == "restricted_second_upload_fails" {
				if rpc.forward == nil || !reflect.DeepEqual(rpc.forward.ID, []int{11, 12}) || d.reads != 2 || rpc.parts != 2 {
					t.Fatalf("wrong origin IDs or received attachment downloader: %+v reads=%d", rpc.forward, d.reads)
				}
				if name == "restricted_upload" {
					if err != nil || len(links) != 1 || rpc.send == nil || len(rpc.send.MultiMedia) != 2 || rpc.send.ReplyTo.(*tg.InputReplyToMessage).ReplyToMsgID != 2 {
						t.Fatalf("received album fallback failed: %v send=%+v", err, rpc.send)
					}
					for _, item := range rpc.send.MultiMedia {
						if item.Message != "舞团 🐱" || len(item.Entities) != 1 || !item.Media.(*tg.InputMediaPhoto).Spoiler {
							t.Fatal("fallback changed caption, entities or spoiler")
						}
					}
				} else if !errors.Is(err, rpc.uploadErr) || rpc.send != nil || len(links) != 0 || len(rpc.media) != 1 {
					t.Fatalf("second upload failure published a partial album: %v", err)
				}
			} else {
				if !errors.Is(err, downloadErr) || d.reads != 1 || rpc.send != nil || rpc.parts != 0 {
					t.Fatalf("fallback did not preserve the received downloader: %v", err)
				}
				if (rpc.forward != nil) != (name == "copy_restricted") {
					t.Fatalf("unverified original was copied: %+v", rpc.forward)
				}
			}
			entries, readErr := os.ReadDir(cache)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("fallback left local cache: %v %v", entries, readErr)
			}
		})
	}
}

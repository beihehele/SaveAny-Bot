package telegram

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/rpc"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/client/middleware/recovery"
	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type payloadDownload struct {
	downloader.Client
	data []byte
}

func (d payloadDownload) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	end := min(req.Offset+int64(req.Limit), int64(len(d.data)))
	return &tg.UploadFile{Bytes: d.data[req.Offset:end]}, nil
}

type partRPC struct {
	archiveRPC
	mode   string
	calls  map[int]int
	data   map[int][]byte
	fileID int64
	cancel context.CancelFunc
}

func (r *partRPC) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if req, ok := in.(*tg.UploadSaveBigFilePartRequest); ok {
		if err := ctx.Err(); err != nil {
			return err
		}
		if r.fileID == 0 {
			r.fileID = req.FileID
		}
		if req.FileID != r.fileID {
			return errors.New("retry changed upload identity")
		}
		r.calls[req.FilePart]++
		if req.FilePart == 2 {
			if r.mode == "cancel" {
				r.cancel()
				return ctx.Err()
			}
			if r.mode == "closed" || (r.mode == "recovered" && r.calls[req.FilePart] == 1) {
				return rpc.ErrEngineClosed
			}
		}
		r.data[req.FilePart] = append([]byte(nil), req.Bytes...)
		out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		return nil
	}
	return r.archiveRPC.Invoke(ctx, in, out)
}

func TestCollectionUploadPartsAndTransportFailure(t *testing.T) {
	cache := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("threads=1\n[temp]\nbase_path=%q\n", filepath.ToSlash(cache))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	// Big-file RPCs and an incomplete final part, without network access.
	payload := bytes.Repeat([]byte("sample-media-"), 1<<20)
	payload = append(payload, []byte("final part")...)
	for _, mode := range []string{"success", "closed", "recovered", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &partRPC{mode: mode, calls: make(map[int]int), data: make(map[int][]byte), cancel: cancel}
			// Same existing recovery policy, shortened deterministically for this test.
			invoker := recovery.New(ctx, func() backoff.BackOff {
				return backoff.WithMaxRetries(backoff.NewConstantBackOff(0), 1)
			}).Handle(r)
			peers := peerstore.NewPeerStorage(nil, true)
			peers.AddPeer(222, 2, peerstore.TypeChannel, "")
			client := &ext.Context{Context: ctx, Raw: tg.NewClient(invoker), PeerStorage: peers}
			msg := archiveFile(101, true, nil).Message()
			msg.GroupedID = 0
			msg.PeerID = &tg.PeerUser{UserID: 77}
			file := tfile.NewTGFile(&tg.InputDocumentFileLocation{}, payloadDownload{data: payload}, int64(len(payload)), "video.mp4", tfile.WithMessage(msg)).(tfile.TGFileMessage)
			store := &Store{cfg: storcfg.TelegramStorageConfig{ChatID: -1000000000222}}
			_, err := store.saveMessages(ctx, client, []tfile.TGFileMessage{file})
			if mode == "closed" || mode == "cancel" {
				want := rpc.ErrEngineClosed
				if mode == "cancel" {
					want = context.Canceled
				}
				if !errors.Is(err, want) || r.single != nil || r.send != nil || len(r.media) != 0 {
					t.Fatalf("failed upload published content or lost error: %v", err)
				}
				if !strings.Contains(err.Error(), "1048576/") || r.calls[2] > 2 {
					t.Fatalf("missing confirmed-byte diagnostic or excess retries: %v calls=%v", err, r.calls)
				}
			} else {
				if err != nil || r.single == nil || len(r.media) != 1 {
					t.Fatalf("upload failed: %v", err)
				}
				parts := (len(payload) + uploader.MaximumPartSize - 1) / uploader.MaximumPartSize
				var received []byte
				for i := range parts {
					received = append(received, r.data[i]...)
					if len(r.data[i]) != uploader.MaximumPartSize && i != parts-1 {
						t.Fatal("upload does not use full-size parts")
					}
				}
				if len(r.data) != parts || !bytes.Equal(received, payload) {
					t.Fatal("uploaded content or part count changed")
				}
				if mode == "recovered" && r.calls[2] != 2 {
					t.Fatal("transport recovery did not retry the same part")
				}
			}
			entries, readErr := os.ReadDir(cache)
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("upload cache retained: %v %v", entries, readErr)
			}
		})
	}
}

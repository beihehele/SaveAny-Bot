// Package telegram archives media to one configured Telegram group or channel.
package telegram

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	userclient "github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/tdler"
	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	"github.com/krau/SaveAny-Bot/common/utils/ioutil"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type Store struct{ cfg storcfg.TelegramStorageConfig }

// WithTarget returns a request-local destination without changing configured storage.
func (s *Store) WithTarget(chatID int64, topicID int) (*Store, error) {
	copy := *s
	copy.cfg.ChatID, copy.cfg.TopicID = chatID, topicID
	if err := copy.cfg.Validate(); err != nil {
		return nil, err
	}
	return &copy, nil
}

// Destination describes the configured or command-local collection endpoint.
func (s *Store) Destination() string {
	label := fmt.Sprintf("%s(%d)", s.Name(), s.cfg.ChatID)
	if s.cfg.TopicID > 0 {
		label += fmt.Sprintf(" Topic:%d", s.cfg.TopicID)
	}
	return label
}

func (s *Store) Init(_ context.Context, cfg storcfg.StorageConfig) error {
	c, ok := cfg.(*storcfg.TelegramStorageConfig)
	if !ok {
		return fmt.Errorf("invalid telegram storage config")
	}
	copy := *c
	if err := copy.Validate(); err != nil {
		return err
	}
	s.cfg = copy
	return nil
}
func (s *Store) Name() string                  { return s.cfg.Name }
func (s *Store) Type() storenum.StorageType    { return storenum.Telegram }
func (s *Store) CannotStream() string          { return "archives original Telegram messages" }
func (s *Store) CannotDetectExistence() string { return "Telegram messages have no file paths" }
func (s *Store) Exists(context.Context, string) (bool, error) {
	return false, fmt.Errorf("telegram storage has no file paths")
}
func (s *Store) Save(context.Context, io.Reader, string) error {
	return fmt.Errorf("telegram storage requires original messages")
}

// SaveMessages never retries a send with fresh IDs, and only falls back after
// Telegram explicitly rejects forwarding. Cache/attribution errors cannot resend.
func (s *Store) SaveMessages(ctx context.Context, files []tfile.TGFileMessage) ([]string, error) {
	return s.saveMessages(ctx, userclient.GetCtx(), files)
}

func (s *Store) saveMessages(ctx context.Context, client *ext.Context, files []tfile.TGFileMessage) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if client == nil {
		return nil, fmt.Errorf("TG collection requires an initialized Userbot")
	}
	files, source, err := orderedMessages(files)
	if err != nil {
		return nil, err
	}
	if source == s.cfg.ChatID {
		return nil, fmt.Errorf("collection source and target must differ")
	}
	client = tgutil.ClientWithContext(ctx, client)
	ids := make([]int, len(files))
	linkID := files[0].Message().ID
	for i, f := range files {
		ids[i] = f.Message().ID
		if strings.TrimSpace(f.Message().Message) != "" {
			linkID = f.Message().ID
		}
	}

	var messages []*tg.Message
	// Bot-private IDs belong to the Bot's dialog, not the Userbot's. Use
	// their original downloader rather than forwarding unrelated Userbot IDs.
	_, private := files[0].Message().PeerID.(*tg.PeerUser)
	if !private {
		if files[0].Message().GroupedID != 0 {
			// Watch updates and link parsing can yield only part of an album.
			// Compare the selection with a fresh source read before publishing;
			// never expand it here, which would bypass per-file storage rules.
			members, err := tgutil.GetGroupedMessages(client, source, files[0].Message())
			if err != nil {
				return nil, fmt.Errorf("verify collection album: %w", err)
			}
			if len(members) != len(ids) {
				return nil, fmt.Errorf("collection album selection is incomplete or changed; reload the whole album")
			}
			for i, member := range members {
				if member.ID != ids[i] {
					return nil, fmt.Errorf("collection album selection is incomplete or changed; reload the whole album")
				}
			}
		}
		delivery(ctx, "copying", 0, 0, nil)
		messages, err = userclient.CopyMessages(ctx, client, source, s.cfg.ChatID, ids, s.cfg.TopicID, linkID)
		if err != nil && !tgerr.Is(err, "CHAT_FORWARDS_RESTRICTED") {
			return nil, err
		}
	}
	if private || err != nil {
		messages, err = s.uploadMessages(ctx, client, files, source, linkID)
		if err != nil {
			return nil, err
		}
	}

	// An incomplete receipt is not permission to send again.
	if len(messages) != len(files) {
		return nil, fmt.Errorf("Telegram returned %d/%d target messages; delivery may have completed, check target before retrying", len(messages), len(files))
	}
	sort.Slice(messages, func(i, j int) bool { return messages[i].ID < messages[j].ID })
	link, err := tgutil.BuildMessageLink(client, s.cfg.ChatID, messages[0].ID)
	if err != nil {
		log.FromContext(ctx).Warn("Collection delivered but link unavailable", "error", err)
		return nil, nil
	}
	links := []string{link}
	delivery(ctx, "sent", 0, 0, links)
	return links, nil
}

func orderedMessages(files []tfile.TGFileMessage) ([]tfile.TGFileMessage, int64, error) {
	if len(files) == 0 || len(files) > 10 {
		return nil, 0, fmt.Errorf("collection needs 1–10 messages from one album")
	}
	files = append([]tfile.TGFileMessage(nil), files...)
	var source int64
	var group int64
	seen := make(map[int]bool)
	for i, f := range files {
		if f == nil || f.Message() == nil || f.Message().PeerID == nil || f.Message().ID <= 0 {
			return nil, 0, fmt.Errorf("collection requires original source messages")
		}
		msg := f.Message()
		peer := constant.TDLibPeerID(0)
		switch p := msg.PeerID.(type) {
		case *tg.PeerChannel:
			peer.Channel(p.ChannelID)
		case *tg.PeerChat:
			peer.Chat(p.ChatID)
		case *tg.PeerUser:
			peer.User(p.UserID)
		default:
			return nil, 0, fmt.Errorf("unsupported source peer")
		}
		if i == 0 {
			source, group = int64(peer), msg.GroupedID
		}
		if int64(peer) != source || (len(files) > 1 && (group == 0 || msg.GroupedID != group)) || seen[msg.ID] {
			return nil, 0, fmt.Errorf("collection messages must be distinct members of one source album")
		}
		seen[msg.ID] = true
	}
	if group != 0 && len(files) == 1 {
		return nil, 0, fmt.Errorf("TG collection requires the whole album; use its normal link without ?single")
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Message().ID < files[j].Message().ID })
	return files, source, nil
}

func delivery(ctx context.Context, stage string, bytes, total int64, links []string) {
	taskevent.Emit(ctx, taskevent.Event{TaskID: storagetypes.SaveRequestFrom(ctx).TaskID, Phase: taskevent.PhaseProgress,
		DeliveryProgress: &taskevent.DeliveryProgress{Stage: stage, Bytes: bytes, TotalBytes: total}, ResultLinks: links})
}

func (s *Store) uploadMessages(ctx context.Context, client *ext.Context, files []tfile.TGFileMessage, source int64, linkID int) ([]*tg.Message, error) {
	peer, err := client.ResolveInputPeerById(s.cfg.ChatID)
	if err != nil {
		return nil, fmt.Errorf("resolve collection target: %w", err)
	}
	link := collectionSourceLink(client, files, source, linkID)
	items := make([]tg.InputSingleMedia, 0, len(files))
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		media, err := s.uploadFile(ctx, client, peer, f)
		if err != nil {
			return nil, fmt.Errorf("prepare %s: %w", f.Name(), err)
		}
		var bytes [8]byte
		if _, err := rand.Read(bytes[:]); err != nil {
			return nil, err
		}
		item := tg.InputSingleMedia{Media: media, RandomID: int64(binary.LittleEndian.Uint64(bytes[:])), Message: f.Message().Message}
		entities := f.Message().Entities
		if len(entities) > 0 {
			item.SetEntities(entities)
		}
		items = append(items, item)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	delivery(ctx, "sending", 0, 0, nil)
	var updates tg.UpdatesClass
	if len(items) == 1 {
		req := &tg.MessagesSendMediaRequest{Peer: peer, Media: items[0].Media, Message: items[0].Message, RandomID: items[0].RandomID}
		if len(items[0].Entities) > 0 {
			req.SetEntities(items[0].Entities)
		}
		if s.cfg.TopicID > 0 {
			req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: s.cfg.TopicID})
		}
		updates, err = client.Raw.MessagesSendMedia(ctx, req)
	} else {
		req := &tg.MessagesSendMultiMediaRequest{Peer: peer, MultiMedia: items}
		if s.cfg.TopicID > 0 {
			req.SetReplyTo(&tg.InputReplyToMessage{ReplyToMsgID: s.cfg.TopicID})
		}
		updates, err = client.Raw.MessagesSendMultiMedia(ctx, req)
	}
	if err != nil {
		return nil, fmt.Errorf("send collection (check target before retrying on network errors): %w", err)
	}
	if link != "" {
		if err := userclient.AppendAttribution(ctx, client, peer, updates, link); err != nil {
			log.FromContext(ctx).Warn("Collection delivered but attribution edit failed", "error", err)
		}
	}
	return userclient.MessagesFromUpdates(updates), nil
}

func (s *Store) uploadFile(ctx context.Context, client *ext.Context, peer tg.InputPeerClass, file tfile.TGFileMessage) (tg.InputMediaClass, error) {
	// Random cache names are independent of captions/filenames. Each cache is
	// removed once uploaded; no target messages are published until all are ready.
	cache, err := fsutil.CreateTempFile(config.C().Temp.BasePath, "telegram-*")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := cache.CloseAndRemove(); err != nil {
			log.FromContext(ctx).Warn("Remove collection cache", "error", err)
		}
	}()
	delivery(ctx, "downloading", 0, file.Size(), nil)
	var downloaded atomic.Int64
	writer := ioutil.NewProgressWriterAt(cache, func(n int) { delivery(ctx, "downloading", downloaded.Add(int64(n)), file.Size(), nil) })
	if _, err = tdler.NewDownloader(file).Parallel(ctx, writer); err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	stat, err := cache.Stat()
	if err != nil {
		return nil, err
	}
	if file.Size() > 0 && stat.Size() != file.Size() {
		return nil, fmt.Errorf("incomplete download: %d/%d bytes", stat.Size(), file.Size())
	}
	if _, err = cache.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	delivery(ctx, "uploading", 0, stat.Size(), nil)
	uploaded, err := uploader.NewUploader(client.Raw).WithThreads(config.C().Threads).WithProgress(uploadProgress{}).
		Upload(ctx, uploader.NewUpload(file.Name(), cache, stat.Size()))
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	media, err := uploadedMedia(file.Message(), uploaded)
	if err != nil {
		return nil, err
	}
	// sendMultiMedia requires server-side photo/document references, not raw
	// uploaded constructors. uploadMedia does not publish a message.
	prepared, err := client.Raw.MessagesUploadMedia(ctx, &tg.MessagesUploadMediaRequest{Peer: peer, Media: media})
	if err != nil {
		return nil, fmt.Errorf("upload media: %w", err)
	}
	// uploadMedia's reply describes the stored file; keep display flags from
	// the source message when constructing the final send.
	var spoiler bool
	switch source := file.Message().Media.(type) {
	case *tg.MessageMediaPhoto:
		spoiler = source.Spoiler
	case *tg.MessageMediaDocument:
		spoiler = source.Spoiler
	}
	switch m := prepared.(type) {
	case *tg.MessageMediaPhoto:
		if p, ok := m.Photo.(*tg.Photo); ok {
			return &tg.InputMediaPhoto{ID: &tg.InputPhoto{ID: p.ID, AccessHash: p.AccessHash, FileReference: p.FileReference}, Spoiler: spoiler}, nil
		}
	case *tg.MessageMediaDocument:
		if d, ok := m.Document.(*tg.Document); ok {
			return &tg.InputMediaDocument{ID: &tg.InputDocument{ID: d.ID, AccessHash: d.AccessHash, FileReference: d.FileReference}, Spoiler: spoiler}, nil
		}
	}
	return nil, fmt.Errorf("Telegram returned unsupported uploaded media %T", prepared)
}

type uploadProgress struct{}

func (uploadProgress) Chunk(ctx context.Context, state uploader.ProgressState) error {
	delivery(ctx, "uploading", state.Uploaded, state.Total, nil)
	return ctx.Err()
}

func uploadedMedia(msg *tg.Message, file tg.InputFileClass) (tg.InputMediaClass, error) {
	switch m := msg.Media.(type) {
	case *tg.MessageMediaPhoto:
		return &tg.InputMediaUploadedPhoto{File: file, Spoiler: m.Spoiler}, nil
	case *tg.MessageMediaDocument:
		d, ok := m.Document.(*tg.Document)
		if !ok {
			return nil, fmt.Errorf("source document unavailable")
		}
		return &tg.InputMediaUploadedDocument{File: file, MimeType: d.MimeType, Attributes: d.Attributes, Spoiler: m.Spoiler}, nil
	default:
		return nil, fmt.Errorf("unsupported collection media %T", msg.Media)
	}
}

func collectionSourceLink(client *ext.Context, files []tfile.TGFileMessage, source int64, linkID int) string {
	if _, private := files[0].Message().PeerID.(*tg.PeerUser); private {
		// New Bot attachments have no source URL. Only retain a link when
		// Telegram supplied the original forwarded channel post.
		found := false
		for _, file := range files {
			header, ok := file.Message().GetFwdFrom()
			if !ok || header.ChannelPost <= 0 {
				continue
			}
			if peer, ok := header.FromID.(*tg.PeerChannel); ok {
				var id constant.TDLibPeerID
				id.Channel(peer.ChannelID)
				source, linkID, found = int64(id), header.ChannelPost, true
				break
			}
		}
		if !found {
			return ""
		}
	}
	link, err := tgutil.BuildMessageLink(client, source, linkID)
	if err != nil {
		log.FromContext(client).Warn("Collection source link unavailable", "error", err)
		return ""
	}
	return link
}

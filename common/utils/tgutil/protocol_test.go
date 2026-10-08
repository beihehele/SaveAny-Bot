package tgutil

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"testing"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/proto"
	"github.com/gotd/td/tg"
)

func TestPlaintextDecodeRejectsInvalidLengthBeforeAllocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length int32
	}{
		{"negative", -1},
		{"truncated", 1024},
		{"large truncated", 1 << 20},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if value := recover(); value != nil {
					t.Errorf("malformed plaintext packet panicked: %v", value)
				}
			}()
			buffer := new(bin.Buffer)
			buffer.PutLong(0)
			buffer.PutLong(1)
			buffer.PutInt32(tc.length)
			previous := []byte("previous message")
			message := proto.UnencryptedMessage{MessageData: bytes.Clone(previous)}
			err := message.Decode(buffer)
			if tc.length < 0 {
				var invalid *bin.InvalidLengthError
				if !errors.As(err, &invalid) {
					t.Fatalf("negative length error=%v", err)
				}
			} else if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("truncated payload error=%v", err)
			}
			if !bytes.Equal(message.MessageData, previous) {
				t.Fatalf("invalid length allocated/replaced message buffer: len=%d", len(message.MessageData))
			}
		})
	}
}

func TestLayer225MediaMessagePreservesStableRoutingFields(t *testing.T) {
	// Generated once from the stable v0.143.0 Message encoder. Only the first
	// four bytes (the layer-225 Message constructor) were changed. Keeping the
	// literal payload independent of the new encoder guards existing album,
	// caption, attribution and topic fields against decoder layout regressions.
	const wire = "2b6fef958c0302000000000017000000221751592a000000000000001e37a5a2de00000000000000bbf44d4e010000001e37a5a24d01000000000000fff0536566dd971b1a000000070000000700000000f153650763617074696f6ed9ccd8520100000071c8f836630000000000000015c4b51c01000000c90b61bd00000000070000008403000000000000"
	payload, err := hex.DecodeString(wire)
	if err != nil {
		t.Fatal(err)
	}
	buffer := &bin.Buffer{Buf: payload}
	var message tg.Message
	if err := message.Decode(buffer); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != 0 || message.ID != 23 || message.Message != "caption" {
		t.Fatalf("message or payload boundary changed: %+v remaining=%d", message, buffer.Len())
	}
	if group, ok := message.GetGroupedID(); !ok || group != 900 {
		t.Fatalf("album group=%d present=%v", group, ok)
	}
	from, ok := message.FromID.(*tg.PeerUser)
	if !ok || from.UserID != 42 {
		t.Fatalf("sender changed: %v", message.FromID)
	}
	peer, ok := message.PeerID.(*tg.PeerChannel)
	if !ok || peer.ChannelID != 222 {
		t.Fatalf("chat changed: %v", message.PeerID)
	}
	reply, ok := message.ReplyTo.(*tg.MessageReplyHeader)
	if !ok || !reply.ForumTopic || reply.ReplyToMsgID != 7 || reply.ReplyToTopID != 7 {
		t.Fatalf("topic changed: %v", message.ReplyTo)
	}
	forward, ok := message.FwdFrom.FromID.(*tg.PeerChannel)
	if !ok || forward.ChannelID != 333 || message.FwdFrom.Date != 1699999999 {
		t.Fatalf("forward attribution changed: %+v", message.FwdFrom)
	}
	media, ok := message.Media.(*tg.MessageMediaDocument)
	if !ok {
		t.Fatalf("media changed: %v", message.Media)
	}
	document, ok := media.Document.(*tg.DocumentEmpty)
	if !ok || document.ID != 99 {
		t.Fatalf("document changed: %v", media.Document)
	}
	if len(message.Entities) != 1 {
		t.Fatalf("caption entities=%v", message.Entities)
	}
	entity, ok := message.Entities[0].(*tg.MessageEntityBold)
	if !ok || entity.Offset != 0 || entity.Length != 7 {
		t.Fatalf("caption formatting changed: %v", message.Entities[0])
	}
}

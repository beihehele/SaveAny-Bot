package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/constant"
	"github.com/gotd/td/tg"
	"github.com/krau/ffmpeg-go"
	"github.com/yapingcat/gomedia/go-mp4"
)

type VideoMetadata struct {
	Duration int
	Width    int
	Height   int
}

// a go native way to get mp4 video metadata
func getMP4Meta(rs io.ReadSeeker) (metadata *VideoMetadata, err error) {
	// Recover from panics in the gomedia library (e.g., "no vosdata" panic)
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic while parsing MP4: %v", r)
		}
	}()

	d := mp4.CreateMp4Demuxer(rs)

	tracks, e := d.ReadHead()
	if e != nil {
		return nil, e
	}

	for _, track := range tracks {
		if track.Cid == mp4.MP4_CODEC_H264 {
			info := d.GetMp4Info()
			return &VideoMetadata{
				Duration: int(info.Duration / info.Timescale),
				Width:    int(track.Width),
				Height:   int(track.Height),
			}, nil
		}
	}

	return nil, fmt.Errorf("no h264 track found")
}

// getVideoMetadata uses ffprobe to get video metadata
func getVideoMetadata(ctx context.Context, rs io.ReadSeeker) (*VideoMetadata, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error",
		"-select_streams", "v:0", "-show_entries", "stream=width,height:format=duration", "-of", "json", "-")
	result, err := runMediaCommand(ctx, rs, cmd)
	if err != nil {
		return nil, err
	}

	var data struct {
		Streams []struct {
			Width  int `json:"width"`
			Height int `json:"height"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(result, &data); err != nil {
		return nil, err
	}

	// 转换 duration
	var durationFloat float64
	if data.Format.Duration != "" {
		fmt.Sscanf(data.Format.Duration, "%f", &durationFloat)
	}

	meta := &VideoMetadata{
		Duration: int(durationFloat),
	}

	if len(data.Streams) > 0 {
		meta.Width = data.Streams[0].Width
		meta.Height = data.Streams[0].Height
	}

	return meta, nil
}

func extractThumbFrame(ctx context.Context, rs io.ReadSeeker) ([]byte, error) {
	// Both timestamps share one budget; fallback must not prolong cancellation.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	data, err := extractFrameAt(ctx, rs, 1.0)
	if err == nil && len(data) > 0 {
		return data, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return extractFrameAt(ctx, rs, 0.0)
}

func extractFrameAt(ctx context.Context, rs io.ReadSeeker, timestamp float64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stream := ffmpeg.
		Input("pipe:0", ffmpeg.KwArgs{
			"ss": fmt.Sprintf("%.3f", timestamp),
		}).
		Output("pipe:1", ffmpeg.KwArgs{
			"vframes": 1,
			"f":       "mjpeg",
		})
	stream.Context = ctx
	return runMediaCommand(ctx, rs, stream.OverWriteOutput().Compile())
}

func runMediaCommand(ctx context.Context, rs io.ReadSeeker, cmd *exec.Cmd) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := rs.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("seek media input: %w", err)
	}
	var out, stderr bytes.Buffer
	// Let os/exec own stdin copying. An extra io.Pipe producer would survive
	// when the child stops reading after its first frame or fails to start.
	cmd.Stdin = rs
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	cmd.WaitDelay = time.Second
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("media command failed (%s): %w", stderr.String(), err)
	}
	return out.Bytes(), nil
}

func tryGetInputPeer(ctx *ext.Context, chatID int64) tg.InputPeerClass {
	peer := ctx.PeerStorage.GetInputPeerById(chatID)
	if peer != nil && !peer.Zero() {
		return peer
	}
	id := constant.TDLibPeerID(chatID)
	plain := id.ToPlain()
	var channel constant.TDLibPeerID
	channel.Channel(plain)
	peer = ctx.PeerStorage.GetInputPeerById(int64(channel))
	if peer != nil && !peer.Zero() {
		return peer
	}
	var chat constant.TDLibPeerID
	chat.Chat(plain)
	peer = ctx.PeerStorage.GetInputPeerById(int64(chat))
	if peer != nil && !peer.Zero() {
		return peer
	}
	var user constant.TDLibPeerID
	user.User(plain)
	peer = ctx.PeerStorage.GetInputPeerById(int64(user))
	return peer
}

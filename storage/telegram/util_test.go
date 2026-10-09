package telegram

import (
	"bytes"
	"context"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func makeTestVideo(t *testing.T) *os.File {
	t.Helper()
	for _, binary := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Fatalf("video integration tests require %s on PATH: %v", binary, err)
		}
	}
	path := filepath.Join(t.TempDir(), "sample.mp4")
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	// MPEG-4 uses a built-in encoder; faststart makes the sample readable from
	// the pipes used by the existing thumbnail and metadata implementations.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "color=c=black:s=160x90:r=10",
		"-t", "2", "-an", "-c:v", "mpeg4", "-pix_fmt", "yuv420p",
		"-movflags", "+faststart", "-threads", "1", path,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to generate test video: %v\n%s", err, output)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("failed to close test video: %v", err)
		}
	})
	return file
}

func TestExtractThumbFrame(t *testing.T) {
	thumb, err := extractThumbFrame(t.Context(), makeTestVideo(t))
	if err != nil {
		t.Fatalf("failed to extract thumb frame: %v", err)
	}
	image, err := jpeg.Decode(bytes.NewReader(thumb))
	if err != nil {
		t.Fatalf("thumbnail is not a valid JPEG: %v", err)
	}
	if bounds := image.Bounds(); bounds.Dx() != 160 || bounds.Dy() != 90 {
		t.Fatalf("unexpected thumbnail dimensions: %v", bounds)
	}
}

func TestGetVideoMetadata(t *testing.T) {
	meta, err := getVideoMetadata(t.Context(), makeTestVideo(t))
	if err != nil {
		t.Fatalf("failed to get video metadata: %v", err)
	}
	if meta.Duration != 2 || meta.Width != 160 || meta.Height != 90 {
		t.Fatalf("unexpected video metadata: %+v", meta)
	}
}

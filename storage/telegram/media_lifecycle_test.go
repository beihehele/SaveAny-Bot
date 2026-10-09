package telegram

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMediaHelpersReleaseEarlyExitInput(t *testing.T) {
	file := makeTestVideo(t)
	video, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	video = append(video, make([]byte, 16<<20)...)
	thumbnail, err := extractThumbFrame(t.Context(), bytes.NewReader(video))
	if err != nil || len(thumbnail) == 0 {
		t.Fatalf("thumbnail bytes=%d err=%v", len(thumbnail), err)
	}
	assertNoMediaPipeProducer(t)
}

func TestMediaHelpersReleaseStartFailureInput(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, helper := range []func(context.Context, io.ReadSeeker) error{
		func(ctx context.Context, r io.ReadSeeker) error { _, err := getVideoMetadata(ctx, r); return err },
		func(ctx context.Context, r io.ReadSeeker) error { _, err := extractThumbFrame(ctx, r); return err },
	} {
		if err := helper(t.Context(), bytes.NewReader(make([]byte, 1<<20))); err == nil {
			t.Fatal("missing executable succeeded")
		}
	}
	// Let any incorrectly detached producer reach its first blocked Write.
	runtime.Gosched()
	assertNoMediaPipeProducer(t)
}

func assertNoMediaPipeProducer(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	stack := string(buf[:runtime.Stack(buf, true)])
	for _, goroutine := range strings.Split(stack, "\n\n") {
		if strings.Contains(goroutine, "io.(*pipe).write") && strings.Contains(goroutine, "/storage/telegram.") {
			t.Fatalf("media helper left a pipe producer behind:\n%s", goroutine)
		}
	}
}

func TestMediaCommandHelperProcess(t *testing.T) {
	if os.Getenv("SAVEANY_TEST_MEDIA_CHILD") != "wait" {
		return
	}
	// The parent command must kill this real child on cancellation/deadline.
	io.Copy(io.Discard, os.Stdin)
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestMediaCommandCancellationAndDeadline(t *testing.T) {
	for _, mode := range []string{"cancel", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			limit := 2 * time.Second
			if mode == "cancel" {
				limit = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(t.Context(), limit)
			defer cancel()
			var reader io.ReadSeeker = bytes.NewReader([]byte("input"))
			want := context.DeadlineExceeded
			if mode == "cancel" {
				reader = &cancelMediaReader{Reader: bytes.NewReader([]byte("input")), cancel: cancel}
				want = context.Canceled
			}
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMediaCommandHelperProcess$")
			cmd.Env = append(os.Environ(), "SAVEANY_TEST_MEDIA_CHILD=wait")
			if _, err := runMediaCommand(ctx, reader, cmd); !errors.Is(err, want) {
				t.Fatalf("err=%v want=%v", err, want)
			}
			if cmd.ProcessState == nil {
				t.Fatal("media child was not waited for")
			}
		})
	}
}

type cancelMediaReader struct {
	*bytes.Reader
	cancel context.CancelFunc
}

func (r *cancelMediaReader) Read(p []byte) (int, error) {
	r.cancel()
	return r.Reader.Read(p)
}

// Hide bytes.Reader.WriteTo so os/exec must call the cancellation-aware Read.
func (r *cancelMediaReader) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, struct{ io.Reader }{r})
}

func TestMediaHelperCancelledContextDoesNotRead(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader := bytes.NewReader([]byte("untouched"))
	if _, err := extractThumbFrame(ctx, reader); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if reader.Len() != len("untouched") {
		t.Fatal("cancelled helper consumed input")
	}
}

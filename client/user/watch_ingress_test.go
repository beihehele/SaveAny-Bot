package user

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

func TestMediaIngressCapturesRoutesBeforeWaiting(t *testing.T) {
	const env = "SAVEANY_MEDIA_INGRESS_TEST_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestMediaIngressCapturesRoutesBeforeWaiting$", "-test.count=1")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated media ingress: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[[users]]\nid=42\n[[users]]\nid=43\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	ctx := &ext.Context{Context: t.Context()}
	owner, err := database.GetUserByChatID(ctx, 42)
	if err != nil {
		t.Fatal(err)
	}
	other, err := database.GetUserByChatID(ctx, 43)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []*database.User{owner, other} {
		if err := user.WatchChat(ctx, database.WatchChat{ChatID: 100, TargetID: 200, TargetTopicID: 2, Filter: "msgre:caption"}); err != nil {
			t.Fatal(err)
		}
	}
	msg := &tg.Message{ID: 7, Message: "caption"}
	file := tfile.NewTGFile(nil, nil, 1, "photo.jpg", tfile.WithMessage(msg)).(tfile.TGFileMessage)
	pending, err := captureMediaMessageEvent(ctx, 100, 7, file)
	if err != nil || len(pending.Routes) != 2 || pending.File != file || pending.Ctx != ctx {
		t.Fatalf("capture lost recipients, media or client context: %+v %v", pending, err)
	}
	old := owner.WatchChats[0]
	if err := owner.UnwatchByID(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := owner.WatchChat(ctx, database.WatchChat{ChatID: 100, TargetID: 200, TargetTopicID: 2}); err != nil {
		t.Fatal(err)
	}
	newRoute := owner.WatchChats[len(owner.WatchChats)-1]
	for _, route := range pending.Routes {
		if route.ID == newRoute.ID || route.TargetTopicID != 2 || route.Filter != "msgre:caption" {
			t.Fatal("pending event was rebound or route metadata changed")
		}
	}
	fresh, err := captureMediaMessageEvent(ctx, 100, 8, file)
	if err != nil || len(fresh.Routes) != 2 {
		t.Fatalf("fresh capture: %v %v", fresh, err)
	}
	found := false
	for _, route := range fresh.Routes {
		found = found || route.ID == newRoute.ID
	}
	if !found {
		t.Fatal("new event missed newly created route")
	}
	unwatched, err := captureMediaMessageEvent(ctx, 999, 9, file)
	if err != nil || len(unwatched.Routes) != 0 {
		t.Fatalf("unwatched source captured recipients: %+v %v", unwatched, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if event, err := captureMediaMessageEvent(&ext.Context{Context: cancelled}, 100, 7, file); !errors.Is(err, context.Canceled) || len(event.Routes) != 0 {
		t.Fatalf("cancelled lookup captured partial recipients: %+v %v", event, err)
	}
	// Cancelling one lookup must not damage the shared client's lifetime.
	if _, err := captureMediaMessageEvent(ctx, 100, 7, file); err != nil || ctx.Err() != nil {
		t.Fatalf("cancelled lookup damaged shared client lifetime: %v", err)
	}
}

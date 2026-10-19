package handlers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/core/tasks/batchtfile"
	coretfile "github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/rule"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type watchCollectionStorage struct {
	storage.Storage
	received int
}

func TestWatchCollectionFolderRuleCannotOverwriteMemberStorage(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	text := "[telegram.userbot]\nenable=true\n[[storages]]\nname='tg-collection-review'\ntype='telegram'\nenable=true\nchat_id=-1000000000222\n[[users]]\nid=42\nstorages=['tg-collection-review']\n"
	if err := os.WriteFile(file, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	for _, reverse := range []bool{false, true} {
		ctx, _ := watchAdmissionContext(t)
		files := []tfile.TGFileMessage{watchAdmissionFile(t, 1, 55, "one.mp4", "caption"), watchAdmissionFile(t, 2, 55, "two.mp4", "")}
		if reverse {
			files[0], files[1] = files[1], files[0]
		}
		user := &database.User{ChatID: 42, ApplyRule: true, Rules: []database.Rule{
			{Type: rule.FileNameRegex.String(), Data: "^one", StorageName: "tg-collection-review", DirPath: rule.RuleDirPathNewForAlbum},
		}}
		called := false
		processWatchMediaGroup(ctx, user, watchAdmissionStorage{}, "", files, func(context.Context, ...core.Executable) error { called = true; return nil })
		if called {
			t.Fatalf("album silently used its first member's storage (reverse=%v)", reverse)
		}
	}
}

func (*watchCollectionStorage) Name() string         { return "TG收藏" }
func (*watchCollectionStorage) CannotStream() string { return "messages" }
func (s *watchCollectionStorage) SaveMessages(_ context.Context, files []tfile.TGFileMessage) ([]string, error) {
	s.received = len(files)
	return nil, nil
}

func TestWatchCollectionSubmitsAlbumTogether(t *testing.T) {
	file := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(file, []byte("workers=1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	ctx, _ := watchAdmissionContext(t)
	store := &watchCollectionStorage{}
	files := []tfile.TGFileMessage{watchAdmissionFile(t, 1, 55, "one.mp4", "caption"), watchAdmissionFile(t, 2, 55, "two.mp4", "")}
	var accepted []core.Executable
	processWatchLocalAlbumTasks(ctx, &database.User{ChatID: 42}, store, "", files, func(_ context.Context, tasks ...core.Executable) error { accepted = tasks; return nil })
	if len(accepted) != 1 {
		t.Fatalf("watch split album into %d tasks", len(accepted))
	}
	batch, ok := accepted[0].(*batchtfile.Task)
	if !ok || batch.Count() != 2 {
		t.Fatalf("not a complete batch: %T", accepted[0])
	}
	if err := batch.Execute(ctx); err != nil || store.received != 2 {
		t.Fatalf("watch did not save full album: %v members=%d", err, store.received)
	}
	// A failed preparation cannot turn a whole-album collection into a partial send.
	called := false
	task := &coretfile.Task{ID: "one", Storage: store, File: files[0]}
	if n := submitWatchAlbum(ctx, 42, files[0].Message(), []core.Executable{task}, 1, func(context.Context, ...core.Executable) error { called = true; return nil }); n != 0 || called {
		t.Fatal("incomplete collection was admitted")
	}
}

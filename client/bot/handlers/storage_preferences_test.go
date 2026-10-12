package handlers

import (
	"errors"
	"fmt"
	"github.com/celestix/gotgproto/dispatcher"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/storage"
)

func TestStorageSwitchClearsPreviousDirectory(t *testing.T) {
	const env = "SAVEANY_REVIEW_STORAGE_DIR"
	dir := os.Getenv(env)
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestStorageSwitchClearsPreviousDirectory$", "-test.v")
		cmd.Env = append(os.Environ(), env+"="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated review: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(dir)
	path := filepath.Join(dir, "config.toml")
	body := "[[storages]]\nname='old'\ntype='local'\nenable=true\nbase_path='old-root'\n[[storages]]\nname='new'\ntype='local'\nenable=true\nbase_path='new-root'\n[[users]]\nid=42\nstorages=['old','new']\n[[users]]\nid=43\nstorages=['old','new']\n"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	cache.Init()
	storage.LoadStorages(t.Context())
	u, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateDirForUser(t.Context(), u.ID, "old", "old-subdir"); err != nil {
		t.Fatal(err)
	}
	dirs, err := database.GetUserDirs(t.Context(), u.ID)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("dirs=%v err=%v", dirs, err)
	}
	if err := database.UpdateUserPreferences(t.Context(), 42, map[string]any{"default_storage": "old", "default_dir": dirs[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err := cache.Set("review-switch", tcbdata.SetDefaultStorage{StorageName: "new"}); err != nil {
		t.Fatal(err)
	}
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "test")
	ctx := &ext.Context{Context: t.Context(), Raw: tg.NewClient(&acknowledgedActionInvoker{}), PeerStorage: peers}
	update := &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{QueryID: 123, UserID: 42, MsgID: 7, Data: []byte(fmt.Sprintf("%s review-switch", tcbdata.TypeSetDefault))}}
	if err := handleSetDefaultCallback(ctx, update); !errors.Is(err, dispatcher.EndGroups) {
		t.Fatalf("handler: %v", err)
	}
	got, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	_, _, savePath, err := resolveWatchLocalStorage(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultStorage != "new" || got.DefaultDir != 0 || savePath != "" {
		t.Fatalf("storage switch retained old directory: storage=%s directory=%d watch_save_path=%q", got.DefaultStorage, got.DefaultDir, savePath)
	}

	foreign, err := database.GetUserByChatID(t.Context(), 43)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateDirForUser(t.Context(), foreign.ID, "new", "foreign"); err != nil {
		t.Fatal(err)
	}
	foreignDirs, err := database.GetUserDirs(t.Context(), foreign.ID)
	if err != nil || len(foreignDirs) != 1 {
		t.Fatalf("foreign fixture: %v %v", foreignDirs, err)
	}
	for _, invalid := range []tcbdata.SetDefaultStorage{
		{StorageName: "new", DirID: dirs[0].ID},
		{StorageName: "new", DirID: foreignDirs[0].ID},
		{StorageName: "new", DirID: 999},
	} {
		if err := cache.Set("review-invalid", invalid); err != nil {
			t.Fatal(err)
		}
		invoker := &acknowledgedActionInvoker{}
		ctx.Raw = tg.NewClient(invoker)
		update.CallbackQuery.Data = []byte(tcbdata.TypeSetDefault + " review-invalid")
		if err := handleSetDefaultCallback(ctx, update); !errors.Is(err, dispatcher.EndGroups) {
			t.Fatal(err)
		}
		got, err = database.GetUserByChatID(t.Context(), 42)
		if err != nil || got.DefaultStorage != "new" || got.DefaultDir != 0 || len(invoker.answers) != 1 || !invoker.answers[0].Alert {
			t.Fatalf("invalid directory accepted: %+v user=%+v err=%v", invalid, got, err)
		}
	}
}

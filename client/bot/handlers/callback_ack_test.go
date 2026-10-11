package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	peerstore "github.com/celestix/gotgproto/storage"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/cache"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/tcbdata"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type acknowledgedActionInvoker struct {
	answers       []*tg.MessagesSetBotCallbackAnswerRequest
	edits         int
	deliveryError bool
}

func (i *acknowledgedActionInvoker) Invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	switch req := in.(type) {
	case *tg.MessagesSetBotCallbackAnswerRequest:
		deadline, ok := ctx.Deadline()
		if !req.Alert && (!ok || time.Until(deadline) > callbackAckTimeout+time.Second) {
			return fmt.Errorf("callback acknowledgement has no bounded lifetime")
		}
		i.answers = append(i.answers, req)
		if i.deliveryError {
			return errors.New("callback response unavailable")
		}
		out.(*tg.BoolBox).Bool = &tg.BoolTrue{}
		return nil
	case *tg.MessagesEditMessageRequest:
		if len(i.answers) != 1 {
			return fmt.Errorf("edit before callback acknowledgement")
		}
		i.edits++
		// Editing is best effort; an acknowledgement failure must not undo an action.
		return errors.New("edit unavailable")
	default:
		return fmt.Errorf("unexpected RPC %T", in)
	}
}

func TestCallbackActionsAcknowledgeWithoutBreakingTasksOrAlerts(t *testing.T) {
	// Process isolation keeps singleton cache, queue, storage and database state
	// out of the other handler tests; the parent owns temporary-file cleanup.
	dir := os.Getenv("SAVEANY_CALLBACK_TEST_DIR")
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), exe, "-test.run=^TestCallbackActionsAcknowledgeWithoutBreakingTasksOrAlerts$", "-test.v")
		cmd.Env = append(os.Environ(), "SAVEANY_CALLBACK_TEST_DIR="+t.TempDir())
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("callback regression: %v\n%s", err, output)
		}
		return
	}
	local := filepath.ToSlash(filepath.Join(dir, "downloads"))
	body := fmt.Sprintf("[db]\npath = %q\n[[storages]]\nname = 'callback-local'\ntype = 'local'\nenable = true\nbase_path = %q\n[[users]]\nid = 42\nstorages = ['callback-local']\n", filepath.ToSlash(filepath.Join(dir, "database.db")), local)
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	cache.Init()
	core.Prepare()
	peers := peerstore.NewPeerStorage(nil, true)
	peers.AddPeer(42, 1, peerstore.TypeUser, "test")
	run := func(name, data string, handler func(*ext.Context, *ext.Update) error, alert, deliveryError bool, tasks int) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			invoker := &acknowledgedActionInvoker{deliveryError: deliveryError}
			ctx := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker), PeerStorage: peers}
			update := &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{QueryID: 123, UserID: 42, MsgID: 7, Data: []byte(data)}}
			before := core.GetLength(ctx)
			err := handler(ctx, update)
			if ctx.Err() != nil {
				t.Fatal("callback acknowledgement cancelled the action context")
			}
			if !errors.Is(err, dispatcher.EndGroups) {
				t.Fatalf("handler = %v", err)
			}
			if len(invoker.answers) != 1 || invoker.answers[0].QueryID != 123 || invoker.answers[0].Alert != alert {
				t.Fatalf("answers = %+v", invoker.answers)
			}
			if alert && invoker.edits != 0 || !alert && invoker.edits != 1 {
				t.Fatalf("edits = %d", invoker.edits)
			}
			if got := core.GetLength(ctx) - before; got != tasks {
				t.Fatalf("added %d tasks, want %d", got, tasks)
			}
		})
	}
	set := func(key string, data any) {
		t.Helper()
		if err := cache.Set(key, data); err != nil {
			t.Fatal(err)
		}
	}
	run("filename menu", "config fnamest", handleConfigCallback, false, false, 0)
	run("filename setting", "config fnamest default", handleConfigCallback, false, false, 0)
	run("conflict menu", "config conflictst", handleConfigCallback, false, false, 0)
	run("conflict setting", "config conflictst rename", handleConfigCallback, false, false, 0)
	set("default-local", tcbdata.SetDefaultStorage{StorageName: "callback-local"})
	run("default storage", "setdefault default-local", handleSetDefaultCallback, false, false, 0)
	user, err := database.GetUserByChatID(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	if user.DefaultStorage != "callback-local" {
		t.Fatalf("default storage = %q", user.DefaultStorage)
	}
	files := []tfile.TGFileMessage{watchAdmissionFile(t, 101, 99, "a.bin", ""), watchAdmissionFile(t, 102, 99, "b.bin", "")}
	for _, batch := range []bool{false, true} {
		key := fmt.Sprintf("files-%v", batch)
		set(key, tcbdata.Add{TaskType: tasktype.TaskTypeTgfiles, SelectedStorName: "callback-local", Files: files, AsBatch: batch, SettedDir: true})
		run(key, "add "+key, handleAddCallback, false, false, 1)
	}
	set("delivery-failure", tcbdata.Add{TaskType: tasktype.TaskTypeTgfiles, SelectedStorName: "callback-local", Files: files, SettedDir: true})
	run("ack failure still adds once", "add delivery-failure", handleAddCallback, false, true, 1)
	if err := os.WriteFile(filepath.Join(local, "a.bin"), []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	set("conflict", tcbdata.Add{TaskType: tasktype.TaskTypeTgfiles, SelectedStorName: "callback-local", Files: files, SettedDir: true, ConflictStrategy: tcbdata.ConflictStrategyAsk})
	run("file conflict prompt", "add conflict", handleAddCallback, false, false, 0)
	if err := database.CreateDirForUser(t.Context(), user.ID, "callback-local", "folder"); err != nil {
		t.Fatal(err)
	}
	set("dir-prompt", tcbdata.Add{TaskType: tasktype.TaskTypeTgfiles, SelectedStorName: "callback-local", Files: files})
	run("save directory prompt", "add dir-prompt", handleAddCallback, false, false, 0)
	run("default directory prompt", "setdefault default-local", handleSetDefaultCallback, false, false, 0)
	dirs, err := database.GetUserDirs(t.Context(), user.ID)
	if err != nil || len(dirs) != 1 {
		t.Fatalf("dirs %v %v", dirs, err)
	}
	set("default-dir", tcbdata.SetDefaultStorage{StorageName: "callback-local", DirID: dirs[0].ID})
	run("default directory setting", "setdefault default-dir", handleSetDefaultCallback, false, false, 0)
	for _, handler := range []struct {
		name, prefix string
		fn           func(*ext.Context, *ext.Update) error
	}{{"save", "add", handleAddCallback}, {"default", "setdefault", handleSetDefaultCallback}} {
		run(handler.name+" expired", handler.prefix+" missing", handler.fn, true, false, 0)
	}
	set("denied-storage", tcbdata.Add{TaskType: tasktype.TaskTypeTgfiles, SelectedStorName: "not-owned", Files: files})
	run("save denied storage", "add denied-storage", handleAddCallback, true, false, 0)
	set("denied-default", tcbdata.SetDefaultStorage{StorageName: "not-owned"})
	run("default denied storage", "setdefault denied-default", handleSetDefaultCallback, true, false, 0)
	run("invalid config", "config unknown", handleConfigCallback, true, false, 0)
}

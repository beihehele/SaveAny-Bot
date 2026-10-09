package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/celestix/gotgproto/ext"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage"
)

type telegramContextTask struct {
	client *ext.Context
}

func (*telegramContextTask) Type() tasktype.TaskType { return tasktype.TaskTypeTransfer }
func (*telegramContextTask) Title() string           { return "client context probe" }
func (*telegramContextTask) TaskID() string          { return "telegram-context-probe" }
func (t *telegramContextTask) Execute(ctx context.Context) error {
	if tgutil.ExtFromContext(ctx) != t.client || taskresult.PolicyFromContext(ctx) != taskresult.Strict {
		return errors.New("Telegram client or request policy was lost")
	}
	return ctx.Err()
}
func (*telegramContextTask) ResultSummary() taskresult.Summary {
	return taskresult.Summary{Total: 1, Succeeded: 1}
}

// Isolate process-wide storage and queue registries, including worker shutdown.
// No client connects to Telegram; the worker verifies the injected dependency.
func TestTelegramTaskContextEndToEnd(t *testing.T) {
	const helperEnv = "SAVEANY_API_TELEGRAM_CONTEXT_TEST_PROCESS"
	if os.Getenv(helperEnv) != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestTelegramTaskContextEndToEnd$", "-test.count=1")
		command.Env = append(os.Environ(), helperEnv+"=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("isolated Telegram context integration: %v\n%s", err, output)
		}
		return
	}
	serviceCtx, cancelService := context.WithCancel(t.Context())
	defer cancelService()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	text := fmt.Sprintf("workers=1\n[[storages]]\nname='context-tg'\ntype='telegram'\nenable=true\nchat_id=123\n[[storages]]\nname='context-local'\ntype='local'\nenable=true\nbase_path=%q\n", filepath.ToSlash(t.TempDir()))
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(serviceCtx, configPath); err != nil {
		t.Fatal(err)
	}
	storage.LoadStorages(serviceCtx)
	core.Prepare()
	client := &ext.Context{Context: serviceCtx}
	task := &telegramContextTask{client: client}
	factory := NewTaskFactory(serviceCtx)
	clientFailure := errors.New("client not ready")
	factory.clientContext = func() (*ext.Context, error) { return nil, clientFailure }
	if err := factory.registerAndEnqueueTask(task, task.Type(), "context-tg", "", ""); !errors.Is(err, clientFailure) {
		t.Fatalf("missing client: %v", err)
	}
	if _, exists := GetTask(task.TaskID()); exists {
		t.Fatal("unexecutable task was registered")
	}
	factory.clientContext = func() (*ext.Context, error) { return nil, nil }
	if _, err := factory.contextForStorage("context-tg"); err == nil {
		t.Fatal("nil client was accepted")
	}
	factory.clientContext = func() (*ext.Context, error) { panic("unnecessary client resolution") }
	if ctx, err := factory.contextForStorage("context-local"); err != nil || ctx != serviceCtx {
		t.Fatalf("local task unexpectedly needs a client: %v", err)
	}
	provided := *factory
	provided.ctx = tgutil.ExtWithContext(serviceCtx, client)
	if ctx, err := provided.contextForStorage("context-tg"); err != nil || tgutil.ExtFromContext(ctx) != client {
		t.Fatalf("provided client was lost: %v", err)
	}
	factory.clientContext = func() (*ext.Context, error) { return client, nil }
	requestCtx, cancelRequest := context.WithCancel(serviceCtx)
	defer cancelRequest()
	factory.prepareCtx = requestCtx
	factory.resultPolicy = taskresult.Strict
	if err := factory.registerAndEnqueueTask(task, task.Type(), "context-tg", "", ""); err != nil {
		t.Fatal(err)
	}
	defer DeleteTask(task.TaskID())
	cancelRequest()
	done := core.Run(serviceCtx)
	defer func() {
		cancelService()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, exists := GetTask(task.TaskID())
		if !exists {
			t.Fatal("task record was lost")
		}
		snapshot := info.responseSnapshot()
		if snapshot.status.terminal() {
			if snapshot.status != TaskStatusCompleted || snapshot.resultPolicy != taskresult.Strict || snapshot.resultSummary == nil || snapshot.resultSummary.Succeeded != 1 {
				t.Fatalf("context, sink or policy failed: %+v", snapshot)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("worker did not finish")
		}
	}
	ctx, err := factory.contextForStorage("context-tg")
	if err != nil {
		t.Fatal(err)
	}
	cancelService()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatal("Telegram context detached the task from service shutdown")
	}
}

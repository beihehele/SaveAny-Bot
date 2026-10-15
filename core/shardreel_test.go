package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
	"github.com/krau/SaveAny-Bot/storage/shardreel"
)

type stagedFixture struct{ store *shardreel.Store }

func (s stagedFixture) TaskID() string          { return "deferred-fixture" }
func (s stagedFixture) Type() tasktype.TaskType { return tasktype.TaskTypeTgfiles }
func (s stagedFixture) Title() string           { return "staged fixture" }
func (s stagedFixture) UsesShardReel() bool     { return true }
func (s stagedFixture) Execute(ctx context.Context) error {
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(4))
	return s.store.Save(ctx, strings.NewReader("file"), "/fixture.bin")
}

func TestFinishShardReelHandoffRetriesTransientPersistenceError(t *testing.T) {
	calls := 0
	finish := func(context.Context, string, taskresult.Summary, error) (bool, error) {
		calls++
		if calls == 1 {
			return false, errors.New("temporary database error")
		}
		return true, nil
	}
	handedOff, err := finishShardReelHandoff(t.Context(), t.Context(), "task", taskresult.Summary{}, nil, finish, time.Millisecond)
	if err != nil || !handedOff || calls != 2 {
		t.Fatalf("handoff=(%v, %v), calls=%d", handedOff, err, calls)
	}
}

type handoffReadFailureFixture struct {
	stagedFixture
	db   *gorm.DB
	stop context.CancelFunc
}

func (s handoffReadFailureFixture) Execute(ctx context.Context) error {
	result := s.stagedFixture.Execute(ctx)
	if !errors.Is(result, storagetypes.ErrSaveQueued) {
		return result
	}
	if err := s.db.Callback().Query().Before("gorm:query").Register("test:handoff-read-failure", func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "ShardReelTask" {
			tx.AddError(errors.New("injected handoff read failure"))
		}
	}); err != nil {
		return err
	}
	s.stop()
	return result
}

func TestShardReelHandoffReadFailureAtShutdownPreservesPendingUpload(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "premature-terminal.txt")
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "upload-complete")
	t.Setenv("SAVEANY_TEST_HOOK_UPLOAD_MARKER", marker)
	hook := strconv.Quote(hookTestCommand())
	configuration := fmt.Sprintf("workers=1\n[db]\npath=%q\n[hook.exec]\ntimeout='5s'\ntask_success=%s\ntask_fail=%s\ntask_cancel=%s\n[[storages]]\nname='drive'\ntype='shardreel'\nenable=true\nurl='http://127.0.0.1:9'\napi_key='isolated-storage-integration-key-2026'\nspool_path=%q\n", filepath.ToSlash(filepath.Join(root, "db.sqlite")), hook, hook, hook, filepath.ToSlash(filepath.Join(root, "spool")))
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	db := database.ShardReelDB(t.Context())
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	if handedOff, err := shardreel.FinishDownload(t.Context(), "not-reserved", taskresult.Summary{}, errors.New("download failed before reservation")); err != nil || handedOff {
		t.Fatalf("an unreserved task must retain ordinary completion: handoff=%v err=%v", handedOff, err)
	}
	store := new(shardreel.Store)
	if err := store.Init(t.Context(), config.C().Storages[0]); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	task := handoffReadFailureFixture{stagedFixture: stagedFixture{store}, db: db, stop: cancel}
	var doneEvents int
	taskCtx := taskevent.WithSink(t.Context(), taskevent.SinkFunc(func(event taskevent.Event) {
		if event.Phase == taskevent.PhaseDone {
			doneEvents++
		}
	}))
	qe := queue.NewTaskQueue[Executable]()
	if err := qe.Add(queue.NewTask(taskCtx, task.TaskID(), task.Title(), Executable(task))); err != nil {
		t.Fatal(err)
	}
	qe.Close()
	worker(ctx, qe)
	if err := db.Callback().Query().Remove("test:handoff-read-failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending upload ran a terminal hook: %v", err)
	}
	if doneEvents != 0 {
		t.Fatalf("pending upload emitted %d premature terminal events", doneEvents)
	}
	var group database.ShardReelTask
	if err := db.First(&group, "id = ?", task.TaskID()).Error; err != nil {
		t.Fatal(err)
	}
	if group.DownloadDone || group.Notified || group.HookDone {
		t.Fatalf("failed handoff was recorded as complete: %+v", group)
	}
	if err := store.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&group, "id = ?", task.TaskID()).Error; err != nil {
		t.Fatal(err)
	}
	var file database.ShardReelFile
	if err := db.First(&file, "task_id = ?", task.TaskID()).Error; err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(filepath.Join(root, "spool", file.ID, "payload"))
	if err != nil || string(payload) != "file" || !group.DownloadDone || file.Status != "queued" || file.LocalClean {
		t.Fatalf("upload cannot resume: task=%+v file=%+v payload=%q err=%v", group, file, payload, err)
	}
}

func TestShardReelHandoffDoesNotRunSuccessHookBeforeRemoteCompletion(t *testing.T) {
	root := t.TempDir()
	marker := filepath.Join(root, "hook.txt")
	t.Setenv("SAVEANY_TEST_HOOK_MODE", "upload-complete")
	t.Setenv("SAVEANY_TEST_HOOK_UPLOAD_MARKER", marker)
	configuration := fmt.Sprintf("workers=1\n[db]\npath=%q\n[hook.exec]\ntimeout='5s'\ntask_success=%s\n[[storages]]\nname='drive'\ntype='shardreel'\nenable=true\nurl='http://127.0.0.1:9'\napi_key='isolated-storage-integration-key-2026'\nspool_path=%q\n", filepath.ToSlash(filepath.Join(root, "db.sqlite")), strconv.Quote(hookTestCommand()), filepath.ToSlash(filepath.Join(root, "spool")))
	configPath := filepath.Join(root, "config.toml")
	if err := os.WriteFile(configPath, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), configPath); err != nil {
		t.Fatal(err)
	}
	database.Init(t.Context())
	store := new(shardreel.Store)
	if err := store.Init(t.Context(), config.C().Storages[0].(*storcfg.ShardReelStorageConfig)); err != nil {
		t.Fatal(err)
	}
	qe := queue.NewTaskQueue[Executable]()
	if err := qe.Add(queue.NewTask(t.Context(), "deferred-fixture", "fixture", Executable(stagedFixture{store}))); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	go func() { defer close(done); worker(ctx, qe) }()
	defer func() { cancel(); qe.CloseAndCancel(); <-done }()
	var group database.ShardReelTask
	db := database.ShardReelDB(t.Context())
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := db.First(&group, "id = ?", "deferred-fixture").Error; err == nil && group.DownloadDone {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !group.DownloadDone {
		t.Fatal("download was not handed off")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("success hook ran early: %v", err)
	}
	group.Status = "completed"
	if err := CompleteShardReel(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&group, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := CompleteShardReel(t.Context(), group); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "completed\n" {
		t.Fatalf("hook result=%q err=%v", data, err)
	}
	stale := group
	stale.HookDone = false
	if err := db.Model(&group).Updates(map[string]any{"hook_done": false, "result_version": group.ResultVersion + 1}).Error; err != nil {
		t.Fatal(err)
	}
	if err := CompleteShardReel(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&group, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(marker)
	if err != nil || string(data) != "completed\n" || group.HookDone {
		t.Fatalf("stale completion affected a new attempt: hook=%q done=%v err=%v", data, group.HookDone, err)
	}
}

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/rs/xid"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/core"
	coretfile "github.com/krau/SaveAny-Bot/core/tasks/tfile"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/fnamest"
	"github.com/krau/SaveAny-Bot/pkg/queue"
	"github.com/krau/SaveAny-Bot/pkg/rule"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type watchAdmissionStorage struct{ storage.Storage }

func (watchAdmissionStorage) Name() string { return "chosen" }

func watchAdmissionFile(t *testing.T, id int, groupID int64, name, caption string) tfile.TGFileMessage {
	t.Helper()
	msg := &tg.Message{ID: id, PeerID: &tg.PeerChannel{ChannelID: 123}, Message: caption,
		Media: &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(id), MimeType: "video/mp4", Size: 1,
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: name}}}}}
	msg.SetGroupedID(groupID)
	file, err := tfile.FromMediaMessage(msg.Media, nil, msg)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func watchAdmissionContext(t *testing.T) (*ext.Context, *bytes.Buffer) {
	t.Helper()
	output := &bytes.Buffer{}
	logger := log.NewWithOptions(output, log.Options{Formatter: log.JSONFormatter})
	return &ext.Context{Context: log.WithContext(t.Context(), logger)}, output
}

// Use the real queue for the submission boundary while keeping it independent
// of the application's Prepare/Run singleton and Telegram network clients.
func watchAdmissionQueue(t *testing.T, ctx *ext.Context, duplicate bool) (*queue.TaskQueue[core.Executable], watchAlbumEnqueue, *[][]core.Executable) {
	t.Helper()
	qe := queue.NewTaskQueue[core.Executable]()
	t.Cleanup(qe.CloseAndCancel)
	groups := &[][]core.Executable{}
	enqueue := func(parent context.Context, tasks ...core.Executable) error {
		if tgutil.ExtFromContext(parent) != ctx || parent.Err() != nil {
			t.Fatal("submission lost the Telegram client or caller lifetime")
		}
		*groups = append(*groups, append([]core.Executable(nil), tasks...))
		if duplicate {
			if len(tasks) < 2 {
				t.Fatal("album was submitted one member at a time")
			}
			existing := queue.NewTask(parent, tasks[1].TaskID(), "existing", tasks[1])
			if err := qe.Add(existing); err != nil {
				t.Fatal(err)
			}
		}
		wrappers := make([]*queue.Task[core.Executable], 0, len(tasks))
		for _, task := range tasks {
			wrappers = append(wrappers, queue.NewTask(parent, task.TaskID(), task.Title(), task))
		}
		if err := qe.AddBatch(wrappers...); err != nil {
			for _, wrapper := range wrappers {
				wrapper.Cancel()
			}
			return err
		}
		return nil
	}
	return qe, enqueue, groups
}

func watchAdmissionLog(t *testing.T, output *bytes.Buffer, message string) map[string]any {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		var fields map[string]any
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			t.Fatal(err)
		}
		if fields["msg"] == message {
			return fields
		}
	}
	t.Fatalf("missing %q in %s", message, output.String())
	return nil
}

func TestWatchAlbumAdmissionPreservesCaptionNamingAndRules(t *testing.T) {
	for _, strategy := range []string{"", fnamest.Message.String()} {
		t.Run(strategy, func(t *testing.T) {
			ctx, output := watchAdmissionContext(t)
			qe, enqueue, groups := watchAdmissionQueue(t, ctx, false)
			files := []tfile.TGFileMessage{
				watchAdmissionFile(t, 101, 99, "first.mp4", "shared caption"),
				watchAdmissionFile(t, 102, 99, "second.mp4", ""),
				watchAdmissionFile(t, 103, 99, "third.mp4", ""),
			}
			user := &database.User{ChatID: 42, FilenameStrategy: strategy, ApplyRule: true, Rules: []database.Rule{
				{Type: rule.FileNameRegex.String(), Data: "_102", StorageName: rule.RuleStorNameChosen, DirPath: "earlier"},
				{Type: rule.FileNameRegex.String(), Data: "_102", StorageName: rule.RuleStorNameChosen, DirPath: "last-match"},
			}}
			stor := watchAdmissionStorage{}
			processWatchLocalAlbumTasks(ctx, user, stor, "base", files, enqueue)
			if len(*groups) != 1 || len((*groups)[0]) != 3 || qe.Length() != 3 {
				t.Fatalf("expected one complete album submission: %v", *groups)
			}
			firstName := "first.mp4"
			if strategy == fnamest.Message.String() {
				firstName = "shared_caption.mp4"
			}
			wantPaths := []string{"base/" + firstName, "last-match/shared_caption_102.mp4", "base/shared_caption_103.mp4"}
			qe.Close()
			for index, want := range wantPaths {
				wrapped, err := qe.Get()
				if err != nil {
					t.Fatal(err)
				}
				task := wrapped.Data.(*coretfile.Task)
				if task.Path != want || task.Storage != stor || task.File != files[index] ||
					tgutil.ExtFromContext(task.Ctx) != ctx || task.Ctx.Err() != nil {
					t.Fatalf("member %d lost naming, rules, storage, order, or context: %+v", index, task)
				}
				qe.Done(wrapped.ID)
			}
			if files[1].Message().Message != "" || files[2].Message().Message != "" || ctx.Err() != nil {
				t.Fatal("submission mutated source captions or cancelled the caller")
			}
			if got := watchAdmissionLog(t, output, "Watch album tasks admitted")["admitted_tasks"]; got != float64(3) {
				t.Fatalf("admitted_tasks = %v", got)
			}
		})
	}
}

func TestWatchAlbumAdmissionRejectsWithoutPrefix(t *testing.T) {
	for _, folder := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "folder"}[folder], func(t *testing.T) {
			ctx, output := watchAdmissionContext(t)
			qe, enqueue, groups := watchAdmissionQueue(t, ctx, true)
			user := &database.User{ChatID: 42}
			if folder {
				user.ApplyRule = true
				user.Rules = []database.Rule{{Type: rule.IsAlbum.String(), Data: "true", DirPath: rule.RuleDirPathNewForAlbum}}
			}
			files := []tfile.TGFileMessage{watchAdmissionFile(t, 1, 99, "one.mp4", "one"), watchAdmissionFile(t, 2, 99, "two.mp4", "two")}
			processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "base", files, enqueue)
			if len(*groups) != 1 || qe.Length() != 1 || ctx.Err() != nil {
				t.Fatal("rejection published a prefix or affected the caller")
			}
			qe.Close()
			existing, err := qe.Get()
			if err != nil || existing.Title != "existing" || existing.Context().Err() != nil {
				t.Fatal("rejection changed previously admitted work")
			}
			qe.Done(existing.ID)
			fields := watchAdmissionLog(t, output, "Watch album admission rejected")
			if fields["grouped_id"] != float64(99) || fields["rejected_tasks"] != float64(2) || fields["user_id"] != float64(42) || fields["source_peer"] == nil {
				t.Fatalf("missing rejection diagnostic: %v", fields)
			}
			if strings.Contains(output.String(), "Watch album tasks admitted") {
				t.Fatal("rejected album logged as admitted")
			}
		})
	}
}

func TestWatchAlbumAdmissionKeepsFolderGroupsAndDirectories(t *testing.T) {
	ctx, _ := watchAdmissionContext(t)
	_, enqueue, groups := watchAdmissionQueue(t, ctx, false)
	user := &database.User{ChatID: 42, ApplyRule: true, Rules: []database.Rule{
		{Type: rule.IsAlbum.String(), Data: "true", DirPath: rule.RuleDirPathNewForAlbum},
		{Type: rule.FileNameRegex.String(), Data: "^second", DirPath: "override"},
	}}
	files := []tfile.TGFileMessage{
		watchAdmissionFile(t, 1, 99, "first.mp4", "first"), watchAdmissionFile(t, 2, 99, "second.mp4", "second"),
		watchAdmissionFile(t, 3, 100, "other.mp4", "other"), watchAdmissionFile(t, 4, 100, "last.mp4", "last"),
		watchAdmissionFile(t, 5, 101, "singleton.mp4", "singleton"),
	}
	processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "base", files, enqueue)
	if len(*groups) != 2 {
		t.Fatal("independent albums were combined or singleton selection changed")
	}
	want := map[int64][]string{99: {"base/first/first.mp4", "override/first/second.mp4"}, 100: {"base/other/other.mp4", "base/other/last.mp4"}}
	for _, group := range *groups {
		if len(group) != 2 {
			t.Fatal("folder album split")
		}
		id := group[0].(*coretfile.Task).File.(tfile.TGFileMessage).Message().GroupedID
		paths, ok := want[id]
		if !ok {
			t.Fatalf("unexpected or repeated group %d", id)
		}
		for index, task := range group {
			if task.(*coretfile.Task).Path != paths[index] {
				t.Fatalf("directory or member order changed: %s", task.Title())
			}
		}
		delete(want, id)
	}
}

func TestWatchAlbumAdmissionReportsPreparationFailures(t *testing.T) {
	for _, mode := range []string{"ordinary", "folder", "all_failed"} {
		t.Run(mode, func(t *testing.T) {
			ctx, output := watchAdmissionContext(t)
			qe, enqueue, groups := watchAdmissionQueue(t, ctx, false)
			user := &database.User{ChatID: 42, ApplyRule: true}
			if mode == "folder" {
				user.Rules = append(user.Rules, database.Rule{Type: rule.IsAlbum.String(), Data: "true", DirPath: rule.RuleDirPathNewForAlbum})
			}
			user.Rules = append(user.Rules, database.Rule{Type: rule.FileNameRegex.String(), Data: "^bad", StorageName: "missing-" + xid.New().String()})
			files := []tfile.TGFileMessage{watchAdmissionFile(t, 1, 99, "good.mp4", "good"), watchAdmissionFile(t, 2, 99, "bad.mp4", "bad"), watchAdmissionFile(t, 3, 99, "also-good.mp4", "good")}
			if mode == "all_failed" {
				files = files[1:2]
			}
			processWatchLocalAlbumTasks(ctx, user, watchAdmissionStorage{}, "base", files, enqueue)
			if mode == "all_failed" {
				if len(*groups) != 0 || qe.Length() != 0 {
					t.Fatal("empty prepared group submitted")
				}
				watchAdmissionLog(t, output, "Watch album has no prepared tasks")
				return
			}
			if len(*groups) != 1 || len((*groups)[0]) != 2 || qe.Length() != 2 {
				t.Fatal("preparation failure changed selection or split submission")
			}
			fields := watchAdmissionLog(t, output, "Watch album tasks admitted")
			if fields["admitted_tasks"] != float64(2) || fields["prepare_failed"] != float64(1) {
				t.Fatalf("inaccurate preparation diagnostic: %v", fields)
			}
		})
	}
}

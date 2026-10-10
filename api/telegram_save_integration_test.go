package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/core"
	"github.com/krau/SaveAny-Bot/pkg/enums/tasktype"
	filemodel "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage"
)

type saveContextKey struct{}

type saveTestClient struct {
	downloader.Client
	mode    string
	started chan struct{}
}

func (c saveTestClient) UploadGetFile(ctx context.Context, req *tg.UploadGetFileRequest) (tg.UploadFileClass, error) {
	if ctx.Value(saveContextKey{}) != "service lifetime" {
		return nil, errors.New("service context values were lost")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.mode == "failure" {
		return nil, errors.New("synthetic Telegram download failure")
	}
	if c.mode == "cancel" {
		select {
		case c.started <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	data := []byte("data")
	if req.Offset >= int64(len(data)) {
		return &tg.UploadFile{}, nil
	}
	return &tg.UploadFile{Bytes: data[req.Offset:]}, nil
}

// Only the Telegram RPC adapter is mocked: the factory, queue, download task,
// local backend and API progress sink are the real production implementations.
func TestTelegramSaveEndToEnd(t *testing.T) {
	const modeEnv = "SAVEANY_LOCAL_SAVE_TEST_MODE"
	mode := os.Getenv(modeEnv)
	if mode == "" {
		for _, scenario := range []string{"single-buffered", "album-buffered", "single-stream", "album-stream", "failure", "cancel"} {
			t.Run(scenario, func(t *testing.T) {
				binary, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				command := exec.CommandContext(ctx, binary, "-test.run=^TestTelegramSaveEndToEnd$", "-test.count=1")
				command.Env = append(os.Environ(), modeEnv+"="+scenario)
				if output, err := command.CombinedOutput(); err != nil {
					t.Fatalf("%v\n%s", err, output)
				}
			})
		}
		return
	}
	root := t.TempDir()
	configPath := filepath.Join(root, "config.toml")
	text := fmt.Sprintf("workers=1\nthreads=1\nretry=1\nstream=%t\n[temp]\nbase_path=%q\n[[storages]]\nname='archive'\ntype='local'\nenable=true\nbase_path=%q\n", strings.Contains(mode, "stream"), filepath.ToSlash(filepath.Join(root, "cache")), filepath.ToSlash(filepath.Join(root, "saved")))
	if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	serviceCtx, stop := context.WithCancel(context.WithValue(t.Context(), saveContextKey{}, "service lifetime"))
	defer stop()
	if err := config.Init(serviceCtx, configPath); err != nil {
		t.Fatal(err)
	}
	storage.LoadStorages(serviceCtx)
	core.Prepare()
	requestCtx, cancelRequest := context.WithCancel(serviceCtx)
	defer cancelRequest()
	count := 1
	if strings.HasPrefix(mode, "album") {
		count = 2
	}
	started := make(chan struct{}, 1)
	factory := NewTaskFactoryWithExtractor(serviceCtx, func(ctx context.Context, links []string) ([]filemodel.TGFileMessage, error) {
		if ctx != requestCtx || len(links) != 1 {
			return nil, errors.New("request preprocessing context was lost")
		}
		var files []filemodel.TGFileMessage
		for i := 1; i <= count; i++ {
			media := &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(i), Size: 4, Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: fmt.Sprintf("asset-%d.bin", i)}}}}
			file, err := filemodel.FromMediaMessage(media, saveTestClient{mode: mode, started: started}, &tg.Message{ID: i, GroupedID: 99})
			if err != nil {
				return nil, err
			}
			files = append(files, file)
		}
		return files, nil
	})
	response, err := factory.CreateTaskWithContext(requestCtx, &CreateTaskRequest{Type: tasktype.TaskTypeTgfiles, Storage: "archive", Path: "albums", Params: json.RawMessage(`{"message_links":["https://t.me/c/123/1"]}`)})
	if err != nil {
		t.Fatal(err)
	}
	defer DeleteTask(response.TaskID)
	// An accepted save must survive the HTTP request ending.
	cancelRequest()
	done := core.Run(serviceCtx)
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	if mode == "cancel" {
		select {
		case <-started:
		case <-time.After(3 * time.Second):
			t.Fatal("download never started")
		}
		if err := core.CancelTask(serviceCtx, response.TaskID); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		info, exists := GetTask(response.TaskID)
		if !exists {
			t.Fatal("task record disappeared")
		}
		snapshot := info.responseSnapshot()
		if snapshot.status.terminal() {
			want := TaskStatusCompleted
			if mode == "failure" {
				want = TaskStatusFailed
			}
			if mode == "cancel" {
				want = TaskStatusCancelled
			}
			if snapshot.status != want {
				t.Fatalf("status=%s want=%s error=%s", snapshot.status, want, snapshot.err)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("save did not finish")
		}
	}
	for i := 1; i <= count; i++ {
		path := filepath.Join(root, "saved", "albums", fmt.Sprintf("asset-%d.bin", i))
		data, err := os.ReadFile(path)
		if mode == "cancel" || mode == "failure" {
			if !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed save published a file: %v", err)
			}
		} else if err != nil || string(data) != "data" {
			t.Fatalf("saved file=%q err=%v", data, err)
		}
	}
}

func TestRemovedTaskTypesCannotResolveOrEnqueue(t *testing.T) {
	factory := NewTaskFactoryWithExtractor(t.Context(), func(context.Context, []string) ([]filemodel.TGFileMessage, error) {
		t.Fatal("removed task reached Telegram source adapter")
		return nil, nil
	})
	before := len(GetAllTasks())
	for _, typ := range []string{"directlinks", "aria2", "ytdlp", "parseditem", "tphpics", "transfer", "copy"} {
		_, err := factory.CreateTask(&CreateTaskRequest{Type: tasktype.TaskType(typ), Storage: "unavailable", Params: json.RawMessage(`{}`)})
		if err == nil || !strings.Contains(err.Error(), "unsupported task type") {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if len(GetAllTasks()) != before {
		t.Fatal("removed tasks were registered")
	}
}

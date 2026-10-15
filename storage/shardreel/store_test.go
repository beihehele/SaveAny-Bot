package shardreel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/krau/SaveAny-Bot/pkg/tfile"
)

type fakeRemote struct {
	mu        sync.Mutex
	state     string
	initCalls int
	size      int64
	hash      string
	data      []byte
	loseInit  bool
}

func (f *fakeRemote) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer isolated-storage-integration-key-2026" {
		w.WriteHeader(401)
		return
	}
	reply := func(body any) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(body); err != nil {
			panic(err)
		}
	}
	if r.URL.Path == "/v1/storage/info" {
		reply(map[string]any{"root": "Telegram", "worker_enabled": true})
		return
	}
	if r.Method == "POST" && r.URL.Path == "/v1/storage/uploads" {
		var body struct {
			Size   int64  `json:"size"`
			SHA256 string `json:"sha256"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(400)
			return
		}
		f.initCalls++
		f.size, f.hash, f.state = body.Size, body.SHA256, "uploading"
		if f.loseInit {
			f.loseInit = false
			connection, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				panic(err)
			}
			if err := connection.Close(); err != nil {
				panic(err)
			}
			return
		}
	}
	if f.state == "" {
		w.WriteHeader(404)
		return
	}
	if r.Method == "PUT" {
		data, err := io.ReadAll(r.Body)
		if err != nil {
			panic(err)
		}
		f.data = append(f.data, data...)
		reply(map[string]any{"received": len(f.data)})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/complete") {
		f.state = "processing"
	}
	if r.Method == "DELETE" {
		f.state = "cancelled"
	}
	if strings.HasSuffix(r.URL.Path, "/ack") {
		reply(map[string]any{"ok": true})
		return
	}
	reply(map[string]any{"status": f.state, "size": f.size, "sha256": f.hash, "received_bytes": len(f.data)})
}

func fixture(t *testing.T, endpoint string) *Store {
	t.Helper()
	root := t.TempDir()
	db, err := gorm.Open(database.GetDialect(filepath.Join(root, "queue.db")), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := db.AutoMigrate(&database.ShardReelTask{}, &database.ShardReelFile{}); err != nil {
		t.Fatal(err)
	}
	spool := filepath.Join(root, "spool")
	if err := os.Mkdir(spool, 0700); err != nil {
		t.Fatal(err)
	}
	return &Store{db: db, client: &http.Client{Timeout: time.Second}, cfg: storcfg.ShardReelStorageConfig{BaseConfig: storcfg.BaseConfig{Name: "drive", Type: "shardreel", Enable: true}, URL: endpoint, APIKey: "isolated-storage-integration-key-2026", SpoolPath: spool, MaxPendingBytes: 20 << 30}}
}

func stage(t *testing.T, s *Store, data []byte, parent string) database.ShardReelFile {
	t.Helper()
	ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: parent, FileID: "file", Title: "fixture", ExpectedFiles: 1})
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(len(data)))
	if err := s.Save(ctx, bytes.NewReader(data), "/photos/中文.bin"); !errors.Is(err, storagetypes.ErrSaveQueued) {
		t.Fatal(err)
	}
	var row database.ShardReelFile
	if err := s.db.First(&row, "task_id = ?", parent).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func rowAt(t *testing.T, s *Store, id string) database.ShardReelFile {
	t.Helper()
	var row database.ShardReelFile
	if err := s.db.First(&row, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func due(t *testing.T, s *Store, id string) {
	t.Helper()
	if err := s.update(t.Context(), id, map[string]any{"next_attempt_at": time.Time{}}); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadedSizeCannotReplaceTelegramExpectedSize(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: "size-check", FileID: "file"})
	if err := s.Reserve(ctx, 10, "/expected.bin"); err != nil {
		t.Fatal(err)
	}
	ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(5))
	if err := s.Save(ctx, strings.NewReader("short"), "/expected.bin"); err == nil || errors.Is(err, storagetypes.ErrSaveQueued) {
		t.Fatalf("truncated download was admitted: %v", err)
	}
	row := rowAt(t, s, "size-check_file")
	if row.Size != 10 || row.SHA256 != "" || row.Status != "preparing" {
		t.Fatalf("expected size was overwritten: %+v", row)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, row.ID, "payload")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected committed payload: %v", err)
	}
}

func TestCaptionPhotoAlbumStagesToOneRemoteDirectory(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	var files []tfile.TGFileMessage
	for index := 1; index <= 4; index++ {
		media := &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: int64(index), Sizes: []tg.PhotoSizeClass{&tg.PhotoSizeProgressive{Type: "y", Sizes: []int{2, 4}}}}}
		msg := &tg.Message{ID: index, PeerID: &tg.PeerChannel{ChannelID: 123}}
		msg.SetGroupedID(int64(10 + (index-1)/2))
		if index%2 == 1 {
			msg.Message = "旅行 / 照片"
		}
		file, err := tfile.FromMediaMessage(media, nil, msg)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}
	dirs := tfile.AlbumDirectories(files)
	for _, file := range files {
		ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: "photos", FileID: fmt.Sprint(file.Message().ID), ExpectedFiles: len(files)})
		if err := s.Reserve(ctx, file.Size(), path.Join("/base", dirs[file.Message()], file.Name())); err != nil {
			t.Fatal(err)
		}
		ctx = context.WithValue(ctx, ctxkey.ContentLength, int64(4))
		if err := s.Save(ctx, strings.NewReader("data"), path.Join("/base", dirs[file.Message()], file.Name())); !errors.Is(err, storagetypes.ErrSaveQueued) {
			t.Fatal(err)
		}
	}
	var rows []database.ShardReelFile
	if err := s.db.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 {
		t.Fatal("photo album lost members")
	}
	for _, row := range rows {
		if path.Dir(row.Path) != "/base/旅行_照片" || row.Size != 4 || row.Status != "queued" {
			t.Fatalf("incorrect durable photo destination: %+v", row)
		}
	}
}

func TestSuccessRequiresRemoteIngestAndThenDeletesPayload(t *testing.T) {
	remote := &fakeRemote{}
	server := httptest.NewServer(http.HandlerFunc(remote.handler))
	defer server.Close()
	s := fixture(t, server.URL)
	payload := bytes.Repeat([]byte("original"), 600000)
	row := stage(t, s, payload, "success")
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "processing" || current.LocalClean {
		t.Fatalf("premature completion: %+v", current)
	}
	remote.mu.Lock()
	remote.state = "succeeded"
	remote.mu.Unlock()
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	current := rowAt(t, s, row.ID)
	if current.Status != "succeeded" || !current.LocalClean {
		t.Fatalf("result: %+v", current)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, row.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("payload retained: %v", err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if !bytes.Equal(remote.data, payload) || remote.initCalls != 1 {
		t.Fatal("uploaded bytes or identity changed")
	}
}

func TestLostInitializationReplyReusesRemoteRequestAfterRestart(t *testing.T) {
	remote := &fakeRemote{loseInit: true}
	server := httptest.NewServer(http.HandlerFunc(remote.handler))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("resume"), "resume")
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "retry_wait" || !current.RemoteStarted {
		t.Fatalf("result: %+v", current)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if remote.initCalls != 1 || string(remote.data) != "resume" {
		t.Fatal("restart created a second upload")
	}
}

func TestRecoveryResumesCompletePayloadAndCleansInterruptedDownload(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	complete := stage(t, s, []byte("complete"), "complete")
	if err := s.update(t.Context(), complete.ID, map[string]any{"status": "preparing", "sha256": ""}); err != nil {
		t.Fatal(err)
	}
	ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: "partial", FileID: "file"})
	if err := s.Reserve(ctx, 10, "/partial.bin"); err != nil {
		t.Fatal(err)
	}
	partialDir := filepath.Join(s.cfg.SpoolPath, "partial_file")
	if err := os.Mkdir(partialDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partialDir, "payload.part"), []byte("part"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	if row := rowAt(t, s, complete.ID); row.Status != "queued" || row.SHA256 != complete.SHA256 || row.LocalClean {
		t.Fatalf("complete payload was not recovered: %+v", row)
	}
	if row := rowAt(t, s, "partial_file"); row.Status != "failed" || !row.LocalClean {
		t.Fatalf("partial download was incorrectly recovered: %+v", row)
	}
}

func TestCancellationDeletesStagedFileOnlyAfterRemoteCancellation(t *testing.T) {
	remote := &fakeRemote{}
	server := httptest.NewServer(http.HandlerFunc(remote.handler))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("cancel"), "cancel")
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if err := s.db.Model(&database.ShardReelTask{}).Where("id = ?", row.TaskID).Update("cancel_requested", true).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.cfg.SpoolPath, row.ID, "payload")); err != nil {
		t.Fatal("payload removed before confirmation", err)
	}
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if result := rowAt(t, s, row.ID); result.Status != "cancelled" || !result.LocalClean {
		t.Fatalf("cancel did not finish: %+v", result)
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if remote.state != "cancelled" || remote.initCalls != 1 {
		t.Fatalf("remote cancel was not confirmed: %s", remote.state)
	}
}

func TestCleanupFailureDoesNotReuploadOrChangeSuccessfulStatus(t *testing.T) {
	remote := &fakeRemote{}
	server := httptest.NewServer(http.HandlerFunc(remote.handler))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("saved"), "cleanup")
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	extra := filepath.Join(s.cfg.SpoolPath, row.ID, "unrelated")
	if err := os.WriteFile(extra, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.state = "succeeded"
	remote.mu.Unlock()
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	current := rowAt(t, s, row.ID)
	if current.Status != "succeeded" || current.CleanupError == "" || current.LocalClean {
		t.Fatalf("result: %+v", current)
	}
	if err := os.Remove(extra); err != nil {
		t.Fatal(err)
	}
	if err := s.maintenance(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !rowAt(t, s, row.ID).LocalClean {
		t.Fatal("cleanup did not retry")
	}
	remote.mu.Lock()
	defer remote.mu.Unlock()
	if remote.initCalls != 1 {
		t.Fatal("cleanup reuploaded the file")
	}
}

func TestChecksumMismatchKeepsLocalFileForInspection(t *testing.T) {
	remote := &fakeRemote{}
	server := httptest.NewServer(http.HandlerFunc(remote.handler))
	defer server.Close()
	s := fixture(t, server.URL)
	row := stage(t, s, []byte("preserve"), "integrity")
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	remote.mu.Lock()
	remote.state = "succeeded"
	remote.hash = strings.Repeat("0", 64)
	remote.mu.Unlock()
	due(t, s, row.ID)
	if err := s.step(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if current := rowAt(t, s, row.ID); current.Status != "failed" || current.LocalClean {
		t.Fatalf("result: %+v", current)
	}
	if data, err := os.ReadFile(filepath.Join(s.cfg.SpoolPath, row.ID, "payload")); err != nil || string(data) != "preserve" {
		t.Fatal("unconfirmed file deleted")
	}
}

func TestHistoryCleanupRetainsFailedAndUncleanTasks(t *testing.T) {
	s := fixture(t, "http://unused.invalid")
	old := time.Now().Add(-8 * 24 * time.Hour)
	for _, state := range []string{"completed", "failed", "cancelled"} {
		group := database.ShardReelTask{ID: state, Status: state, Notified: true, UpdatedAt: old, CreatedAt: old}
		if err := s.db.Create(&group).Error; err != nil {
			t.Fatal(err)
		}
		file := database.ShardReelFile{ID: state, TaskID: state, LocalClean: state != "cancelled", RemoteAcked: true}
		if err := s.db.Create(&file).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := pruneHistory(t.Context(), s.db); err != nil {
		t.Fatal(err)
	}
	var groups []database.ShardReelTask
	if err := s.db.Find(&groups).Error; err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 {
		t.Fatalf("retained: %+v", groups)
	}
	for _, group := range groups {
		if group.ID == "completed" {
			t.Fatal("expired clean history retained")
		}
	}
}

// TestLiveStorageAPI runs only against a separately started isolated ShardReel.
func TestLiveStorageAPI(t *testing.T) {
	endpoint := os.Getenv("SAVEANY_SHARDREEL_TEST_URL")
	if endpoint == "" {
		t.Skip("set SAVEANY_SHARDREEL_TEST_URL for isolated cross-project HTTP verification")
	}
	s := fixture(t, endpoint)
	s.client.Timeout = 30 * time.Second
	s.cfg.APIKey = os.Getenv("SAVEANY_SHARDREEL_TEST_KEY")
	caption := "联调 / 相册_" + fmt.Sprint(time.Now().UnixNano())
	var directory string
	for index, payload := range [][]byte{bytes.Repeat([]byte("cross-project"), 400000), bytes.Repeat([]byte("large-file-test!"), 1600000)} {
		parent := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
		media := &tg.MessageMediaDocument{Document: &tg.Document{ID: int64(index + 1), Size: int64(len(payload)),
			Attributes: []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "中文.bin"}}}}
		msg := &tg.Message{ID: index + 1, PeerID: &tg.PeerChannel{ChannelID: 123}, Message: caption}
		msg.SetGroupedID(int64(index + 1))
		file, err := tfile.FromMediaMessage(media, nil, msg)
		if err != nil {
			t.Fatal(err)
		}
		directory = path.Join("/albums", tfile.AlbumDirectories([]tfile.TGFileMessage{file})[msg])
		ctx := storagetypes.WithSaveRequest(t.Context(), storagetypes.SaveRequest{TaskID: parent, FileID: "file", ExpectedFiles: 1})
		ctx = context.WithValue(ctx, ctxkey.ContentLength, file.Size())
		if err := s.Save(ctx, bytes.NewReader(payload), path.Join(directory, file.Name())); !errors.Is(err, storagetypes.ErrSaveQueued) {
			t.Fatal(err)
		}
		row := rowAt(t, s, parent+"_file")
		deadline := time.Now().Add(time.Minute)
		for time.Now().Before(deadline) {
			due(t, s, row.ID)
			if err := s.step(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			row = rowAt(t, s, row.ID)
			if row.Status == "succeeded" {
				break
			}
			if row.Status == "failed" {
				t.Fatal(row.Error)
			}
			time.Sleep(100 * time.Millisecond)
		}
		if row.Status != "succeeded" || !row.LocalClean {
			t.Fatalf("incomplete: %+v", row)
		}
		if path.Dir(row.SavedPath) != directory {
			t.Fatalf("same caption used a different folder: %s", row.SavedPath)
		}
		reader, _, err := s.OpenFile(t.Context(), row.SavedPath)
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != row.SHA256 {
			t.Fatal("downloaded hash differs")
		}
	}
	files, err := s.ListFiles(t.Context(), directory)
	if err != nil || len(files) != 2 || files[0].Name == files[1].Name {
		t.Fatalf("album folder did not retain both conflicting files: files=%+v error=%v", files, err)
	}
}

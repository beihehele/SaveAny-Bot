// Package shardreel stages complete Telegram files and uploads them independently.
package shardreel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/rs/xid"
	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/common/utils/fsutil"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/database"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
)

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)

// Store owns an outbox under one configured directory and remote endpoint.
type Store struct {
	cfg    storcfg.ShardReelStorageConfig
	db     *gorm.DB
	client *http.Client
	mu     sync.Mutex
}

func (s *Store) Init(ctx context.Context, cfg storcfg.StorageConfig) error {
	value, ok := cfg.(*storcfg.ShardReelStorageConfig)
	if !ok {
		return errors.New("invalid ShardReel configuration")
	}
	s.cfg = *value
	if err := s.cfg.Validate(); err != nil {
		return err
	}
	s.cfg.URL = strings.TrimRight(s.cfg.URL, "/")
	absolute, err := filepath.Abs(s.cfg.SpoolPath)
	if err != nil {
		return err
	}
	s.cfg.SpoolPath = absolute
	if err := os.MkdirAll(absolute, 0700); err != nil {
		return err
	}
	s.db = database.ShardReelDB(ctx)
	if s.db == nil {
		return errors.New("database is not ready")
	}
	s.client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return nil
}

func (s *Store) Type() storenum.StorageType { return storenum.Shardreel }
func (s *Store) Name() string               { return s.cfg.Name }
func (s *Store) CannotStream() string {
	return "persistent file staging is required for retry and restart recovery"
}

func validPath(value string) (string, error) {
	if strings.Contains(value, "\\") || len(value) > 2048 {
		return "", errors.New("invalid remote path")
	}
	parts := strings.Split(strings.Trim(value, "/"), "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part || strings.ContainsAny(part, "\x00\r\n") {
			return "", errors.New("invalid remote path")
		}
	}
	return "/" + strings.Join(parts, "/"), nil
}

func request(ctx context.Context) storagetypes.SaveRequest {
	meta := storagetypes.SaveRequestFrom(ctx)
	if meta.TaskID == "" {
		meta.TaskID = xid.New().String()
	}
	if meta.FileID == "" {
		meta.FileID = meta.TaskID
	}
	return meta
}

// Reserve admits a known-size file before its Telegram download consumes space.
func (s *Store) Reserve(ctx context.Context, size int64, target string) error {
	meta := request(ctx)
	id := meta.TaskID + "_" + meta.FileID
	if !safeID.MatchString(id) || size < 0 {
		return errors.New("invalid upload identity or size")
	}
	if size == 0 {
		return errors.New("ShardReel does not support empty files; select local storage")
	}
	target, err := validPath(target)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.WithContext(ctx).Transaction(func(db *gorm.DB) error {
		var previous database.ShardReelFile
		if err := db.First(&previous, "id = ?", id).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		var used int64
		if err := db.Model(&database.ShardReelFile{}).Select("COALESCE(SUM(size), 0)").Where("storage = ? AND local_clean = ?", s.Name(), false).Scan(&used).Error; err != nil {
			return err
		}
		if size > s.cfg.MaxPendingBytes-used {
			return errors.New("ShardReel outbox is full; finish uploads or discard failed files before downloading more")
		}
		var reserved int64
		if err := db.Model(&database.ShardReelFile{}).Select("COALESCE(SUM(size), 0)").Where("storage = ? AND status = ?", s.Name(), "preparing").Scan(&reserved).Error; err != nil {
			return err
		}
		available, err := fsutil.AvailableDiskBytes(s.cfg.SpoolPath)
		if err != nil {
			return fmt.Errorf("inspect outbox disk space: %w", err)
		}
		if uint64(size) > available/2 || uint64(reserved) > available/2-uint64(size) {
			return errors.New("not enough disk space for download and staging; uploads are retained")
		}
		group := database.ShardReelTask{ID: meta.TaskID, Title: meta.Title, Source: meta.Source, ChatID: meta.ChatID, MessageID: meta.MessageID, Status: "running", ExpectedFiles: max(1, meta.ExpectedFiles)}
		if err := db.Where("id = ?", group.ID).FirstOrCreate(&group).Error; err != nil {
			return err
		}
		row := database.ShardReelFile{ID: id, ElementID: meta.FileID, TaskID: meta.TaskID, Storage: s.Name(),
			Endpoint: s.cfg.URL, Spool: s.cfg.SpoolPath, Path: target, Size: size, Status: "preparing", Conflict: "keep_both"}
		overwrite, _ := ctx.Value(ctxkey.OverwriteExisting).(bool)
		if overwrite {
			row.Conflict = "replace"
		}
		return db.Create(&row).Error
	})
}

// Save stages a complete file. ErrSaveQueued is an intermediate result, not success.
func (s *Store) Save(ctx context.Context, reader io.Reader, target string) error {
	meta := request(ctx)
	ctx = storagetypes.WithSaveRequest(ctx, meta)
	size, ok := ctx.Value(ctxkey.ContentLength).(int64)
	if !ok {
		return errors.New("ShardReel saves require a known file size")
	}
	if err := s.Reserve(ctx, size, target); err != nil {
		return err
	}
	id := meta.TaskID + "_" + meta.FileID
	var row database.ShardReelFile
	if err := s.db.WithContext(ctx).First(&row, "id = ?", id).Error; err != nil {
		return err
	}
	if size != row.Size {
		return errors.New("Telegram download size differs from its expected size")
	}
	if row.Status != "preparing" {
		if row.Status == "failed" {
			return errors.New(row.Error)
		}
		return storagetypes.ErrSaveQueued
	}
	target, err := validPath(target)
	if err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&row).Update("path", target).Error; err != nil {
		return err
	}
	root, err := os.OpenRoot(s.cfg.SpoolPath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.Mkdir(id, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	payload := id + "/payload"
	// A committed payload can survive a crash immediately before its DB update.
	if _, err := root.Stat(payload); err == nil {
		return s.recoverPayload(ctx, &row, root)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := root.OpenFile(id+"/payload.part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	hash := sha256.New()
	copied, copyErr := io.Copy(io.MultiWriter(file, hash), &contextReader{ctx: ctx, reader: io.LimitReader(reader, size+1)})
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(copyErr, syncErr, closeErr, ctx.Err()); err != nil {
		return err
	}
	if copied != size {
		return fmt.Errorf("file size mismatch: expected %d, got %d", size, copied)
	}
	if err := root.Rename(id+"/payload.part", payload); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&row).Updates(map[string]any{"status": "queued", "sha256": hex.EncodeToString(hash.Sum(nil)), "error": ""}).Error; err != nil {
		return err
	}
	return storagetypes.ErrSaveQueued
}

func (s *Store) recoverPayload(ctx context.Context, row *database.ShardReelFile, root *os.Root) error {
	file, err := root.Open(row.ID + "/payload")
	if err != nil {
		return err
	}
	hash := sha256.New()
	size, copyErr := io.Copy(hash, &contextReader{ctx, file})
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	checksum := hex.EncodeToString(hash.Sum(nil))
	if size != row.Size || row.SHA256 != "" && row.SHA256 != checksum {
		return errors.New("staged file integrity check failed")
	}
	if err := s.db.WithContext(ctx).Model(row).Updates(map[string]any{"status": "queued", "sha256": checksum}).Error; err != nil {
		return err
	}
	return storagetypes.ErrSaveQueued
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func (s *Store) Exists(ctx context.Context, target string) (bool, error) {
	if _, err := validPath(target); err != nil {
		return false, err
	}
	var response struct {
		Name string `json:"name"`
	}
	err := s.call(ctx, "GET", "/stat?path="+query(target), nil, nil, &response)
	var remote *httpError
	if errors.As(err, &remote) && remote.status == 404 && remote.code == "PATH_NOT_FOUND" {
		return false, nil
	}
	return err == nil, err
}

// ListFiles reads the remote directory; staged payloads are never browseable.
func (s *Store) ListFiles(ctx context.Context, dir string) ([]storagetypes.FileInfo, error) {
	result := []storagetypes.FileInfo{}
	for offset := 0; ; {
		var response struct {
			Files []struct {
				Name    string    `json:"name"`
				IsDir   bool      `json:"is_dir"`
				Size    int64     `json:"size"`
				ModTime time.Time `json:"mod_time"`
			} `json:"files"`
			Total int `json:"total"`
		}
		if err := s.call(ctx, "GET", fmt.Sprintf("/list?path=%s&offset=%d", query(dir), offset), nil, nil, &response); err != nil {
			return nil, err
		}
		for _, file := range response.Files {
			result = append(result, storagetypes.FileInfo{Name: file.Name, Path: path.Join(dir, file.Name), IsDir: file.IsDir, Size: file.Size, ModTime: file.ModTime})
		}
		offset += len(response.Files)
		if offset >= response.Total || len(response.Files) == 0 {
			return result, nil
		}
	}
}

func (s *Store) OpenFile(ctx context.Context, target string) (io.ReadCloser, int64, error) {
	if _, err := validPath(target); err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", s.cfg.URL+"/v1/storage/download?path="+query(target), nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	client := *s.client
	client.Timeout = 0
	response, err := client.Do(req)
	if err != nil {
		return nil, 0, errors.New("ShardReel download connection failed")
	}
	if response.StatusCode != 200 {
		response.Body.Close()
		return nil, 0, fmt.Errorf("ShardReel download returned HTTP %d", response.StatusCode)
	}
	return response.Body, response.ContentLength, nil
}

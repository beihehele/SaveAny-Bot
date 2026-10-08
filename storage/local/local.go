package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"
	config "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
	"github.com/krau/SaveAny-Bot/pkg/storagetypes"
	"github.com/rs/xid"
)

type Local struct {
	config    config.LocalStorageConfig
	logger    *log.Logger
	publishMu *sync.Mutex
}

var publishLocks sync.Map

func (l *Local) Init(ctx context.Context, cfg config.StorageConfig) error {
	localConfig, ok := cfg.(*config.LocalStorageConfig)
	if !ok {
		return fmt.Errorf("failed to cast local config")
	}
	if err := localConfig.Validate(); err != nil {
		return err
	}
	l.config = *localConfig
	basePath, err := filepath.Abs(localConfig.BasePath)
	if err != nil {
		return fmt.Errorf("resolve local storage directory: %w", err)
	}
	l.config.BasePath = basePath
	err = os.MkdirAll(basePath, os.ModePerm)
	if err != nil {
		return fmt.Errorf("failed to create local storage directory: %w", err)
	}
	lockPath, err := filepath.EvalSymlinks(basePath)
	if err != nil {
		return fmt.Errorf("resolve local storage directory: %w", err)
	}
	if runtime.GOOS == "windows" {
		lockPath = strings.ToLower(lockPath)
	}
	lock, _ := publishLocks.LoadOrStore(lockPath, &sync.Mutex{})
	l.publishMu = lock.(*sync.Mutex)
	l.logger = log.FromContext(ctx).WithPrefix(fmt.Sprintf("local[%s]", l.config.Name))
	return nil
}

func (l *Local) Type() storenum.StorageType {
	return storenum.Local
}

func (l *Local) Name() string {
	return l.config.Name
}

func (l *Local) JoinStoragePath(path string) string {
	return filepath.Join(l.config.BasePath, path)
}

func (l *Local) Save(ctx context.Context, r io.Reader, storagePath string) error {
	storagePath, err := localPath(storagePath)
	if err != nil {
		return err
	}
	if storagePath == "." {
		return fmt.Errorf("local file path is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(l.config.BasePath)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(storagePath), os.ModePerm); err != nil {
		return fmt.Errorf("create storage directory: %w", err)
	}
	overwrite, _ := ctx.Value(ctxkey.OverwriteExisting).(bool)
	mode := os.FileMode(0666)
	retainMode := false
	if overwrite {
		if info, err := root.Lstat(storagePath); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("overwrite target is not a regular file: %s", storagePath)
			}
			mode = info.Mode().Perm()
			retainMode = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	// Stage in the destination directory so publication stays on one filesystem.
	tempPath := filepath.Join(filepath.Dir(storagePath), ".saveany-"+xid.New().String()+".tmp")
	file, err := root.OpenFile(tempPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create temporary storage file: %w", err)
	}
	defer func() {
		if err := root.Remove(tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			l.logger.Warn("Failed to remove temporary storage file", "path", tempPath, "error", err)
		}
	}()
	if retainMode {
		if err := file.Chmod(mode); err != nil {
			return fmt.Errorf("preserve storage file permissions: %w", errors.Join(err, file.Close()))
		}
	}
	_, copyErr := io.Copy(file, contextReader{ctx: ctx, reader: r})
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr, ctx.Err()); err != nil {
		return fmt.Errorf("write storage file: %w", err)
	}
	if overwrite {
		// Do not change the meaning of a configured file by replacing a symlink.
		if info, err := root.Lstat(storagePath); err == nil {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("overwrite target is not a regular file: %s", storagePath)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := renameFile(ctx, root, tempPath, storagePath); err != nil {
			return fmt.Errorf("commit storage file: %w", err)
		}
		return nil
	}
	return publishFile(ctx, root, tempPath, storagePath, l.publishMu)
}

type filePublisher interface {
	Link(string, string) error
	Lstat(string) (os.FileInfo, error)
	Rename(string, string) error
}

func publishFile(ctx context.Context, root filePublisher, tempPath, storagePath string, mu *sync.Mutex) error {
	mu.Lock()
	defer mu.Unlock()
	ext := filepath.Ext(storagePath)
	base := strings.TrimSuffix(storagePath, ext)
	for i := 0; ; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidate := storagePath
		if i > 0 {
			candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
		}
		if err := root.Link(tempPath, candidate); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			// FAT and some mounted filesystems do not support hard links. Keep a
			// same-process commit lock, check the target, and publish by rename.
			// External writers must use separate directories on these filesystems.
			if _, statErr := root.Lstat(candidate); statErr == nil {
				continue
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return fmt.Errorf("inspect storage target: %w", statErr)
			}
			if err := renameFile(ctx, root, tempPath, candidate); err != nil {
				return fmt.Errorf("commit storage file: %w", err)
			}
		}
		return nil
	}
}

func renameFile(ctx context.Context, root interface{ Rename(string, string) error }, source, target string) error {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := root.Rename(source, target)
		if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) || attempt == 20 {
			return err
		}
		// Windows readers can briefly hold a file without delete sharing. Retry
		// replacement without removing the old target or bypassing os.Root.
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (l *Local) Exists(ctx context.Context, storagePath string) bool {
	storagePath, err := localPath(storagePath)
	if err != nil || ctx.Err() != nil {
		return false
	}
	root, err := os.OpenRoot(l.config.BasePath)
	if err != nil {
		return false
	}
	defer root.Close()
	_, err = root.Stat(storagePath)
	return err == nil
}

// ListFiles implements StorageListable interface
func (l *Local) ListFiles(ctx context.Context, dirPath string) ([]storagetypes.FileInfo, error) {
	localDir, err := localPath(dirPath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(l.config.BasePath)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	dir, err := root.Open(localDir)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return nil, fmt.Errorf("failed to read directory %s: %w", dirPath, err)
	}

	files := make([]storagetypes.FileInfo, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if stagingName(entry.Name()) {
			continue
		}
		info, err := root.Stat(filepath.Join(localDir, entry.Name()))
		if err != nil {
			l.logger.Warnf("Failed to get file info for %s: %v", entry.Name(), err)
			continue
		}

		filePath := filepath.Join(dirPath, entry.Name())
		files = append(files, storagetypes.FileInfo{
			Name:    entry.Name(),
			Path:    filePath,
			Size:    info.Size(),
			IsDir:   info.IsDir(),
			ModTime: info.ModTime(),
		})
	}

	return files, nil
}

func stagingName(name string) bool {
	if !strings.HasPrefix(name, ".saveany-") || !strings.HasSuffix(name, ".tmp") {
		return false
	}
	id := strings.TrimSuffix(strings.TrimPrefix(name, ".saveany-"), ".tmp")
	_, err := xid.FromString(id)
	return err == nil
}

// OpenFile implements StorageReadable interface
func (l *Local) OpenFile(ctx context.Context, filePath string) (io.ReadCloser, int64, error) {
	filePath, err := localPath(filePath)
	if err != nil {
		return nil, 0, err
	}
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	root, err := os.OpenRoot(l.config.BasePath)
	if err != nil {
		return nil, 0, err
	}
	defer root.Close()
	file, err := root.Open(filePath)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to open file %s: %w", filePath, err)
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, fmt.Errorf("failed to stat file %s: %w", filePath, err)
	}
	if !stat.Mode().IsRegular() {
		file.Close()
		return nil, 0, fmt.Errorf("not a regular storage file: %s", filePath)
	}

	return file, stat.Size(), nil
}

// localPath retains storage-root paths such as /photos while rejecting native
// absolute paths and parent traversal. os.Root enforces containment during IO.
func localPath(p string) (string, error) {
	p = strings.ReplaceAll(p, "\\", "/")
	if strings.HasPrefix(p, "//") || strings.Contains(p, ":") {
		return "", fmt.Errorf("invalid local storage path: %s", p)
	}
	p = filepath.Clean(filepath.FromSlash(strings.TrimLeft(p, "/")))
	if !filepath.IsLocal(p) {
		return "", fmt.Errorf("local storage path escapes its root: %s", p)
	}
	return p, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

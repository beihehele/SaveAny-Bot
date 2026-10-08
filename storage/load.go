package storage

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/charmbracelet/log"
	"golang.org/x/sync/singleflight"

	"github.com/krau/SaveAny-Bot/config"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
)

var (
	storageMu      sync.RWMutex
	storages       = make(map[string]Storage)
	initFlight     = new(singleflight.Group)
	userStoragesMu sync.RWMutex
	userStorages   = make(map[int64][]Storage)
)

// GetStorage returns an initialized storage without creating one on demand.
func GetStorage(name string) (Storage, bool) {
	storageMu.RLock()
	defer storageMu.RUnlock()
	stor, ok := storages[name]
	return stor, ok
}

// AllStorages returns a snapshot of initialized storages.
func AllStorages() map[string]Storage {
	storageMu.RLock()
	defer storageMu.RUnlock()
	return maps.Clone(storages)
}

// GetStorageByName returns storage by name from cache or creates new one
// It should NOT be used to get storage for user, use GetStorageByUserIDAndName instead
func GetStorageByName(ctx context.Context, name string) (Storage, error) {
	if name == "" {
		return nil, ErrStorageNameEmpty
	}

	storage, ok := GetStorage(name)
	if ok {
		return storage, nil
	}
	cfg := config.C().GetStorageByName(name)
	if cfg == nil {
		return nil, fmt.Errorf("未找到存储 %s", name)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resultCh := initFlight.DoChan(name, func() (any, error) {
		if existing, ok := GetStorage(name); ok {
			return existing, nil
		}
		stor, err := NewStorage(ctx, cfg)
		if err != nil {
			return nil, err
		}
		storageMu.Lock()
		storages[name] = stor
		storageMu.Unlock()
		return stor, nil
	})
	// Each caller can stop waiting without canceling another caller's
	// initialization. The initializer still uses its original context.
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case result := <-resultCh:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(Storage), nil
	}
}

// 检查 user 是否可用指定的 storage, 若不可用则返回未找到错误
func GetStorageByUserIDAndName(ctx context.Context, chatID int64, name string) (Storage, error) {
	if name == "" {
		return nil, ErrStorageNameEmpty
	}

	if !config.C().HasStorage(chatID, name) {
		return nil, fmt.Errorf("no storage %s for user %d", name, chatID)
	}

	return GetStorageByName(ctx, name)
}

func GetUserStorages(ctx context.Context, chatID int64) []Storage {
	if chatID <= 0 {
		return nil
	}
	userStoragesMu.RLock()
	cached, ok := userStorages[chatID]
	if ok {
		cached = slices.Clone(cached)
	}
	userStoragesMu.RUnlock()
	if ok {
		return cached
	}
	names := config.C().GetStorageNamesByUserID(chatID)
	if len(names) == 0 {
		return nil
	}
	var available []Storage
	complete := true
	for _, name := range names {
		storage, err := GetStorageByName(ctx, name)
		if err != nil {
			complete = false
			continue
		}
		available = append(available, storage)
	}
	// Failed initialization must be retried on the next request, rather than
	// permanently caching an empty or incomplete storage list.
	if complete {
		userStoragesMu.Lock()
		userStorages[chatID] = slices.Clone(available)
		userStoragesMu.Unlock()
	}
	return available
}

func LoadStorages(ctx context.Context) {
	logger := log.FromContext(ctx)
	logger.Debug("loading storages...")
	for _, storage := range config.C().Storages {
		_, err := GetStorageByName(ctx, storage.GetName())
		if err != nil {
			logger.Errorf("failed to load storage %s: %v", storage.GetName(), err)
		}
	}
	loaded := AllStorages()
	logger.Infof("successfully loaded %d storages", len(loaded))
	if config.C().Stream {
		for name, s := range loaded {
			if cs, ok := s.(StorageCannotStream); ok {
				logger.Warnf("stream=true but storage %q cannot stream: %s (will fall back to temp file)", name, cs.CannotStream())
			}
		}
	}
	for _, userID := range config.C().GetUsersID() {
		GetUserStorages(ctx, userID)
	}
}

// GetTelegramStorageByUserID returns the first enabled Telegram storage for the user
func GetTelegramStorageByUserID(ctx context.Context, chatID int64) (Storage, error) {
	storages := GetUserStorages(ctx, chatID)
	for _, stor := range storages {
		if stor.Type() == storenum.Telegram {
			return stor, nil
		}
	}
	return nil, fmt.Errorf("no telegram storage found for user %d", chatID)
}

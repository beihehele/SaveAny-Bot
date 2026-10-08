package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/krau/SaveAny-Bot/config"
	storcfg "github.com/krau/SaveAny-Bot/config/storage"
	storenum "github.com/krau/SaveAny-Bot/pkg/enums/storage"
)

type registryTestStorage struct {
	Storage
	name string
	init func(context.Context, string) error
}

func (s *registryTestStorage) Init(ctx context.Context, cfg storcfg.StorageConfig) error {
	s.name = cfg.GetName()
	return s.init(ctx, s.name)
}

func (s *registryTestStorage) Name() string { return s.name }

func setupRegistryTest(t *testing.T, init func(context.Context, string) error) {
	t.Helper()
	oldStorages, oldUsers := storages, userStorages
	oldFlight := initFlight
	oldConstructor := storageConstructors[storenum.Local]
	storages = make(map[string]Storage)
	userStorages = make(map[int64][]Storage)
	initFlight = new(singleflight.Group)
	storageConstructors[storenum.Local] = func() Storage {
		return &registryTestStorage{init: init}
	}
	t.Cleanup(func() {
		storages, userStorages = oldStorages, oldUsers
		storageConstructors[storenum.Local] = oldConstructor
		initFlight = oldFlight
	})
	path := filepath.Join(t.TempDir(), "config.toml")
	err := os.WriteFile(path, []byte(`
[[storages]]
name = "first"
type = "local"
enable = true
base_path = "unused"
[[storages]]
name = "second"
type = "local"
enable = true
base_path = "unused"
[[users]]
id = 41
storages = ["first"]
[[users]]
id = 42
storages = ["second", "first"]
`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Init(context.Background(), path); err != nil {
		t.Fatal(err)
	}
}

func TestStorageRegistryConcurrentInitialization(t *testing.T) {
	var calls atomic.Int32
	setupRegistryTest(t, func(context.Context, string) error {
		calls.Add(1)
		return nil
	})
	start := make(chan struct{})
	results := make(chan Storage, 24)
	errorsCh := make(chan error, 24)
	var wg sync.WaitGroup
	for range 24 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			stor, err := GetStorageByName(context.Background(), "first")
			results <- stor
			errorsCh <- err
			// Exercise readers while other requests may still be initializing.
			GetStorage("first")
			delete(AllStorages(), "first")
			userStorage := GetUserStorages(context.Background(), 41)
			if len(userStorage) > 0 {
				userStorage[0] = nil
			}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	first, ok := GetStorage("first")
	if !ok || calls.Load() != 1 {
		t.Fatalf("storage initialized %d times, present=%v", calls.Load(), ok)
	}
	for stor := range results {
		if stor != first {
			t.Fatal("concurrent callers received different storage instances")
		}
	}
}

func TestStorageInitializationDoesNotBlockOtherNames(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	setupRegistryTest(t, func(ctx context.Context, name string) error {
		if name == "first" {
			close(entered)
			select {
			case <-release:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := GetStorageByName(ctx, "first")
		done <- err
	}()
	<-entered
	other := make(chan error, 1)
	go func() {
		_, err := GetStorageByName(ctx, "second")
		// Snapshots must also remain available during slow initialization.
		AllStorages()
		other <- err
	}()
	otherFinished := false
	select {
	case err := <-other:
		otherFinished = true
		if err != nil {
			t.Error(err)
		}
	case <-ctx.Done():
		t.Error("unrelated storage initialization or snapshot was blocked")
	}
	close(release)
	if err := <-done; err != nil {
		t.Error(err)
	}
	// Wait for the other goroutine before restoring the registry.
	if !otherFinished {
		<-other
	}
}

func TestLoadStoragesCachesRealUserIDsAndProtectsPermissions(t *testing.T) {
	setupRegistryTest(t, func(context.Context, string) error { return nil })
	LoadStorages(context.Background())
	for _, id := range []int64{41, 42} {
		if _, ok := userStorages[id]; !ok {
			t.Fatalf("missing cache for actual user ID %d", id)
		}
	}
	if _, ok := userStorages[1]; ok {
		t.Fatal("cached a configuration index instead of a user ID")
	}
	first := GetUserStorages(context.Background(), 41)
	if len(first) != 1 || first[0].Name() != "first" {
		t.Fatalf("unexpected user 41 storages: %v", first)
	}
	first[0] = nil
	if got := GetUserStorages(context.Background(), 41); len(got) != 1 || got[0] == nil {
		t.Fatal("caller mutated the cached storage slice")
	}
	second := GetUserStorages(context.Background(), 42)
	if len(second) != 2 || second[0].Name() != "second" || second[1].Name() != "first" {
		t.Fatal("configured storage order was lost")
	}
	if _, err := GetStorageByUserIDAndName(context.Background(), 41, "second"); err == nil {
		t.Fatal("user gained access to an unconfigured storage")
	}
	if got := GetUserStorages(context.Background(), 999); len(got) != 0 {
		t.Fatal("unknown user gained access to storages")
	}
}

func TestUserStorageCacheRetriesFailedInitialization(t *testing.T) {
	var attempts atomic.Int32
	setupRegistryTest(t, func(_ context.Context, name string) error {
		if name == "first" && attempts.Add(1) == 1 {
			return errors.New("temporary initialization failure")
		}
		return nil
	})
	ctx := context.Background()
	got := GetUserStorages(ctx, 42)
	if len(got) != 1 || got[0].Name() != "second" {
		t.Fatal("successful storages should remain usable during partial failure")
	}
	if _, ok := userStorages[42]; ok {
		t.Fatal("incomplete storage list was cached")
	}
	if _, ok := GetStorage("first"); ok {
		t.Fatal("failed storage was registered")
	}
	got = GetUserStorages(ctx, 42)
	if len(got) != 2 || got[1].Name() != "first" || attempts.Load() != 2 {
		t.Fatal("failed initialization did not recover on next request")
	}
}

func TestStorageInitializationWaiterRespectsDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	setupRegistryTest(t, func(ctx context.Context, _ string) error {
		calls.Add(1)
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := GetStorageByName(context.Background(), "first")
		firstDone <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	secondDone := make(chan error, 1)
	go func() {
		_, err := GetStorageByName(ctx, "first")
		secondDone <- err
	}()
	finished := false
	select {
	case err := <-secondDone:
		finished = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("waiter returned %v instead of its own deadline error", err)
		}
	case <-time.After(time.Second):
		t.Error("waiter remained blocked after its deadline")
	}
	// A waiter's timeout must not cancel the original caller's initialization.
	close(release)
	if err := <-firstDone; err != nil {
		t.Error(err)
	}
	if !finished {
		if err := <-secondDone; !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("expired waiter eventually returned %v", err)
		}
	}
	if _, ok := GetStorage("first"); !ok || calls.Load() != 1 {
		t.Errorf("initialization was canceled or repeated: present=%v calls=%d", ok, calls.Load())
	}
}

func TestStorageInitializationRejectsCanceledContext(t *testing.T) {
	var calls atomic.Int32
	setupRegistryTest(t, func(context.Context, string) error {
		calls.Add(1)
		return nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := GetStorageByName(ctx, "first"); !errors.Is(err, context.Canceled) {
		t.Errorf("canceled request returned %v", err)
	}
	if calls.Load() != 0 {
		t.Fatal("canceled request started storage initialization")
	}
}

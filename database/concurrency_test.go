package database

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/krau/SaveAny-Bot/config"
)

func TestConcurrentReadWriteTransactions(t *testing.T) {
	const marker = "SAVEANY_DATABASE_CONCURRENCY_TEST"
	root := os.Getenv(marker)
	if root == "" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, binary, "-test.run=^TestConcurrentReadWriteTransactions$", "-test.count=1")
		command.Env = append(os.Environ(), marker+"="+t.TempDir())
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("concurrent database operations: %v\n%s", err, output)
		}
		return
	}
	t.Chdir(root)
	configuration := `[[storages]]
name='drive'
type='shardreel'
enable=true
url='http://127.0.0.1:1'
api_key='isolated-database-contention-test-key'
[[users]]
id=42
storages=['drive']
`
	file := filepath.Join(root, "config.toml")
	if err := os.WriteFile(file, []byte(configuration), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), file); err != nil {
		t.Fatal(err)
	}
	Init(t.Context())
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := connection.Close(); err != nil {
			t.Error(err)
		}
	})
	const workers, writes = 6, 12
	start := make(chan struct{})
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			<-start
			for index := range writes {
				err := db.WithContext(t.Context()).Transaction(func(tx *gorm.DB) error {
					var count int64
					if err := tx.Model(&ShardReelTask{}).Count(&count).Error; err != nil {
						return err
					}
					// Let a competing upload transaction attempt a write after
					// this read, reproducing the live reservation lock conflict.
					time.Sleep(time.Millisecond)
					return tx.Create(&ShardReelTask{ID: fmt.Sprintf("%d-%d", worker, index), Status: "running"}).Error
				})
				if err != nil {
					errors <- err
					return
				}
			}
		})
	}
	close(start)
	group.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	var count int64
	if err := db.Model(&ShardReelTask{}).Count(&count).Error; err != nil || count != workers*writes {
		t.Fatalf("committed tasks: %d, error: %v", count, err)
	}
}

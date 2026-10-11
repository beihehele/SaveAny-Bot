package batchtfile

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/config"
	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	filepkg "github.com/krau/SaveAny-Bot/pkg/tfile"
	"github.com/krau/SaveAny-Bot/storage/local"
)

func TestAlbumCacheIgnoresFilenamesAndCleansOwnedFiles(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "cache")
	p := filepath.Join(root, "config.toml")
	if err := os.WriteFile(p, []byte(fmt.Sprintf("workers=2\nthreads=1\nretry=1\nstream=false\n[temp]\nbase_path=%q\n", filepath.ToSlash(cacheDir))), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "sentinel.bin")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	stor := &local.Local{}
	if err := stor.Init(t.Context(), &storconfig.LocalStorageConfig{BasePath: filepath.Join(root, "saved")}); err != nil {
		t.Fatal(err)
	}
	names := []string{"../../../sentinel.bin", `..\..\..\sentinel.bin`, sentinel, strings.Repeat("a", 400) + ".bin", "same.bin", "same.bin"}
	elems := make([]TaskElement, 0, len(names))
	for index, name := range names {
		file := filepkg.NewTGFile(&tg.InputDocumentFileLocation{}, downloadClient{}, 4, name)
		elem, err := NewTaskElement(stor, fmt.Sprintf("file-%d.bin", index), file)
		if err != nil {
			t.Fatal(err)
		}
		elems = append(elems, *elem)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("constructor allocated cache: %v", err)
	}
	task := NewBatchTGFileTask("album", t.Context(), elems, nil)
	if err := task.Execute(t.Context()); err != nil {
		t.Fatal(err)
	}
	for index := range names {
		data, err := os.ReadFile(filepath.Join(root, "saved", fmt.Sprintf("file-%d.bin", index)))
		if err != nil || string(data) != "data" {
			t.Fatalf("album item changed: %q %v", data, err)
		}
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "untouched" {
		t.Fatalf("outside sentinel changed: %q %v", data, err)
	}
	entries, err := os.ReadDir(cacheDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("owned cache files remain: %v %v", entries, err)
	}
	if summary := task.ResultSummary(); summary.Succeeded != len(names) || summary.Failed != 0 {
		t.Fatalf("album outcomes changed: %+v", summary)
	}
}

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	storconfig "github.com/krau/SaveAny-Bot/config/storage"
	"github.com/krau/SaveAny-Bot/pkg/enums/ctxkey"
)

func testLocal(t *testing.T, base string) *Local {
	t.Helper()
	l := &Local{}
	if err := l.Init(t.Context(), &storconfig.LocalStorageConfig{BasePath: base}); err != nil {
		t.Fatal(err)
	}
	return l
}

func TestLocalPaths(t *testing.T) {
	base := t.TempDir()
	l := testLocal(t, filepath.Join(base, "store"))
	for _, p := range []string{"../outside", "a/../../outside", `..\outside`, `C:\outside`, `\\server\outside`} {
		t.Run(p, func(t *testing.T) {
			if err := l.Save(t.Context(), strings.NewReader("bad"), p); err == nil {
				t.Fatal("accepted escaping save")
			}
			if f, _, err := l.OpenFile(t.Context(), p); err == nil {
				f.Close()
				t.Fatal("accepted escaping read")
			}
			if _, err := l.ListFiles(t.Context(), p); err == nil {
				t.Fatal("accepted escaping listing")
			}
			if l.Exists(t.Context(), p) {
				t.Fatal("escaping path exists")
			}
		})
	}
	if err := l.Save(t.Context(), strings.NewReader("ok"), "/photos/a.txt"); err != nil {
		t.Fatal(err)
	}
	f, size, err := l.OpenFile(t.Context(), "photos/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil || string(b) != "ok" || size != 2 {
		t.Fatalf("content=%q size=%d err=%v", b, size, err)
	}
	files, err := l.ListFiles(t.Context(), "/photos")
	if err != nil || len(files) != 1 || files[0].Path != filepath.Join("/photos", "a.txt") {
		t.Fatalf("files=%v err=%v", files, err)
	}
	if !l.Exists(t.Context(), "/photos/a.txt") {
		t.Fatal("saved file missing")
	}
}

type brokenReader struct{ err error }

func (r brokenReader) Read(p []byte) (int, error) { return copy(p, "partial"), r.err }

func TestLocalFailedWritePreservesTarget(t *testing.T) {
	base := t.TempDir()
	l := testLocal(t, base)
	if err := l.Save(t.Context(), strings.NewReader("original"), "file.txt"); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), ctxkey.OverwriteExisting, true)
	for _, writeCtx := range []context.Context{t.Context(), ctx} {
		failure := errors.New("reader disconnected")
		if err := l.Save(writeCtx, brokenReader{failure}, "file.txt"); !errors.Is(err, failure) {
			t.Fatalf("error=%v", err)
		}
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := l.Save(cancelCtx, strings.NewReader("new"), "file.txt"); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v", err)
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != 1 {
		t.Fatalf("leftovers=%v err=%v", entries, err)
	}
	b, _ := os.ReadFile(filepath.Join(base, "file.txt"))
	if string(b) != "original" {
		t.Fatalf("original overwritten: %q", b)
	}
	if err := l.Save(ctx, strings.NewReader("replaced"), "file.txt"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(base, "file.txt"))
	if string(b) != "replaced" {
		t.Fatalf("overwrite failed: %q", b)
	}
}

func TestLocalConcurrentNamesAcrossInstances(t *testing.T) {
	base := t.TempDir()
	stores := []*Local{testLocal(t, base), testLocal(t, base)}
	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- stores[i%2].Save(t.Context(), strings.NewReader(fmt.Sprintf("payload-%d", i)), "file.txt")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) != writers {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	seen := make(map[string]bool)
	for _, entry := range entries {
		b, err := os.ReadFile(filepath.Join(base, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if seen[string(b)] {
			t.Fatalf("duplicate content: %q", b)
		}
		seen[string(b)] = true
	}
	for i := range writers {
		if !seen[fmt.Sprintf("payload-%d", i)] {
			t.Fatalf("lost writer %d", i)
		}
	}
}

func TestLocalSymlinkContainment(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "store")
	l := testLocal(t, root)
	out := filepath.Join(base, "outside")
	if err := os.Mkdir(out, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "file.txt"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(out, filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := l.Save(t.Context(), strings.NewReader("bad"), "escape/new.txt"); err == nil {
		t.Fatal("symlink save escaped")
	}
	if f, _, err := l.OpenFile(t.Context(), "escape/file.txt"); err == nil {
		f.Close()
		t.Fatal("symlink read escaped")
	}
	if _, err := l.ListFiles(t.Context(), "escape"); err == nil {
		t.Fatal("symlink listing escaped")
	}
	if l.Exists(t.Context(), "escape/file.txt") {
		t.Fatal("symlink existence escaped")
	}
	b, _ := os.ReadFile(filepath.Join(out, "file.txt"))
	if string(b) != "original" {
		t.Fatal("outside file changed")
	}
	if _, err := os.Stat(filepath.Join(out, "new.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("outside output: %v", err)
	}
}

type noLinkPublisher struct{ *os.Root }

func (r noLinkPublisher) Link(string, string) error { return errors.ErrUnsupported }

func TestLocalPublicationWithoutHardLinks(t *testing.T) {
	base := t.TempDir()
	root, err := os.OpenRoot(base)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.WriteFile("file.txt", []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := range 4 {
		stage := fmt.Sprintf("stage-%d", i)
		if err := root.WriteFile(stage, []byte(fmt.Sprint(i)), 0600); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- publishFile(t.Context(), noLinkPublisher{root}, stage, "file.txt", &mu)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, err := root.ReadFile("file.txt")
	if err != nil || string(b) != "existing" {
		t.Fatalf("existing target changed: %q %v", b, err)
	}
	files, err := os.ReadDir(base)
	if err != nil || len(files) != 5 {
		t.Fatalf("published files=%v err=%v", files, err)
	}
	seen := make(map[string]bool)
	for i := 1; i <= 4; i++ {
		b, err := root.ReadFile(fmt.Sprintf("file_%d.txt", i))
		if err != nil || seen[string(b)] {
			t.Fatalf("lost/corrupted output: %q %v", b, err)
		}
		seen[string(b)] = true
	}
}

func TestLocalOverwriteRetainsPermissions(t *testing.T) {
	base := t.TempDir()
	l := testLocal(t, base)
	p := filepath.Join(base, "private.txt")
	if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), ctxkey.OverwriteExisting, true)
	if err := l.Save(ctx, strings.NewReader("new"), "private.txt"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(p)
	if err != nil || before.Mode().Perm() != after.Mode().Perm() {
		t.Fatalf("permissions changed: before=%v after=%v err=%v", before.Mode(), after, err)
	}
}

func TestLocalListKeepsUserTemporaryFilename(t *testing.T) {
	l := testLocal(t, t.TempDir())
	if err := l.Save(t.Context(), strings.NewReader("user data"), ".saveany-user.tmp"); err != nil {
		t.Fatal(err)
	}
	files, err := l.ListFiles(t.Context(), "")
	if err != nil || len(files) != 1 || files[0].Name != ".saveany-user.tmp" {
		t.Fatalf("user file hidden: %+v %v", files, err)
	}
}

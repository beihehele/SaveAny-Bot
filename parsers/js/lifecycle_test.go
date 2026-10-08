//go:build !no_jsparser

package js

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

func TestParseCancellationWhileVMIsBusy(t *testing.T) {
	rt := newPluginRuntime(goja.New())
	rt.gate <- struct{}{}
	p := &jsParser{runtime: rt}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.Parse(ctx, "https://example.org"); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled parser blocked before enqueue")
	}
	if len(rt.gate) != 1 {
		t.Fatal("cancelled request entered a busy VM")
	}
}

func TestPluginLoaderKeepsValidFilesAfterInvalidPlugin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a-invalid.js"), []byte(installTestCode+`throw new Error("bad");`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b-valid.js"), []byte(installTestCode), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "c-directory.js"), 0700); err != nil {
		t.Fatal(err)
	}
	before := len(parsers.Get())
	err := LoadPlugins(t.Context(), dir)
	if err == nil || !strings.Contains(err.Error(), "a-invalid.js") {
		t.Fatalf("missing failed file: %v", err)
	}
	registered := parsers.Get()
	if len(registered) != before+1 {
		t.Fatalf("registered=%d want=%d", len(registered), before+1)
	}
	if !registered[before].CanHandle("https://example.org") {
		t.Fatal("valid plugin not usable")
	}
	// A caller must not be able to mutate the registry through its snapshot.
	registered[before] = nil
	if parsers.Get()[before] == nil {
		t.Fatal("registry snapshot aliases internal slice")
	}
}

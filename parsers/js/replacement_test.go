//go:build !no_jsparser

package js

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

func replacementCode(title string) string {
	return fmt.Sprintf(`registerParser({metadata:{version:"1.0.0"},canHandle:function(){return true},parse:function(url){return {title:%q,url:url,resources:[]}}});`, title)
}

func TestPluginReplacementSwapsWholeSourceAndPreservesFailedReplacement(t *testing.T) {
	dir := setupPluginDirectory(t)
	before := len(parsers.Get())
	initialSource, _ := pluginSource(filepath.Join(dir, "replace.js"))
	if err := AddPlugin(t.Context(), replacementCode("v1")+replacementCode("v1-extra"), "replace.js"); err != nil {
		t.Fatal(err)
	}
	old := parsers.Get()[before].(*jsParser)
	actualSource, _ := pluginSource(filepath.Join(dir, "replace.js"))
	if initialSource != actualSource {
		t.Fatalf("source identity changed after mkdir: %q -> %q", initialSource, actualSource)
	}
	if err := AddPlugin(t.Context(), replacementCode("v2"), "replace.js"); err != nil {
		t.Fatal(err)
	}
	current := parsers.Get()
	if len(current) != before+1 {
		t.Fatal("old source parsers remained registered")
	}
	item, err := current[before].Parse(t.Context(), "test")
	if err != nil || item.Title != "v2" {
		t.Fatalf("active=%+v err=%v", item, err)
	}
	if _, err := old.Parse(t.Context(), "test"); !errors.Is(err, errPluginRetired) {
		t.Fatalf("old snapshot still usable: %v", err)
	}
	if old.CanHandle("test") {
		t.Fatal("retired match still selected")
	}
	if err := AddPlugin(t.Context(), replacementCode("bad")+`throw new Error("failed");`, "replace.js"); err == nil {
		t.Fatal("broken replacement succeeded")
	}
	if got, err := os.ReadFile(filepath.Join(dir, "replace.js")); err != nil || string(got) != replacementCode("v2") {
		t.Fatalf("valid file changed: %q %v", got, err)
	}
	if got := parsers.Get()[before]; got != current[before] {
		t.Fatal("failed install replaced runtime")
	}
	// Startup loading uses the same source key and must not append duplicates.
	if err := LoadPlugins(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if len(parsers.Get()) != before+1 {
		t.Fatal("reloading appended duplicate source")
	}
}

func TestReplacingActivePluginKeepsResultButSuppressesLateRegistration(t *testing.T) {
	setupPluginDirectory(t)
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	before := len(parsers.Get())
	code := `const registerLater=registerParser;
registerParser({metadata:{version:"1.0.0"},canHandle:function(){return true},parse:function(url){
ghttp.get(url);
registerLater({metadata:{version:"1.0.0"},canHandle:function(){return true},parse:function(){return {title:"stale",resources:[]}}});
return {title:"old-active",resources:[]};}});`
	if err := AddPlugin(t.Context(), code, "active.js"); err != nil {
		t.Fatal(err)
	}
	old := parsers.Get()[before].(*jsParser)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		item, err := old.Parse(ctx, server.URL)
		if err == nil && item.Title != "old-active" {
			err = fmt.Errorf("old result=%+v", item)
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("old invocation not started")
	}
	queued := make(chan error, 1)
	go func() { _, err := old.Parse(ctx, "queued"); queued <- err }()
	if err := AddPlugin(t.Context(), replacementCode("new"), "active.js"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-queued:
		if !errors.Is(err, errPluginRetired) {
			t.Fatalf("queued error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("retired waiting caller did not wake")
	}
	// Release the active call without closing the channel twice in cleanup.
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("active call failed to finish")
	}
	if len(parsers.Get()) != before+1 {
		t.Fatal("retired registerParser polluted new source")
	}
	item, err := parsers.Get()[before].Parse(t.Context(), "test")
	if err != nil || item.Title != "new" {
		t.Fatalf("new result=%+v err=%v", item, err)
	}
}

func TestSameFilenameInDifferentDirectoriesRemainsIndependent(t *testing.T) {
	firstDir := setupPluginDirectory(t)
	before := len(parsers.Get())
	if err := AddPlugin(t.Context(), replacementCode("first"), "same.js"); err != nil {
		t.Fatal(err)
	}
	secondDir := setupPluginDirectory(t)
	if firstDir == secondDir {
		t.Fatal("test directories overlap")
	}
	if err := AddPlugin(t.Context(), replacementCode("second"), "same.js"); err != nil {
		t.Fatal(err)
	}
	if len(parsers.Get()) != before+2 {
		t.Fatal("different source paths replaced one another")
	}
	for i, want := range []string{"first", "second"} {
		item, err := parsers.Get()[before+i].Parse(t.Context(), "")
		if err != nil || item.Title != want {
			t.Fatalf("source %d=%+v %v", i, item, err)
		}
	}
}

func TestPluginInstallationLockRespondsToCancellation(t *testing.T) {
	dir := setupPluginDirectory(t)
	source, err := pluginSource(filepath.Join(dir, "busy.js"))
	if err != nil {
		t.Fatal(err)
	}
	release, err := lockPlugin(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := AddPlugin(ctx, replacementCode("blocked"), "busy.js"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
}

func TestConcurrentInstallAndLoadLeaveFileAndRuntimeConsistent(t *testing.T) {
	dir := setupPluginDirectory(t)
	before := len(parsers.Get())
	if err := AddPlugin(t.Context(), replacementCode("initial"), "concurrent.js"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Go(func() {
			if err := AddPlugin(t.Context(), replacementCode(fmt.Sprintf("version-%d", i)), "concurrent.js"); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if err := LoadPlugins(t.Context(), dir); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	registered := parsers.Get()
	if len(registered) != before+1 {
		t.Fatalf("source duplicated: count=%d want=%d", len(registered), before+1)
	}
	item, err := registered[before].Parse(t.Context(), "test")
	if err != nil {
		t.Fatal(err)
	}
	code, err := os.ReadFile(filepath.Join(dir, "concurrent.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(code), fmt.Sprintf("title:%q", item.Title)) {
		t.Fatalf("file and runtime differ: title=%q code=%s", item.Title, code)
	}
}

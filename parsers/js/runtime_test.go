//go:build !no_jsparser

package js

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

func testRuntimeParsers(t *testing.T, rt *pluginRuntime, code string) []*jsParser {
	t.Helper()
	registration, err := runPlugin(t.Context(), rt, code)
	if err != nil {
		t.Fatal(err)
	}
	result := make([]*jsParser, 0, len(registration.definitions))
	for _, d := range registration.definitions {
		result = append(result, newJSParser(rt, d.canHandle, d.parse, d.metadata))
	}
	return result
}

func TestParsersFromOneScriptSerializeVM(t *testing.T) {
	rt, err := createPluginRuntime(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int32
	if err := rt.vm.Set("probe", func() {
		if active.Add(1) != 1 {
			t.Error("multiple parsers entered one VM concurrently")
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
	}); err != nil {
		t.Fatal(err)
	}
	code := `var count=0; function parse(url){probe();count++;return {title:String(count),url:url,resources:[]};}
registerParser({metadata:{version:"1.0.0"},canHandle:function(){probe();return true},parse:parse});
registerParser({metadata:{version:"1.0.0"},canHandle:function(){probe();return true},parse:parse});`
	ps := testRuntimeParsers(t, rt, code)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := make(map[string]bool)
	for i := range 40 {
		wg.Go(func() {
			p := ps[i%2]
			if !p.CanHandle("https://example.test") {
				t.Error("canHandle failed")
				return
			}
			item, err := p.Parse(t.Context(), "https://example.test")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if seen[item.Title] {
				t.Errorf("shared state repeated result %s", item.Title)
			}
			seen[item.Title] = true
		})
	}
	wg.Wait()
	if len(seen) != 40 {
		t.Fatalf("unique results=%d", len(seen))
	}
}

func TestRuntimeInterruptsLoopAndCanBeReused(t *testing.T) {
	rt, err := createPluginRuntime(t.Context(), t.Name())
	if err != nil {
		t.Fatal(err)
	}
	p := testRuntimeParsers(t, rt, `registerParser({metadata:{version:"1.0.0"},canHandle:function(url){if(url==="loop")while(true){};return true},parse:function(url){if(url==="loop")while(true){};return {url:url,resources:[]}}});`)[0]
	for _, method := range []string{"parse", "match"} {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		if method == "parse" {
			if _, err := p.Parse(ctx, "loop"); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error=%v", err)
			}
		} else if p.CanHandleContext(ctx, "loop") {
			t.Fatal("looping canHandle succeeded")
		}
		cancel()
		if !p.CanHandle("normal") {
			t.Fatal("interrupt poisoned next match")
		}
		item, err := p.Parse(t.Context(), "normal")
		if err != nil || item.URL != "normal" {
			t.Fatalf("next parse=%+v err=%v", item, err)
		}
	}
}

func TestCancelledPluginInitializationDoesNotCommit(t *testing.T) {
	dir := setupPluginDirectory(t)
	before := len(parsers.Get())
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := AddPlugin(ctx, installTestCode+`while(true){}`, "loop.js"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	if len(parsers.Get()) != before {
		t.Fatal("cancelled initialization published a parser")
	}
	if _, err := os.Stat(filepath.Join(dir, "loop.js")); !os.IsNotExist(err) {
		t.Fatalf("cancelled initialization wrote plugin: %v", err)
	}
}

func TestPluginHTTPCancellationAndReuse(t *testing.T) {
	for _, method := range []string{"get", "getJSON", "head"} {
		t.Run(method, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/blocked" {
					close(started)
					<-r.Context().Done()
					close(stopped)
					return
				}
				io.WriteString(w, `{"ok":true}`)
			}))
			defer server.Close()
			rt, err := createPluginRuntime(t.Context(), t.Name())
			if err != nil {
				t.Fatal(err)
			}
			code := fmt.Sprintf(`registerParser({metadata:{version:"1.0.0"},canHandle:function(){return true},parse:function(url){ghttp.%s(url);return {url:url,resources:[]}}});`, method)
			p := testRuntimeParsers(t, rt, code)[0]
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.Parse(ctx, server.URL+"/blocked"); done <- err }()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("HTTP request did not start")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("HTTP request ignored cancellation")
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("server request not cancelled")
			}
			if _, err := p.Parse(t.Context(), server.URL+"/normal"); err != nil {
				t.Fatalf("next invocation failed: %v", err)
			}
		})
	}
}

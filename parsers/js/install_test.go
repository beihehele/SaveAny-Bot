//go:build !no_jsparser

package js

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

const installTestCode = `registerParser({metadata:{name:"install",version:"1.0.0"},canHandle:function(){return true},parse:function(url){return {url:url,resources:[]}}});`

func TestPluginInstallRejectsUnsafeNamesBeforeExecution(t *testing.T) {
	dir := setupPluginDirectory(t)
	for _, name := range []string{"", "..", "../escape.js", `..\escape.js`, "/absolute.js", `C:\absolute.js`, "drive:stream.js", "bad.txt"} {
		t.Run(name, func(t *testing.T) {
			before := len(parsers.Get())
			if err := AddPlugin(context.Background(), installTestCode, name); err == nil || !strings.Contains(err.Error(), "filename") {
				t.Fatalf("error = %v", err)
			}
			if len(parsers.Get()) != before {
				t.Fatal("invalid filename registered a parser")
			}
		})
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("rejected installs created plugin directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.js")); !os.IsNotExist(err) {
		t.Fatalf("wrote outside directory: %v", err)
	}
}

func TestPluginInstallFailureDoesNotRegister(t *testing.T) {
	for _, failure := range []string{"directory creation", "destination directory", "script exception", "mixed definitions"} {
		t.Run(failure, func(t *testing.T) {
			dir := setupPluginDirectory(t)
			code := installTestCode
			switch failure {
			case "directory creation":
				if err := os.WriteFile(dir, []byte("occupied"), 0600); err != nil {
					t.Fatal(err)
				}
			case "destination directory":
				if err := os.MkdirAll(filepath.Join(dir, "plugin.js"), 0700); err != nil {
					t.Fatal(err)
				}
			case "script exception":
				code += `throw new Error("failed after registration");`
			case "mixed definitions":
				code += `registerParser({metadata:{version:"invalid"}});`
			}
			before := len(parsers.Get())
			if err := AddPlugin(context.Background(), code, "plugin.js"); err == nil {
				t.Fatal("failed install reported success")
			}
			if len(parsers.Get()) != before {
				t.Fatal("failed install left a registered parser")
			}
			if failure == "script exception" || failure == "mixed definitions" {
				if _, err := os.Stat(filepath.Join(dir, "plugin.js")); !os.IsNotExist(err) {
					t.Fatalf("invalid script was saved: %v", err)
				}
			}
		})
	}
}

func TestPluginInstallReplacesFileOnlyAfterValidation(t *testing.T) {
	dir := setupPluginDirectory(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plugin.js")
	if err := os.WriteFile(path, []byte("// original"), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(parsers.Get())
	if err := AddPlugin(context.Background(), installTestCode+`throw new Error("failed");`, "plugin.js"); err == nil {
		t.Fatal("invalid replacement succeeded")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "// original" {
		t.Fatalf("original changed: %q %v", got, err)
	}
	if len(parsers.Get()) != before {
		t.Fatal("failed replacement registered a parser")
	}
	if err := AddPlugin(context.Background(), installTestCode, "plugin.js"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != installTestCode {
		t.Fatalf("replacement not saved: %q %v", got, err)
	}
	registered := parsers.Get()
	if len(registered) != before+1 {
		t.Fatal("replacement not registered exactly once")
	}
	p := registered[len(registered)-1].(*jsParser)
	if !p.CanHandle("https://example.test") {
		t.Fatal("replacement parser does not work")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary plugin files left behind: %v %v", entries, err)
	}
}

func TestPluginLoadRejectsPartialRegistration(t *testing.T) {
	dir := setupPluginDirectory(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "broken.js"), []byte(installTestCode+`throw new Error("failed");`), 0600); err != nil {
		t.Fatal(err)
	}
	before := len(parsers.Get())
	if err := LoadPlugins(context.Background(), dir); err == nil {
		t.Fatal("broken script loaded")
	}
	if len(parsers.Get()) != before {
		t.Fatal("broken startup plugin partially registered")
	}
}

func TestPluginRenameFailurePreservesOriginalAndCleansStaging(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows rejects replacement of a read-only destination")
	}
	dir := setupPluginDirectory(t)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "plugin.js")
	if err := os.WriteFile(path, []byte("// original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(path, 0600); err != nil {
			t.Error(err)
		}
	})
	before := len(parsers.Get())
	if err := AddPlugin(context.Background(), installTestCode, "plugin.js"); err == nil {
		t.Fatal("read-only replacement succeeded")
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != "// original" {
		t.Fatalf("original changed: %q %v", got, err)
	}
	if len(parsers.Get()) != before {
		t.Fatal("failed rename registered a parser")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("staging files leaked after rename failure: %v %v", entries, err)
	}
}

func TestRunPluginStagesMultipleDefinitions(t *testing.T) {
	vm := goja.New()
	before := len(parsers.Get())
	definitions, err := runPlugin(t.Context(), newPluginRuntime(vm), installTestCode+installTestCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions.definitions) != 2 {
		t.Fatalf("definitions=%d", len(definitions.definitions))
	}
	if len(parsers.Get()) != before {
		t.Fatal("staged definitions became visible before commit")
	}
}

func TestCapturedRegisterParserWorksAfterInstall(t *testing.T) {
	setupPluginDirectory(t)
	before := len(parsers.Get())
	code := `const registerLater = registerParser;
registerParser({metadata:{version:"1.0.0"}, canHandle:function(){return true},
parse:function(url){
registerLater({metadata:{version:"1.0.0"},canHandle:function(){return false},parse:function(){return {resources:[]}}});
return {url:url,resources:[]};
}});`
	if err := AddPlugin(context.Background(), code, "later.js"); err != nil {
		t.Fatal(err)
	}
	p := parsers.Get()[before]
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := p.Parse(ctx, "https://example.test"); err != nil {
		t.Fatal(err)
	}
	if got := len(parsers.Get()); got != before+2 {
		t.Fatalf("runtime registration count=%d want %d", got, before+2)
	}
}

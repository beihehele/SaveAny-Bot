//go:build !no_jsparser

package js

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

func TestRegisterParserRejectsInvalidDefinitions(t *testing.T) {
	cases := []struct {
		name, definition, want string
	}{
		{"invalid version", `{metadata: {version: "invalid"}, canHandle: function() {}, parse: function() {}}`, "invalid parser version"},
		{"empty version", `{metadata: {}, canHandle: function() {}, parse: function() {}}`, "invalid parser version"},
		{"old version", `{metadata: {version: "0.9.0"}, canHandle: function() {}, parse: function() {}}`, "must be at least"},
		{"missing parse", `{metadata: {version: "1.0.0"}, canHandle: function() {}}`, "parse function"},
		{"noncallable parse", `{metadata: {version: "1.0.0"}, canHandle: function() {}, parse: 42}`, "parse function"},
		{"null parse", `{metadata: {version: "1.0.0"}, canHandle: function() {}, parse: null}`, "parse function"},
		{"missing canHandle", `{metadata: {version: "1.0.0"}, parse: function() {}}`, "canHandle function"},
		{"noncallable canHandle", `{metadata: {version: "1.0.0"}, canHandle: {}, parse: function() {}}`, "canHandle function"},
		{"null definition", `null`, "expects an object"},
		{"missing metadata", `{canHandle: function() {}, parse: function() {}}`, "provide metadata"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vm := goja.New()
			if err := vm.Set("registerParser", jsRegisterParser(vm, nil)); err != nil {
				t.Fatal(err)
			}
			before := len(parsers.Get())
			value, err := vm.RunString("registerParser(" + tc.definition + ")")
			if err != nil {
				t.Fatalf("registration must return its existing Error-object contract: %v", err)
			}
			if value == nil || goja.IsUndefined(value) {
				t.Fatal("invalid definition was accepted")
			}
			message := value.ToObject(vm).Get("message")
			if message == nil || !strings.Contains(message.String(), tc.want) {
				t.Fatalf("error=%v want message containing %q", value, tc.want)
			}
			if len(parsers.Get()) != before {
				t.Fatal("invalid parser was added to the registry")
			}
		})
	}
}

func TestRegisterParserPreservesValidPluginBehavior(t *testing.T) {
	for _, version := range []string{"1.0.0", "1.0.0+build", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			vm := goja.New()
			if err := vm.Set("registerParser", jsRegisterParser(vm, nil)); err != nil {
				t.Fatal(err)
			}
			before := len(parsers.Get())
			code := fmt.Sprintf(`registerParser({
metadata: {name: "test", version: %q},
canHandle: function(url) { return url === "https://example.test/item"; },
parse: function(url) {
    return {title: "test item", url: url, resources: [{url: url + "/file", filename: "file.jpg"}]};
}

})`, version)
			value, err := vm.RunString(code)
			if err != nil || !goja.IsUndefined(value) {
				t.Fatalf("valid registration failed: value=%v error=%v", value, err)
			}
			registered := parsers.Get()
			if len(registered) != before+1 {
				t.Fatal("valid parser was not registered exactly once")
			}
			p := registered[len(registered)-1].(*jsParser)
			url := "https://example.test/item"
			if !p.CanHandle(url) || p.CanHandle("https://example.test/other") {
				t.Fatal("canHandle behavior changed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			item, err := p.Parse(ctx, url)
			if err != nil {
				t.Fatal(err)
			}
			if item.Title != "test item" || item.URL != url || len(item.Resources) != 1 || item.Resources[0].Filename != "file.jpg" {
				t.Fatalf("plugin result changed: %+v", item)
			}
		})
	}
}

func setupPluginDirectory(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "plugins")
	configPath := filepath.Join(root, "config.toml")
	data := fmt.Sprintf("[parser]\nplugin_dirs = [%q]\n", filepath.ToSlash(dir))
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(context.Background(), configPath); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPluginEntriesRejectInvalidRegistration(t *testing.T) {
	definitions := []struct {
		name, definition, want string
	}{
		{"version", `{metadata: {version: "invalid"}, canHandle: function() {}, parse: function() {}}`, "invalid parser version"},
		{"parse", `{metadata: {version: "1.0.0"}, canHandle: function() {}, parse: 42}`, "parse function"},
		{"canHandle", `{metadata: {version: "1.0.0"}, parse: function() {}}`, "canHandle function"},
	}
	for _, tc := range definitions {
		t.Run(tc.name, func(t *testing.T) {
			dir := setupPluginDirectory(t)
			// The script deliberately ignores the returned JS Error object.
			code := "registerParser(" + tc.definition + "); void 0;"
			before := len(parsers.Get())
			if err := AddPlugin(context.Background(), code, "bad.js"); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("installation error=%v want %q", err, tc.want)
			}
			if _, err := os.Stat(filepath.Join(dir, "bad.js")); !os.IsNotExist(err) {
				t.Errorf("failed installation wrote a plugin: %v", err)
			}
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "existing.js")
			original := []byte("// existing plugin file")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			if err := AddPlugin(context.Background(), code, "existing.js"); err == nil {
				t.Error("invalid replacement was reported as successful")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
				t.Errorf("invalid replacement changed the existing file: content=%q error=%v", got, err)
			}
			if err := os.WriteFile(filepath.Join(dir, "invalid.js"), []byte(code), 0600); err != nil {
				t.Fatal(err)
			}
			if err := LoadPlugins(context.Background(), dir); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("startup loading error=%v want %q", err, tc.want)
			}
			if len(parsers.Get()) != before {
				t.Error("invalid parser was registered")
			}
		})
	}
}

func TestPluginEntriesPreserveValidRegistration(t *testing.T) {
	for _, entry := range []string{"install", "load"} {
		t.Run(entry, func(t *testing.T) {
			dir := setupPluginDirectory(t)
			path := filepath.Join(dir, "valid.js")
			code := `registerParser({
metadata: {name: "entry-test", version: "1.0.0"},
canHandle: function(url) { return url === "https://example.test/item"; },
parse: function(url) { return {title: "entry test", url: url, resources: []}; }
});`
			before := len(parsers.Get())
			if entry == "install" {
				if err := AddPlugin(context.Background(), code, "valid.js"); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(code), 0600); err != nil {
					t.Fatal(err)
				}
				if err := LoadPlugins(context.Background(), dir); err != nil {
					t.Fatal(err)
				}
			}
			if saved, err := os.ReadFile(path); err != nil || string(saved) != code {
				t.Fatalf("valid plugin file changed: content=%q error=%v", saved, err)
			}
			registered := parsers.Get()
			if len(registered) != before+1 {
				t.Fatal("valid plugin was not registered exactly once")
			}
			p := registered[len(registered)-1].(*jsParser)
			url := "https://example.test/item"
			if !p.CanHandle(url) || p.CanHandle("https://example.test/other") {
				t.Fatal("loaded plugin's canHandle behavior changed")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			item, err := p.Parse(ctx, url)
			if err != nil || item == nil || item.Title != "entry test" || item.URL != url {
				t.Fatalf("loaded plugin returned item=%+v error=%v", item, err)
			}
		})
	}
}

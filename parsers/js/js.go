//go:build !no_jsparser

package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
	"github.com/krau/SaveAny-Bot/pkg/parser"
)

type jsParser struct {
	meta      PluginMeta
	runtime   *pluginRuntime
	canHandle goja.Callable
	parse     goja.Callable
}

func (p *jsParser) CanHandle(url string) bool {
	return p.CanHandleContext(context.Background(), url)
}

func (p *jsParser) CanHandleContext(ctx context.Context, url string) bool {
	var ok bool
	err := p.runtime.run(ctx, pluginMatchTimeout, func() error {
		res, err := p.canHandle(goja.Undefined(), p.runtime.vm.ToValue(url))
		if err == nil {
			ok = res.ToBoolean()
		}
		return err
	})
	return ok && err == nil
}

func (p *jsParser) Parse(ctx context.Context, url string) (*parser.Item, error) {
	var item parser.Item
	err := p.runtime.run(ctx, pluginParseTimeout, func() error {
		result, err := p.parse(goja.Undefined(), p.runtime.vm.ToValue(url))
		if err != nil {
			return err
		}
		exported := result.Export()
		if exported == nil {
			return errors.New("JS function returned null or undefined")
		}
		data, err := json.Marshal(exported)
		if err != nil {
			return fmt.Errorf("failed to marshal result to JSON: %w", err)
		}
		if err := json.Unmarshal(data, &item); err != nil {
			return fmt.Errorf("failed to unmarshal JSON to Item: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &item, nil
}

func newJSParser(rt *pluginRuntime, canHandleFunc, parseFunc goja.Value, metadata PluginMeta) *jsParser {
	canHandle, _ := goja.AssertFunction(canHandleFunc)
	parse, _ := goja.AssertFunction(parseFunc)
	return &jsParser{meta: metadata, runtime: rt, canHandle: canHandle, parse: parse}
}

// 加载指定文件夹下的所有 JS 解析器插件
func LoadPlugins(ctx context.Context, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	var failures []error
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if e.IsDir() || filepath.Ext(e.Name()) != ".js" {
			continue
		}
		scriptPath := filepath.Join(dir, e.Name())
		if err := loadPlugin(ctx, scriptPath); err != nil {
			failures = append(failures, fmt.Errorf("error loading plugin %s: %w", e.Name(), err))
		}
	}
	return errors.Join(failures...)
}

type parserDefinition struct {
	canHandle goja.Value
	parse     goja.Value
	metadata  PluginMeta
}

type pluginRegistration struct {
	runtime     *pluginRuntime
	group       *parsers.Group
	definitions []parserDefinition
	committed   bool
}

func (r *pluginRegistration) register(definition parserDefinition) {
	if r.committed {
		r.group.Add(newJSParser(r.runtime, definition.canHandle, definition.parse, definition.metadata))
		return
	}
	r.definitions = append(r.definitions, definition)
}

// runPlugin stages registrations until the whole script succeeds.
func runPlugin(ctx context.Context, rt *pluginRuntime, code string) (*pluginRegistration, error) {
	registration := &pluginRegistration{runtime: rt}
	vm := rt.vm
	var registrationErr error
	err := rt.run(ctx, pluginInitTimeout, func() error {
		if err := vm.Set("registerParser", jsRegisterParserWithHandler(vm, func(err error) {
			if registrationErr == nil {
				registrationErr = err
			}
		}, registration.register)); err != nil {
			return fmt.Errorf("failed to set registerParser: %w", err)
		}
		if _, err := vm.RunString(code); err != nil {
			return err
		}
		return registrationErr
	})
	// Scripts can ignore registerParser's returned Error object. Keep that JS
	// contract, but report validation failure before an installation is saved.
	if err != nil {
		return nil, err
	}
	return registration, nil
}

func registerPlugin(source string, registration *pluginRegistration) {
	registered := make([]parser.Parser, 0, len(registration.definitions))
	for _, definition := range registration.definitions {
		registered = append(registered, newJSParser(registration.runtime, definition.canHandle, definition.parse, definition.metadata))
	}
	// Captured references to registerParser must keep working at runtime.
	// Activate before publishing the parsers, and publish the script as a batch.
	registration.committed = true
	registration.definitions = nil
	registration.group = parsers.NewGroup(source)
	retired := registration.group.Replace(registered...)
	for _, p := range retired {
		if old, ok := p.(*jsParser); ok {
			old.runtime.retire()
		}
	}
}

var pluginLocks sync.Map

func AddPlugin(ctx context.Context, code string, name string) error {
	// Reject separators on both platforms before executing any plugin code.
	if name == "" || strings.ContainsAny(name, "/\\:") || !filepath.IsLocal(name) || filepath.Ext(name) != ".js" {
		return fmt.Errorf("invalid plugin filename %q", name)
	}
	dir := "plugins"
	configuredDirs := config.C().Parser.PluginDirs
	if len(configuredDirs) > 0 {
		dir = configuredDirs[0]
	}
	path := filepath.Join(dir, name)
	source, err := pluginSource(path)
	if err != nil {
		return err
	}
	release, err := lockPlugin(ctx, source)
	if err != nil {
		return err
	}
	defer release()
	rt, err := createPluginRuntime(ctx, name)
	if err != nil {
		return err
	}
	definitions, err := runPlugin(ctx, rt, code)
	if err != nil {
		return fmt.Errorf("error loading plugin %s: %w", name, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}
	if err := writePluginFile(path, []byte(code)); err != nil {
		return fmt.Errorf("failed to save plugin %s: %w", name, err)
	}
	// Once the file is committed, publish its validated definitions even if the
	// install caller is now cancelled. Disk and registry must agree on success.
	registerPlugin(source, definitions)
	return nil
}

func loadPlugin(ctx context.Context, path string) error {
	source, err := pluginSource(path)
	if err != nil {
		return err
	}
	release, err := lockPlugin(ctx, source)
	if err != nil {
		return err
	}
	defer release()
	code, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read plugin: %w", err)
	}
	rt, err := createPluginRuntime(ctx, filepath.Base(path))
	if err != nil {
		return err
	}
	registration, err := runPlugin(ctx, rt, string(code))
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	registerPlugin(source, registration)
	return nil
}

// pluginSource makes installation and startup loading use the same source key.
func pluginSource(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Resolve the nearest existing ancestor too: before creating a plugin
	// directory, Windows temp paths may still contain an 8.3 parent alias.
	parent, tail := filepath.Dir(abs), filepath.Base(abs)
	for {
		resolved, err := filepath.EvalSymlinks(parent)
		if err == nil {
			abs = filepath.Join(resolved, tail)
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next := filepath.Dir(parent)
		if next == parent {
			return "", err
		}
		tail = filepath.Join(filepath.Base(parent), tail)
		parent = next
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	return abs, nil
}

func lockPlugin(ctx context.Context, source string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value, _ := pluginLocks.LoadOrStore(source, make(chan struct{}, 1))
	gate := value.(chan struct{})
	select {
	case gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		<-gate
		return nil, err
	}
	return func() { <-gate }, nil
}

func createPluginRuntime(ctx context.Context, name string) (*pluginRuntime, error) {
	rt := newPluginRuntime(goja.New())
	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("[plugin|parser]/%s", name))
	playwright, err := jsPlaywright(rt.vm, logger, rt.context)
	if err != nil {
		return nil, fmt.Errorf("set playwright methods: %w", err)
	}
	globals := map[string]any{"console": jsConsole(logger), "ghttp": jsGhttp(rt.vm, rt.context), "playwright": playwright}
	for name, value := range globals {
		if err := rt.vm.Set(name, value); err != nil {
			return nil, fmt.Errorf("set plugin global %s: %w", name, err)
		}
	}
	return rt, nil
}

// writePluginFile leaves an existing plugin intact if writing its replacement fails.
func writePluginFile(path string, code []byte) error {
	mode := os.FileMode(0644)
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin destination is not a regular file: %s", path)
		}
		mode = info.Mode().Perm()
	} else if !os.IsNotExist(err) {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".saveany-plugin-*")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(f.Name()); err != nil && !os.IsNotExist(err) {
			log.Warnf("Failed to remove temporary plugin file: %v", err)
		}
	}()
	if _, err := f.Write(code); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Chmod(mode); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Sync(); err != nil {
		return errors.Join(err, f.Close())
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

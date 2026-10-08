//go:build !no_jsparser

package js

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/dop251/goja"
)

var errPluginRetired = errors.New("parser plugin has been replaced")

const (
	pluginInitTimeout  = 30 * time.Second
	pluginMatchTimeout = 5 * time.Second
	pluginParseTimeout = 2 * time.Minute
)

// pluginRuntime serializes every entry into a script's VM, including parsers
// registered later by that script. No idle per-parser goroutines are needed.
type pluginRuntime struct {
	vm             *goja.Runtime
	gate           chan struct{}
	retired        chan struct{}
	retireOnce     sync.Once
	currentContext context.Context // accessed only while holding gate
}

func newPluginRuntime(vm *goja.Runtime) *pluginRuntime {
	return &pluginRuntime{vm: vm, gate: make(chan struct{}, 1), retired: make(chan struct{})}
}

func (r *pluginRuntime) retire() { r.retireOnce.Do(func() { close(r.retired) }) }

func (r *pluginRuntime) context() context.Context {
	if r.currentContext != nil {
		return r.currentContext
	}
	return context.Background()
}

func (r *pluginRuntime) run(ctx context.Context, limit time.Duration, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case r.gate <- struct{}{}:
	case <-r.retired:
		return errPluginRetired
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-r.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-r.retired:
		return errPluginRetired
	default:
	}
	r.currentContext = ctx
	finished, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			r.vm.Interrupt(ctx.Err())
		case <-finished:
		}
	}()
	defer func() {
		close(finished)
		<-stopped
		// Join the interrupter before clearing: a late interrupt must not poison
		// the next invocation after this VM is returned to its gate.
		r.vm.ClearInterrupt()
		r.currentContext = nil
	}()
	err := fn()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

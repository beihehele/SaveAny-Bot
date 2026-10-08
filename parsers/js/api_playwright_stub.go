//go:build no_playwright && !no_jsparser

package js

import (
	"context"

	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
)

func jsPlaywright(vm *goja.Runtime, _ *log.Logger, _ func() context.Context) (*goja.Object, error) {
	pwObj := vm.NewObject()
	unsupported := vm.ToValue(map[string]any{
		"error": "playwright is not supported in this build",
	})
	err := pwObj.Set("get", func(call goja.FunctionCall) goja.Value {
		return unsupported
	})
	return pwObj, err
}

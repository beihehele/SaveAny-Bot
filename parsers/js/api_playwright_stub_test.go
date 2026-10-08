//go:build !no_jsparser && no_playwright

package js

import (
	"context"
	"io"
	"testing"

	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
)

func TestPlaywrightUnsupportedBuildContract(t *testing.T) {
	vm := goja.New()
	obj, err := jsPlaywright(vm, log.New(io.Discard), context.Background)
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Set("playwright", obj); err != nil {
		t.Fatal(err)
	}
	value, err := vm.RunString(`playwright.get("https://example.test").error`)
	if err != nil || value.String() != "playwright is not supported in this build" {
		t.Fatalf("stub result=%v error=%v", value, err)
	}
}

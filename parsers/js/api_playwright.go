//go:build !no_jsparser && !no_playwright

package js

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
	"github.com/playwright-community/playwright-go"
)

// All plugin VMs install into the same directory. Cache successful installation
// only, so a transient failure can be retried by the next request.
type playwrightInstaller struct {
	gate  chan struct{}
	ready bool // accessed only while holding gate
}

func newPlaywrightInstaller() *playwrightInstaller {
	return &playwrightInstaller{gate: make(chan struct{}, 1)}
}

func (i *playwrightInstaller) ensure(ctx context.Context, install func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case i.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-i.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !i.ready {
		// Upstream has no context. Retain ownership until installation returns;
		// abandoning a goroutine would allow overlapping writes to the driver.
		if err := install(); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("failed to install playwright: %w", err)
		}
		i.ready = true
	}
	return ctx.Err()
}

var sharedPlaywrightInstaller = newPlaywrightInstaller()

// Small interfaces keep lifecycle tests independent of browser downloads.
type playwrightBackend interface {
	install() error
	run() (playwrightSession, error)
}
type playwrightSession interface {
	launch(timeout float64) (playwrightBrowser, error)
	stop() error
}
type playwrightBrowser interface {
	newPage() (playwrightPage, error)
	close() error
}
type playwrightPage interface {
	navigate(url string, timeout float64) (int, error)
	content() (string, error)
}

type nativePlaywrightBackend struct{}

func (nativePlaywrightBackend) install() error {
	// RunOptions.Logger changes an upstream package-global logger. Do not write
	// it from concurrent requests; application errors use the context logger.
	return playwright.Install(&playwright.RunOptions{Browsers: []string{"chromium"}, DriverDirectory: "./playwright"})
}
func (nativePlaywrightBackend) run() (playwrightSession, error) {
	pw, err := playwright.Run(&playwright.RunOptions{DriverDirectory: "./playwright"})
	if err != nil {
		return nil, err
	}
	return nativePlaywrightSession{pw}, nil
}

type nativePlaywrightSession struct{ pw *playwright.Playwright }

func (s nativePlaywrightSession) launch(timeout float64) (playwrightBrowser, error) {
	browser, err := s.pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{Timeout: playwright.Float(timeout)})
	if err != nil {
		return nil, err
	}
	return nativePlaywrightBrowser{browser}, nil
}
func (s nativePlaywrightSession) stop() error { return s.pw.Stop() }

type nativePlaywrightBrowser struct{ browser playwright.Browser }

func (b nativePlaywrightBrowser) newPage() (playwrightPage, error) {
	page, err := b.browser.NewPage()
	if err != nil {
		return nil, err
	}
	return nativePlaywrightPage{page}, nil
}
func (b nativePlaywrightBrowser) close() error { return b.browser.Close() }

type nativePlaywrightPage struct{ page playwright.Page }

func (p nativePlaywrightPage) navigate(url string, timeout float64) (int, error) {
	response, err := p.page.Goto(url, playwright.PageGotoOptions{
		WaitUntil: playwright.WaitUntilStateNetworkidle,
		Timeout:   playwright.Float(timeout),
	})
	if err != nil {
		return 0, err
	}
	if response == nil {
		return 0, nil
	}
	return response.Status(), nil
}
func (p nativePlaywrightPage) content() (string, error) { return p.page.Content() }

func playwrightTimeout(ctx context.Context, maximum time.Duration) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, context.DeadlineExceeded
		}
		maximum = min(maximum, remaining)
	}
	// Zero disables Playwright timeouts. Positive fractions must round up.
	return math.Ceil(float64(maximum) / float64(time.Millisecond)), nil
}

func fetchPlaywright(ctx context.Context, url string, logger *log.Logger, installer *playwrightInstaller, backend playwrightBackend) (content string, err error) {
	defer func() {
		// Native calls and cleanup may outlive cancellation. Never publish a late
		// success or let their errors hide the request's cancellation.
		if ctxErr := ctx.Err(); ctxErr != nil {
			content, err = "", ctxErr
		}
	}()
	if err := installer.ensure(ctx, backend.install); err != nil {
		return "", err
	}
	session, err := backend.run()
	if err != nil {
		return "", fmt.Errorf("failed to start playwright: %w", err)
	}
	cleanup := func(name string, close func() error) {
		if closeErr := close(); closeErr != nil {
			logger.Warnf("Failed to %s: %v", name, closeErr)
			err = errors.Join(err, fmt.Errorf("failed to %s: %w", name, closeErr))
		}
	}
	defer cleanup("stop playwright", session.stop)
	timeout, err := playwrightTimeout(ctx, 30*time.Second)
	if err != nil {
		return "", err
	}
	browser, err := session.launch(timeout)
	if err != nil {
		return "", fmt.Errorf("failed to launch browser: %w", err)
	}
	defer cleanup("close browser", browser.close)
	if err := ctx.Err(); err != nil {
		return "", err
	}
	page, err := browser.newPage()
	if err != nil {
		return "", fmt.Errorf("failed to create page: %w", err)
	}
	timeout, err = playwrightTimeout(ctx, 60*time.Second)
	if err != nil {
		return "", err
	}
	status, err := page.navigate(url, timeout)
	if err != nil {
		return "", fmt.Errorf("failed to navigate: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if status >= 400 {
		return "", fmt.Errorf("bad status code: %d", status)
	}
	content, err = page.content()
	if err != nil {
		return "", fmt.Errorf("failed to get page content: %w", err)
	}
	return content, nil
}

func jsPlaywright(vm *goja.Runtime, logger *log.Logger, requestContext func() context.Context) (*goja.Object, error) {
	return newJSPlaywright(vm, logger, requestContext, sharedPlaywrightInstaller, nativePlaywrightBackend{})
}

func newJSPlaywright(vm *goja.Runtime, logger *log.Logger, requestContext func() context.Context, installer *playwrightInstaller, backend playwrightBackend) (*goja.Object, error) {
	obj := vm.NewObject()
	err := obj.Set("get", func(call goja.FunctionCall) goja.Value {
		content, err := fetchPlaywright(requestContext(), call.Argument(0).String(), logger, installer, backend)
		if err != nil {
			return vm.ToValue(map[string]any{"error": err.Error()})
		}
		return vm.ToValue(content)
	})
	return obj, err
}

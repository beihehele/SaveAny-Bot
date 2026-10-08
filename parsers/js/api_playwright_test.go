//go:build !no_jsparser && !no_playwright

package js

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
)

func TestPlaywrightInstallationRetriesFailure(t *testing.T) {
	i := newPlaywrightInstaller()
	failure := errors.New("download failed")
	attempts := 0
	install := func() error {
		attempts++
		if attempts == 1 {
			return failure
		}
		return nil
	}
	if err := i.ensure(t.Context(), install); !errors.Is(err, failure) {
		t.Fatalf("first installation: %v", err)
	}
	for range 3 {
		if err := i.ensure(t.Context(), install); err != nil {
			t.Fatal(err)
		}
	}
	if attempts != 2 {
		t.Fatalf("install attempts=%d, want failed attempt and successful retry", attempts)
	}
}

func TestPlaywrightInstallationSharedAndCancelableWait(t *testing.T) {
	i := newPlaywrightInstaller()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	install := func() error {
		if calls.Add(1) != 1 {
			t.Error("overlapping or repeated successful installation")
		}
		close(started)
		<-release
		return nil
	}
	first := make(chan error, 1)
	go func() { first <- i.ensure(t.Context(), install) }()
	<-started
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	waiter := make(chan error, 1)
	go func() { waiter <- i.ensure(ctx, install) }()
	select {
	case err := <-waiter:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("waiting request: %v", err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("waiting request did not cancel while installation was blocked")
	}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if err := i.ensure(t.Context(), install); err != nil {
				t.Error(err)
			}
		})
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("installation calls=%d", calls.Load())
	}
}

type playwrightFixture struct {
	calls           []string
	failAt          string
	failure         error
	extraErrors     map[string]error
	before          func(string)
	cancelAt        string
	cancel          context.CancelFunc
	status          int
	url             string
	launchTimeout   float64
	navigateTimeout float64
}

func (f *playwrightFixture) step(name string) error {
	f.calls = append(f.calls, name)
	if f.before != nil {
		f.before(name)
	}
	if name == f.cancelAt {
		f.cancel()
	}
	if name == f.failAt {
		return f.failure
	}
	return f.extraErrors[name]
}

func (f *playwrightFixture) install() error { return f.step("install") }
func (f *playwrightFixture) run() (playwrightSession, error) {
	if err := f.step("run"); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *playwrightFixture) launch(timeout float64) (playwrightBrowser, error) {
	f.launchTimeout = timeout
	if err := f.step("launch"); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *playwrightFixture) stop() error { return f.step("stop") }
func (f *playwrightFixture) newPage() (playwrightPage, error) {
	if err := f.step("page"); err != nil {
		return nil, err
	}
	return f, nil
}
func (f *playwrightFixture) close() error { return f.step("close") }
func (f *playwrightFixture) navigate(url string, timeout float64) (int, error) {
	f.url, f.navigateTimeout = url, timeout
	return f.status, f.step("navigate")
}
func (f *playwrightFixture) content() (string, error) {
	return "<html>fixture</html>", f.step("content")
}

func fetchPlaywrightFixture(ctx context.Context, i *playwrightInstaller, f *playwrightFixture) (string, error) {
	return fetchPlaywright(ctx, "https://example.test/page", log.New(io.Discard), i, f)
}

func TestPlaywrightFailuresReleaseOwnedResources(t *testing.T) {
	for _, tc := range []struct {
		stage string
		calls []string
	}{
		{"install", []string{"install"}},
		{"run", []string{"install", "run"}},
		{"launch", []string{"install", "run", "launch", "stop"}},
		{"page", []string{"install", "run", "launch", "page", "close", "stop"}},
		{"navigate", []string{"install", "run", "launch", "page", "navigate", "close", "stop"}},
		{"content", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
		{"close", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
		{"stop", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			failure := errors.New("native failure")
			f := &playwrightFixture{failAt: tc.stage, failure: failure}
			_, err := fetchPlaywrightFixture(t.Context(), newPlaywrightInstaller(), f)
			if !errors.Is(err, failure) {
				t.Fatalf("error=%v", err)
			}
			if !reflect.DeepEqual(f.calls, tc.calls) {
				t.Fatalf("resource lifecycle=%v, want %v", f.calls, tc.calls)
			}
		})
	}
}

func TestPlaywrightCancellationStopsLaterStages(t *testing.T) {
	for _, tc := range []struct {
		stage string
		calls []string
	}{
		{"install", []string{"install"}},
		{"run", []string{"install", "run", "stop"}},
		{"launch", []string{"install", "run", "launch", "close", "stop"}},
		{"page", []string{"install", "run", "launch", "page", "close", "stop"}},
		{"navigate", []string{"install", "run", "launch", "page", "navigate", "close", "stop"}},
		{"content", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
		{"close", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
		{"stop", []string{"install", "run", "launch", "page", "navigate", "content", "close", "stop"}},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			f := &playwrightFixture{cancelAt: tc.stage, cancel: cancel, failAt: tc.stage, failure: errors.New("late native error")}
			// Installation and resource acquisition can succeed after cancellation;
			// verify that acquired resources are still released before returning.
			if tc.stage == "install" || tc.stage == "run" || tc.stage == "launch" {
				f.failAt = ""
			}
			content, err := fetchPlaywrightFixture(ctx, newPlaywrightInstaller(), f)
			if !errors.Is(err, context.Canceled) || content != "" {
				t.Fatalf("content=%q error=%v", content, err)
			}
			if !reflect.DeepEqual(f.calls, tc.calls) {
				t.Fatalf("calls after cancellation=%v, want %v", f.calls, tc.calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	f := &playwrightFixture{}
	_, err := fetchPlaywrightFixture(ctx, newPlaywrightInstaller(), f)
	if !errors.Is(err, context.Canceled) || len(f.calls) != 0 {
		t.Fatalf("already canceled request executed: %v, %v", f.calls, err)
	}
}

func TestPlaywrightCleanupPreservesAllErrors(t *testing.T) {
	navigationErr := errors.New("navigation failed")
	closeErr := errors.New("browser close failed")
	stopErr := errors.New("driver stop failed")
	f := &playwrightFixture{extraErrors: map[string]error{"navigate": navigationErr, "close": closeErr, "stop": stopErr}}
	_, err := fetchPlaywrightFixture(t.Context(), newPlaywrightInstaller(), f)
	for _, expected := range []error{navigationErr, closeErr, stopErr} {
		if !errors.Is(err, expected) {
			t.Fatalf("lost %v in %v", expected, err)
		}
	}
}

func TestPlaywrightCanceledSuccessfulInstallIsReusable(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	i := newPlaywrightInstaller()
	f := &playwrightFixture{cancelAt: "install", cancel: cancel}
	if _, err := fetchPlaywrightFixture(ctx, i, f); !errors.Is(err, context.Canceled) {
		t.Fatalf("late installation result: %v", err)
	}
	f.cancelAt = ""
	if _, err := fetchPlaywrightFixture(t.Context(), i, f); err != nil {
		t.Fatal(err)
	}
	installCalls := 0
	for _, call := range f.calls {
		if call == "install" {
			installCalls++
		}
	}
	if installCalls != 1 {
		t.Fatalf("successful installation repeated after cancellation: %v", f.calls)
	}
}

func TestPlaywrightSuccessStatusAndTimeouts(t *testing.T) {
	for _, status := range []int{0, 200, 204, 302, 399, 400, 503} {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		f := &playwrightFixture{status: status}
		content, err := fetchPlaywrightFixture(ctx, newPlaywrightInstaller(), f)
		cancel()
		if status >= 400 {
			if err == nil || !strings.Contains(err.Error(), "bad status code") || content != "" {
				t.Fatalf("status=%d content=%q error=%v", status, content, err)
			}
			if strings.Contains(strings.Join(f.calls, ","), "content") {
				t.Fatal("read content after HTTP error")
			}
		} else if err != nil || content != "<html>fixture</html>" {
			t.Fatalf("status=%d content=%q error=%v", status, content, err)
		}
		if f.url != "https://example.test/page" || f.launchTimeout <= 0 || f.launchTimeout > 2000 || f.navigateTimeout <= 0 || f.navigateTimeout > f.launchTimeout {
			t.Fatalf("URL/timeouts not bounded by request: %+v", f)
		}
	}
	for _, maximum := range []time.Duration{30 * time.Second, 60 * time.Second, time.Nanosecond} {
		actual, err := playwrightTimeout(t.Context(), maximum)
		if err != nil || actual < 1 || actual > max(1, float64(maximum/time.Millisecond)) {
			t.Fatalf("maximum=%v timeout=%v error=%v", maximum, actual, err)
		}
	}
}

func TestPlaywrightJSContractAndCanceledVMReuse(t *testing.T) {
	rt := newPluginRuntime(goja.New())
	f := &playwrightFixture{}
	i := newPlaywrightInstaller()
	obj, err := newJSPlaywright(rt.vm, log.New(io.Discard), rt.context, i, f)
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.vm.Set("playwright", obj); err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context) (any, error) {
		var result any
		err := rt.run(ctx, time.Second, func() error {
			value, err := rt.vm.RunString(`playwright.get("https://example.test/page")`)
			if err == nil {
				result = value.Export()
			}
			return err
		})
		return result, err
	}
	f.failAt, f.failure = "install", errors.New("initial download failed")
	value, err := run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	errorObject, ok := value.(map[string]any)
	if !ok || !strings.Contains(errorObject["error"].(string), "initial download failed") {
		t.Fatalf("JS error contract=%#v", value)
	}
	f.failAt = ""
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	f.cancelAt, f.cancel = "content", cancel
	if _, err := run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("native cancellation lost at VM boundary: %v", err)
	}
	f.cancelAt = ""
	value, err = run(t.Context())
	if err != nil || value != "<html>fixture</html>" {
		t.Fatalf("VM not reusable after native cancellation: %#v, %v", value, err)
	}
}

func TestPlaywrightBlockedNativeCallKeepsVMOwnership(t *testing.T) {
	rt := newPluginRuntime(goja.New())
	started, release := make(chan struct{}), make(chan struct{})
	f := &playwrightFixture{before: func(stage string) {
		if stage == "navigate" {
			close(started)
			<-release
		}
	}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- rt.run(ctx, time.Second, func() error {
			_, err := fetchPlaywrightFixture(rt.context(), newPlaywrightInstaller(), f)
			return err
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		close(release)
		t.Fatal("native call did not start")
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer waitCancel()
	entered := false
	err := rt.run(waitCtx, time.Second, func() error { entered = true; return nil })
	if !errors.Is(err, context.DeadlineExceeded) || entered {
		close(release)
		<-done
		t.Fatalf("VM released while native call was active: entered=%v err=%v", entered, err)
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("native completion hid cancellation: %v", err)
	}
	if err := rt.run(t.Context(), time.Second, func() error {
		_, err := rt.vm.RunString(`1 + 1`)
		return err
	}); err != nil {
		t.Fatalf("late native completion poisoned VM: %v", err)
	}
	if !reflect.DeepEqual(f.calls, []string{"install", "run", "launch", "page", "navigate", "close", "stop"}) {
		t.Fatalf("canceled native call continued later stages: %v", f.calls)
	}
}

//go:build !no_jsparser

package js

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/blang/semver"
	"github.com/charmbracelet/log"
	"github.com/dop251/goja"
	"github.com/krau/SaveAny-Bot/common/utils/netutil"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
)

func jsRegisterParser(vm *goja.Runtime, onError func(error)) func(call goja.FunctionCall) goja.Value {
	rt := newPluginRuntime(vm)
	return jsRegisterParserWithHandler(vm, onError, func(definition parserDefinition) {
		parsers.Add(newJSParser(rt, definition.canHandle, definition.parse, definition.metadata))
	})
}

func jsRegisterParserWithHandler(vm *goja.Runtime, onError func(error), register func(parserDefinition)) func(call goja.FunctionCall) goja.Value {
	reject := func(err error) goja.Value {
		if onError != nil {
			onError(err)
		}
		return vm.NewGoError(err)
	}
	return func(call goja.FunctionCall) goja.Value {
		jsObj := call.Argument(0)
		if jsObj == nil || goja.IsUndefined(jsObj) || goja.IsNull(jsObj) {
			return reject(errors.New("registerParser expects an object { canHandle, parse }"))
		}

		obj := jsObj.ToObject(vm)
		if obj == nil {
			return reject(errors.New("registerParser expects an object { canHandle, parse }"))
		}
		metaValue := obj.Get("metadata")
		if metaValue == nil || goja.IsUndefined(metaValue) {
			return reject(errors.New("parser must provide metadata"))
		}
		var metadata PluginMeta
		if exported := metaValue.Export(); exported != nil {
			data, err := json.Marshal(exported)
			if err != nil {
				return reject(fmt.Errorf("failed to marshal metadata to JSON: %w", err))
			}
			if err := json.Unmarshal(data, &metadata); err != nil {
				return reject(fmt.Errorf("failed to unmarshal JSON to PluginMeta: %w", err))
			}
		} else {
			return reject(errors.New("metadata cannot be null or undefined"))
		}

		pluginV, err := semver.Parse(metadata.Version)
		if err != nil {
			return reject(fmt.Errorf("invalid parser version %q: %w", metadata.Version, err))
		}
		if pluginV.LT(MinimumParserVersion) {
			return reject(fmt.Errorf("parser version %s is not supported, must be at least %s", metadata.Version, MinimumParserVersion))
		}
		if pluginV.Major > LatestParserVersion.Major {
			log.Printf("warning: parser major version %d is newer than latest supported major version %d", pluginV.Major, LatestParserVersion.Major)
		}

		handleFn := obj.Get("canHandle")
		parseFn := obj.Get("parse")
		if _, ok := goja.AssertFunction(parseFn); !ok {
			return reject(errors.New("parser must provide a parse function"))
		}
		if _, ok := goja.AssertFunction(handleFn); !ok {
			return reject(errors.New("parser must provide a canHandle function"))
		}
		register(parserDefinition{canHandle: handleFn, parse: parseFn, metadata: metadata})
		return goja.Undefined()
	}
}

var jsConsole = func(logger *log.Logger) map[string]any {
	return map[string]any{
		"log": func(args ...any) {
			if len(args) == 0 {
				return
			}
			if len(args) > 1 {
				logger.Info(args[0], args[1:]...)
			} else {
				logger.Info(args[0])
			}
		},
		"error": func(args ...any) {
			if len(args) == 0 {
				return
			}
			if len(args) > 1 {
				logger.Error(fmt.Sprint(args[0]), args[1:]...)
			} else {
				logger.Error(fmt.Sprint(args[0]))
			}
		},
	}
}

/*
jsGhttp provides a http helper for js plugins

It provides the following functions:
  - get(url): performs a GET request and returns the response body as string
  - getJSON(url): performs a GET request and returns the response body parsed as JSON
  - head(url): performs a HEAD request and returns the response headers and status code
*/
var jsGhttp = func(vm *goja.Runtime, currentContext func() context.Context) *goja.Object {
	ghttp := vm.NewObject()
	client := netutil.DefaultParserHTTPClient()
	request := func(method, url string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(currentContext(), method, url, nil)
		if err != nil {
			return nil, err
		}
		return client.Do(req)
	}
	ghttp.Set("get", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		resp, err := request(http.MethodGet, url)
		if err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Sprintf("failed to fetch %s: %v", url, err),
			})
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return vm.ToValue(map[string]any{
				"error":  fmt.Sprintf("failed to fetch %s: %s", url, resp.Status),
				"status": resp.StatusCode,
			})
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Errorf("failed to read response body: %w", err).Error(),
			})
		}
		return vm.ToValue(string(body))
	})
	ghttp.Set("getJSON", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()

		resp, err := request(http.MethodGet, url)
		if err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Sprintf("failed to fetch %s: %v", url, err),
			})
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return vm.ToValue(map[string]any{
				"error":  fmt.Sprintf("failed to fetch %s: %s", url, resp.Status),
				"status": resp.StatusCode,
			})
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Errorf("failed to read response body: %w", err).Error(),
			})
		}
		var jsonData map[string]any
		if err := json.Unmarshal(body, &jsonData); err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Errorf("failed to unmarshal JSON: %w", err).Error(),
			})
		}
		return vm.ToValue(map[string]any{
			"data": jsonData,
		})
	})
	ghttp.Set("head", func(call goja.FunctionCall) goja.Value {
		url := call.Argument(0).String()
		resp, err := request(http.MethodHead, url)
		if err != nil {
			return vm.ToValue(map[string]any{
				"error": fmt.Sprintf("failed to fetch %s: %v", url, err),
			})
		}
		defer resp.Body.Close()
		headers := make(map[string]string)
		for k, v := range resp.Header {
			headers[k] = v[0]
		}
		return vm.ToValue(map[string]any{
			"status":  resp.StatusCode,
			"headers": headers,
		})
	})
	return ghttp
}

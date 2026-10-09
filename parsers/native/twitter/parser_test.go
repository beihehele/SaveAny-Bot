package twitter

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/krau/SaveAny-Bot/common/utils/netutil"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func twitterFixture() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"code":200,"tweet":{"media":{"all":[{"url":"https://fixture.invalid/file.mp4","type":"video"}]}}}`))}
}

func TestParseCancelsMediaMetadataRequest(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "deadline"}[deadline], func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			want := context.Canceled
			if deadline {
				cancel()
				ctx, cancel = context.WithTimeout(t.Context(), time.Second)
				want = context.DeadlineExceeded
			}
			defer cancel()
			started := make(chan struct{})
			p := &TwitterParser{apiDomain: fxTwitterApi, client: http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method == http.MethodGet {
					return twitterFixture(), nil
				}
				close(started)
				<-r.Context().Done()
				return nil, r.Context().Err()
			})}}
			done := make(chan error, 1)
			go func() {
				_, err := p.Parse(ctx, "https://x.com/fixture/status/123")
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("HEAD request did not start")
			}
			if !deadline {
				cancel()
			}
			select {
			case err := <-done:
				if !errors.Is(err, want) {
					t.Fatalf("Parse = %v, want %v", err, want)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("metadata request ignored cancellation")
			}
		})
	}
}

func TestParseMediaMetadataRemainsOptional(t *testing.T) {
	for _, failed := range []bool{false, true} {
		p := &TwitterParser{apiDomain: fxTwitterApi, client: http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Method == http.MethodGet {
				return twitterFixture(), nil
			}
			if failed {
				return nil, errors.New("metadata unavailable")
			}
			return &http.Response{StatusCode: http.StatusOK, ContentLength: 1234, Body: http.NoBody}, nil
		})}}
		item, err := p.Parse(t.Context(), "https://x.com/fixture/status/123")
		want := int64(1234)
		if failed {
			want = 0
		}
		if err != nil || len(item.Resources) != 1 || item.Resources[0].Size != want || item.Resources[0].Filename != "file.mp4" {
			t.Fatalf("optional metadata: item=%+v err=%v", item, err)
		}
	}
}

func TestConfigureUsesDefaultClientWithoutExplicitProxy(t *testing.T) {
	defaultClient := netutil.DefaultParserHTTPClient()
	for _, config := range []map[string]any{nil, {}, {"api_domain": "custom.invalid"}} {
		p := &TwitterParser{client: http.Client{Timeout: time.Second}}
		if err := p.Configure(config); err != nil {
			t.Fatal(err)
		}
		wantDomain := fxTwitterApi
		if domain, ok := config["api_domain"].(string); ok {
			wantDomain = domain
		}
		if p.client.Transport != defaultClient.Transport || p.client.Timeout != defaultClient.Timeout || p.apiDomain != wantDomain {
			t.Fatal("default HTTP client or configured domain was lost")
		}
	}
}

func TestParseRejectsMetadataSuccessAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	p := &TwitterParser{apiDomain: fxTwitterApi, client: http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet {
			return twitterFixture(), nil
		}
		cancel()
		return &http.Response{StatusCode: http.StatusOK, ContentLength: 1234, Body: http.NoBody}, nil
	})}}
	if item, err := p.Parse(ctx, "https://x.com/fixture/status/123"); item != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled parse reported success: item=%+v err=%v", item, err)
	}
}

func TestConfigureExplicitProxy(t *testing.T) {
	p := &TwitterParser{}
	if err := p.Configure(map[string]any{"proxy": "http://localhost:12345", "api_domain": "custom.invalid"}); err != nil {
		t.Fatal(err)
	}
	transport, ok := p.client.Transport.(*http.Transport)
	if !ok || transport.Proxy == nil {
		t.Fatal("explicit proxy was not configured")
	}
	proxy, err := transport.Proxy(&http.Request{})
	if err != nil || proxy.String() != "http://localhost:12345" || p.apiDomain != "custom.invalid" {
		t.Fatalf("proxy/domain override: proxy=%v domain=%s err=%v", proxy, p.apiDomain, err)
	}
}

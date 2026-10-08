package kemono

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/krau/SaveAny-Bot/common/utils/netutil"
)

type responseTransport func(*http.Request) (*http.Response, error)

func (f responseTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestParseOneSkipsIncompleteAttachments(t *testing.T) {
	client := netutil.DefaultParserHTTPClient()
	previous := client.Transport
	t.Cleanup(func() { client.Transport = previous })
	client.Transport = responseTransport(func(r *http.Request) (*http.Response, error) {
		body := `{"post":{"title":"valid post","user":"author","attachments":[{}, {"name":"missing.jpg"}, {"path":"/missing-name.jpg"}, {"name":"ok.jpg","path":"/ok.jpg"}]},"previews":[{"type":"thumbnail"},{"type":"thumbnail","path":"/missing-server.jpg"},{"type":"thumbnail","server":"https://cdn.example.test"},{"type":"thumbnail","path":"/ok.jpg","server":"https://cdn.example.test"}]}`
		if r.Method == http.MethodHead {
			if r.URL.String() != "https://cdn.example.test/data/ok.jpg" {
				t.Errorf("unexpected resource URL: %s", r.URL)
			}
			body = ""
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), ContentLength: 42, Header: make(http.Header)}, nil
	})
	item, err := new(KemonoParser).parseOne(context.Background(), &DownloadInfo{ServiceName: "service", UserID: "user", PostID: "post"})
	if err != nil {
		t.Fatal(err)
	}
	if item.Title != "valid post" || len(item.Resources) != 1 || item.Resources[0].Filename != "ok.jpg" || item.Resources[0].Size != 42 {
		t.Fatalf("valid resource was not preserved: %+v", item)
	}
}

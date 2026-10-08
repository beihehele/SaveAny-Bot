package parsers

import (
	"context"
	"errors"
	"testing"

	registry "github.com/krau/SaveAny-Bot/parsers/parsers"
	"github.com/krau/SaveAny-Bot/pkg/parser"
)

type cancellingMatchParser struct {
	url    string
	cancel context.CancelFunc
	match  bool
}

func (p *cancellingMatchParser) CanHandle(url string) bool {
	return p.CanHandleContext(context.Background(), url)
}

func (p *cancellingMatchParser) CanHandleContext(_ context.Context, url string) bool {
	if url != p.url {
		return false
	}
	p.cancel()
	return p.match
}

func (p *cancellingMatchParser) Parse(context.Context, string) (*parser.Item, error) {
	return nil, errors.New("cancelled matching must not start parsing")
}

func TestSelectionRejectsMatchCancelledDuringMatching(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	url := "https://cancelled-matching.invalid/select"
	group := registry.NewGroup(t.Name())
	group.Replace(&cancellingMatchParser{url: url, cancel: cancel, match: true})
	t.Cleanup(func() { group.Replace() })
	if ok, p := CanHandleWithContext(ctx, url); ok || p != nil {
		t.Fatal("cancelled request selected a parser")
	}
}

func TestParsePreservesCancellationDuringMatching(t *testing.T) {
	url := "https://cancelled-matching.invalid/parse"
	group := registry.NewGroup(t.Name())
	t.Cleanup(func() { group.Replace() })
	// Both result channels can be ready after matching cancels the request.
	// Exercise that scheduling boundary repeatedly, without network calls.
	for range 100 {
		ctx, cancel := context.WithCancel(t.Context())
		group.Replace(&cancellingMatchParser{url: url, cancel: cancel})
		_, err := ParseWithContext(ctx, url)
		cancel()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("matching cancellation became %v", err)
		}
	}
}

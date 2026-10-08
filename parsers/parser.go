package parsers

import (
	"context"
	"fmt"

	"github.com/krau/SaveAny-Bot/parsers/js"
	"github.com/krau/SaveAny-Bot/parsers/native/kemono"
	"github.com/krau/SaveAny-Bot/parsers/native/twitter"
	"github.com/krau/SaveAny-Bot/parsers/parsers"
	"github.com/krau/SaveAny-Bot/pkg/parser"
)

func init() {
	parsers.Add(new(twitter.TwitterParser), new(kemono.KemonoParser))
}

var (
	ErrNoParserFound = fmt.Errorf("no parser found for the given URL")
)

func ParseWithContext(ctx context.Context, url string) (*parser.Item, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ch := make(chan *parser.Item, 1)
	errCh := make(chan error, 1)

	go func() {
		for _, pser := range parsers.Get() {
			if !parser.CanHandleWithContext(ctx, pser, url) {
				continue
			}
			item, err := pser.Parse(ctx, url)
			if err != nil {
				errCh <- err
				return
			}
			ch <- item
			return
		}
		if err := ctx.Err(); err != nil {
			errCh <- err
		} else {
			errCh <- ErrNoParserFound
		}
	}()

	select {
	case item := <-ch:
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return item, nil
	case err := <-errCh:
		if cancelled := ctx.Err(); cancelled != nil {
			return nil, cancelled
		}
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CanHandle checks if any registered parser can handle the given URL and returns the parser if found.
func CanHandle(url string) (bool, parser.Parser) {
	return CanHandleWithContext(context.Background(), url)
}

// CanHandleWithContext selects a parser using request-scoped matching when available.
func CanHandleWithContext(ctx context.Context, url string) (bool, parser.Parser) {
	for _, pser := range parsers.Get() {
		if parser.CanHandleWithContext(ctx, pser, url) {
			return true, pser
		}
	}
	return false, nil
}

func LoadPlugins(ctx context.Context, dir string) error {
	return js.LoadPlugins(ctx, dir)
}

func AddPlugin(ctx context.Context, code string, name string) error {
	return js.AddPlugin(ctx, code, name)
}

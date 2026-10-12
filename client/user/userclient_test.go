package user

import (
	"sync"
	"testing"

	"github.com/celestix/gotgproto"
	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
)

func TestConcurrentFirstGetCtxReturnsOneSharedContext(t *testing.T) {
	oldClient, oldContext := uc, ectx
	t.Cleanup(func() { clientMu.Lock(); defer clientMu.Unlock(); uc, ectx = oldClient, oldContext })
	uc, ectx = nil, nil
	if GetCtx() != nil {
		t.Fatal("unavailable client must not be cached")
	}
	uc = &gotgproto.Client{Client: telegram.NewClient(1, "test", telegram.Options{}), Self: &tg.User{ID: 9000}}
	var wg sync.WaitGroup
	results := make(chan *ext.Context, 64)
	for range 64 {
		wg.Go(func() { results <- GetCtx() })
	}
	wg.Wait()
	close(results)
	var first *ext.Context
	for result := range results {
		if first == nil {
			first = result
		}
		if result == nil || result != first || result.Self != uc.Self {
			t.Fatal("first access created multiple/missing contexts")
		}
	}
}

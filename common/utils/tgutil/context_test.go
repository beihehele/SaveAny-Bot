package tgutil

import (
	"context"
	"testing"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
)

func TestClientWithContextKeepsSharedClientAlive(t *testing.T) {
	service := t.Context()
	original := &ext.Context{Context: service, Self: &tg.User{ID: 123}}
	ctx, cancel := context.WithCancel(service)
	derived := ClientWithContext(ctx, original)
	cancel()
	if derived == original || derived.Self != original.Self || derived.Err() != context.Canceled || original.Err() != nil {
		t.Fatal("task context mutated or detached from client")
	}
	if ClientWithContext(ctx, nil) != nil {
		t.Fatal("nil client should remain unavailable")
	}
}

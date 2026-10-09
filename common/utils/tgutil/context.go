package tgutil

import (
	"context"

	"github.com/celestix/gotgproto/ext"
)

type contextKey struct{}

var extKey = contextKey{}

func ExtFromContext(ctx context.Context) *ext.Context {
	if extCtx, ok := ctx.Value(extKey).(*ext.Context); ok {
		return extCtx
	}
	return nil
}

func ExtWithContext(ctx context.Context, extCtx *ext.Context) context.Context {
	return context.WithValue(ctx, extKey, extCtx)
}

// ClientWithContext preserves client dependencies while deriving RPC lifetime.
// The shared client context is never modified.
func ClientWithContext(ctx context.Context, client *ext.Context) *ext.Context {
	if client == nil {
		return nil
	}
	derived := *client
	derived.Context = ctx
	return &derived
}

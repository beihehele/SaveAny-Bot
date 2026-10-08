package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/celestix/gotgproto/dispatcher"
	"github.com/celestix/gotgproto/ext"
	"github.com/celestix/gotgproto/types"
	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/config"
)

// Regression: callback queries usually arrive as updateShort without entity
// maps, so resolving the sender through the entity map yields ID 0 and every
// click was denied by the whitelist check. Callback updates must use the
// native UserID field.
func TestResponsibleUserID(t *testing.T) {
	tests := []struct {
		name   string
		update *ext.Update
		want   int64
	}{
		{
			name:   "callback query uses native user id",
			update: &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{UserID: 42}},
			want:   42,
		},
		{
			name: "message resolves through entity map",
			update: &ext.Update{
				EffectiveMessage: &types.Message{Message: &tg.Message{PeerID: &tg.PeerUser{UserID: 7}}},
				Entities:         &tg.Entities{Users: map[int64]*tg.User{7: {ID: 7}}},
			},
			want: 7,
		},
		{
			name: "callback query ignores entity map",
			update: &ext.Update{
				CallbackQuery: &tg.UpdateBotCallbackQuery{UserID: 9},
				Entities:      &tg.Entities{Users: map[int64]*tg.User{8: {ID: 8}}},
			},
			want: 9,
		},
		{
			name:   "unresolvable update yields zero",
			update: &ext.Update{},
			want:   0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responsibleUserID(tt.update); got != tt.want {
				t.Fatalf("responsibleUserID() = %d, want %d", got, tt.want)
			}
		})
	}
}

type callbackInvoker struct {
	answer *tg.MessagesSetBotCallbackAnswerRequest
}

func (i *callbackInvoker) Invoke(_ context.Context, input bin.Encoder, output bin.Decoder) error {
	answer, ok := input.(*tg.MessagesSetBotCallbackAnswerRequest)
	if !ok {
		return fmt.Errorf("unexpected request %T", input)
	}
	i.answer = answer
	result, ok := output.(*tg.BoolBox)
	if !ok {
		return fmt.Errorf("unexpected response %T", output)
	}
	result.Bool = &tg.BoolTrue{}
	return nil
}

func TestWithPermissionRejectsUnauthorizedCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[[users]]\nid = 42\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	invoker := &callbackInvoker{}
	ctx := &ext.Context{Context: t.Context(), Raw: tg.NewClient(invoker)}
	called := false
	handler := withPermission(func(*ext.Context, *ext.Update) error {
		called = true
		return nil
	})
	update := &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{UserID: 99, QueryID: 123}}
	if err := handler(ctx, update); !errors.Is(err, dispatcher.EndGroups) {
		t.Fatalf("got %v, want EndGroups", err)
	}
	if called {
		t.Fatal("unauthorized callback reached the handler")
	}
	if invoker.answer == nil || invoker.answer.QueryID != 123 || !invoker.answer.Alert {
		t.Fatalf("expected a callback alert for query 123, got %#v", invoker.answer)
	}
}

func TestMalformedCallbackData(t *testing.T) {
	for name, handler := range map[string]func(*ext.Context, *ext.Update) error{
		"add": handleAddCallback, "cancel": handleCancelCallback, "default": handleSetDefaultCallback,
	} {
		for _, data := range []string{"", name, name + " "} {
			t.Run(name+"/"+data, func(t *testing.T) {
				update := &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{Data: []byte(data)}}
				if err := handler(&ext.Context{}, update); err == nil {
					t.Fatal("expected malformed callback to be rejected")
				}
			})
		}
	}
}

// Regression: withPermission must treat ContinueGroups (the dispatcher's
// success sentinel) as a pass and invoke the wrapped handler. v0.60.1 treated
// it as an error, so every permitted callback was swallowed before the real
// handler ran.
func TestWithPermissionInvokesHandler(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("workers = 2\n\n[[users]]\nid = 42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := config.Init(t.Context(), path); err != nil {
		t.Fatal(err)
	}

	update := &ext.Update{CallbackQuery: &tg.UpdateBotCallbackQuery{UserID: 42}}
	called := false
	handler := withPermission(func(ctx *ext.Context, u *ext.Update) error {
		called = true
		return nil
	})
	if err := handler(&ext.Context{}, update); err != nil {
		t.Fatalf("withPermission returned error: %v", err)
	}
	if !called {
		t.Fatal("withPermission did not invoke the wrapped handler")
	}
}

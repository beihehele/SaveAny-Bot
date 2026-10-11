package tgutil

import (
	"context"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
)

// ProgressReplyTimeout bounds best-effort Telegram progress feedback.
const ProgressReplyTimeout = 5 * time.Second

// ProgressClient returns a request-local client for progress feedback. Normal
// updates follow task cancellation; terminal updates get a bounded opportunity
// to report the outcome after cancellation. It never cancels the shared client.
func ProgressClient(ctx context.Context, terminal bool) (*ext.Context, context.CancelFunc) {
	client := ExtFromContext(ctx)
	if client == nil {
		return nil, func() {}
	}
	if terminal {
		ctx = context.WithoutCancel(ctx)
	}
	ctx, cancel := context.WithTimeout(ctx, ProgressReplyTimeout)
	return ClientWithContext(ctx, client), cancel
}

// EditProgress updates best-effort task feedback without holding a worker
// indefinitely when Telegram is unavailable.
func EditProgress(ctx context.Context, chatID int64, request *tg.MessagesEditMessageRequest, terminal bool) {
	client, cancel := ProgressClient(ctx, terminal)
	defer cancel()
	if client == nil {
		return
	}
	if terminal {
		copy := *request
		copy.SetReplyMarkup(&tg.ReplyInlineMarkup{})
		request = &copy
	}
	_, err := client.EditMessage(chatID, request)
	if terminal && tgerr.Is(err, "REPLY_MARKUP_INVALID") {
		// Keep the terminal text and styling even if empty markup is rejected.
		// The fallback shares the original deadline rather than starting over.
		fallback := &tg.MessagesEditMessageRequest{ID: request.ID, Peer: request.Peer}
		fallback.SetMessage(request.Message)
		fallback.SetEntities(request.Entities)
		_, err = client.EditMessage(chatID, fallback)
	}
	if err != nil && !tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
		log.FromContext(ctx).Debug("Failed to edit progress message", "error", err)
	}
}

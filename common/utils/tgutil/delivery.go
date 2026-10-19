package tgutil

import (
	"context"
	"sync"

	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

// WithDeliveryFeedback reuses the existing progress message and cancel action.
func WithDeliveryFeedback(ctx context.Context, id string, progress any) context.Context {
	destination, ok := progress.(interface{ UploadDestination() (int64, int) })
	if !ok {
		return ctx
	}
	chat, message := destination.UploadDestination()
	var mu sync.Mutex
	last := ""
	return taskevent.WithSink(ctx, taskevent.SinkFunc(func(event taskevent.Event) {
		if event.DeliveryProgress == nil {
			return
		}
		stage := event.DeliveryProgress.Stage
		mu.Lock()
		defer mu.Unlock()
		if stage == last {
			return
		}
		last = stage
		request := &tg.MessagesEditMessageRequest{ID: message}
		request.SetMessage(i18n.T(i18nk.BotMsgProgressCollectionStage, map[string]any{"Stage": CollectionStage(stage)}))
		request.SetEntities(nil)
		request.SetReplyMarkup(&tg.ReplyInlineMarkup{Rows: []tg.KeyboardButtonRow{{Buttons: []tg.KeyboardButtonClass{BuildCancelButton(id)}}}})
		EditProgress(ctx, chat, request, false)
	}))
}

// CollectionStage translates the stages emitted by Telegram collection storage.
func CollectionStage(stage string) string {
	keys := map[string]i18nk.Key{"copying": i18nk.BotMsgProgressCollectionCopying, "downloading": i18nk.BotMsgProgressCollectionDownloading,
		"uploading": i18nk.BotMsgProgressCollectionUploading, "sending": i18nk.BotMsgProgressCollectionSending, "sent": i18nk.BotMsgProgressCollectionSent}
	return i18n.T(keys[stage])
}

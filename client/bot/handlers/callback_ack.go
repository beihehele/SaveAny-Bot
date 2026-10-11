package handlers

import (
	"context"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"

	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
)

const callbackAckTimeout = 5 * time.Second

// acknowledgeCallback stops the button spinner after validation. Delivery
// failure must not prevent an otherwise valid action or resubmit its task.
func acknowledgeCallback(ctx *ext.Context, queryID int64) {
	ackCtx, cancel := context.WithTimeout(ctx, callbackAckTimeout)
	defer cancel()
	client := tgutil.ClientWithContext(ackCtx, ctx)
	if _, err := client.AnswerCallback(&tg.MessagesSetBotCallbackAnswerRequest{QueryID: queryID}); err != nil {
		log.FromContext(ctx).Warn("Failed to acknowledge callback", "error", err)
	}
}

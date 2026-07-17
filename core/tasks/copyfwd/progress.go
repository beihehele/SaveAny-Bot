package copyfwd

import (
	"context"
	"errors"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
)

type ProgressTracker interface {
	OnScan(ctx context.Context, matched, count, atMsgID int)
	OnForward(ctx context.Context, done, total int)
	OnDone(ctx context.Context, forwarded, failed int, err error)
}

type Progress struct {
	MessageID int
	ChatID    int64
	TaskID    string
}

func NewProgressTracker(messageID int, chatID int64, taskID string) ProgressTracker {
	return &Progress{
		MessageID: messageID,
		ChatID:    chatID,
		TaskID:    taskID,
	}
}

func (p *Progress) OnScan(ctx context.Context, matched, count, atMsgID int) {
	text := i18n.T(i18nk.BotMsgProgressCopyScanning, map[string]any{
		"Matched": matched,
		"Count":   count,
		"At":      atMsgID,
	})
	p.edit(ctx, text, true)
}

func (p *Progress) OnForward(ctx context.Context, done, total int) {
	text := i18n.T(i18nk.BotMsgProgressCopyForwarding, map[string]any{
		"Done":  done,
		"Total": total,
	})
	p.edit(ctx, text, true)
}

func (p *Progress) OnDone(ctx context.Context, forwarded, failed int, err error) {
	var text string
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		text = i18n.T(i18nk.BotMsgProgressCopyCanceled)
	case err != nil:
		text = i18n.T(i18nk.BotMsgProgressTaskFailedWithError, map[string]any{"Error": err.Error()})
		log.FromContext(ctx).Errorf("Copy task %s failed: %v", p.TaskID, err)
	default:
		text = i18n.T(i18nk.BotMsgProgressCopyDone, map[string]any{
			"OK":   forwarded,
			"Fail": failed,
		})
	}
	p.edit(ctx, text, false)
}

func (p *Progress) edit(ctx context.Context, text string, withCancel bool) {
	if p.MessageID == 0 || p.ChatID == 0 {
		return
	}
	req := &tg.MessagesEditMessageRequest{
		ID: p.MessageID,
	}
	req.SetMessage(text)
	if withCancel && p.TaskID != "" {
		req.SetReplyMarkup(&tg.ReplyInlineMarkup{
			Rows: []tg.KeyboardButtonRow{
				{
					Buttons: []tg.KeyboardButtonClass{
						tgutil.BuildCancelButton(p.TaskID),
					},
				},
			},
		})
	}
	ext := tgutil.ExtFromContext(ctx)
	if ext == nil {
		return
	}
	if _, err := ext.EditMessage(p.ChatID, req); err != nil {
		log.FromContext(ctx).Debugf("edit copy progress message: %v", err)
	}
}

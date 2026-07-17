package copyfwd

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
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
	// Temporary format; Task 6 will wire i18n (progress.copy_scanning).
	text := fmt.Sprintf("扫描中 %d/%d（msg#%d）\nScanning %d/%d (msg#%d)", matched, count, atMsgID, matched, count, atMsgID)
	p.edit(ctx, text, true)
}

func (p *Progress) OnForward(ctx context.Context, done, total int) {
	// Temporary format; Task 6 will wire i18n (progress.copy_forwarding).
	text := fmt.Sprintf("转发中 %d/%d\nForwarding %d/%d", done, total, done, total)
	p.edit(ctx, text, true)
}

func (p *Progress) OnDone(ctx context.Context, forwarded, failed int, err error) {
	var text string
	switch {
	case err != nil && errors.Is(err, context.Canceled):
		// Temporary; Task 6 will wire i18n (progress.copy_canceled).
		text = "已取消 copy 任务\nCopy task canceled"
	case err != nil:
		text = fmt.Sprintf("copy 失败: %s\nCopy failed: %s", err.Error(), err.Error())
		log.FromContext(ctx).Errorf("Copy task %s failed: %v", p.TaskID, err)
	default:
		// Temporary; Task 6 will wire i18n (progress.copy_done).
		text = fmt.Sprintf("完成：成功 %d，失败 %d\nDone: ok %d, fail %d", forwarded, failed, forwarded, failed)
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

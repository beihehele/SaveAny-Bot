package copyfwd

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/krau/SaveAny-Bot/common/i18n"
	"github.com/krau/SaveAny-Bot/common/i18n/i18nk"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/pkg/taskresult"
)

const progressMinInterval = 2 * time.Second

type ProgressTracker interface {
	OnScan(ctx context.Context, matched, count, atMsgID int)
	OnScanDone(ctx context.Context, matched, count, atMsgID int)
	OnForward(ctx context.Context, done, total int, counts taskresult.Counts)
	OnDone(ctx context.Context, counts taskresult.Counts, err error)
}

type Progress struct {
	MessageID       int
	ChatID          int64
	TaskID          string
	Title           string
	Requested       int
	matched         int
	lastEdit        atomic.Int64 // unix nano of last edit
	forwardAnnounce atomic.Bool  // one-shot first forward update
}

func NewProgressTracker(messageID int, chatID int64, taskID, title string, requested int) ProgressTracker {
	return &Progress{
		MessageID: messageID,
		ChatID:    chatID,
		TaskID:    taskID,
		Title:     title,
		Requested: requested,
	}
}

func (p *Progress) OnScan(ctx context.Context, matched, count, atMsgID int) {
	p.matched = matched
	text := i18n.T(i18nk.BotMsgProgressCopyScanning, map[string]any{
		"Matched": matched,
		"Count":   count,
		"At":      atMsgID,
	})
	// The final scan update and phase switch are forced separately.
	p.edit(ctx, text, true, false)
}

func (p *Progress) OnScanDone(ctx context.Context, matched, count, atMsgID int) {
	p.matched = matched
	text := i18n.T(i18nk.BotMsgProgressCopyScanning, map[string]any{
		"Matched": matched,
		"Count":   count,
		"At":      atMsgID,
	})
	p.edit(ctx, text, true, true)
}

func (p *Progress) OnForward(ctx context.Context, done, total int, counts taskresult.Counts) {
	text := i18n.T(i18nk.BotMsgProgressCopyForwarding, map[string]any{
		"Done":  done,
		"Total": total,
		"OK":    counts.Succeeded, "Fail": counts.Failed, "Percent": done * 100 / max(1, total),
	})
	// Force phase switch (done=0), first real progress, and the final batch.
	force := done == 0 || done == total || !p.forwardAnnounce.Swap(true)
	p.edit(ctx, text, true, force)
}

func (p *Progress) OnDone(ctx context.Context, counts taskresult.Counts, err error) {
	var text string
	switch {
	case taskresult.ClassifyOutcome(ctx, err) == taskresult.OutcomeCancelled:
		text = i18n.T(i18nk.BotMsgProgressCopyCanceled)
	case err != nil:
		text = i18n.T(i18nk.BotMsgProgressTaskFailedWithError, map[string]any{"Error": err.Error()})
		log.FromContext(ctx).Errorf("Copy task %s failed: %v", p.TaskID, err)
	default:
		text = i18n.T(i18nk.BotMsgProgressCopyDone, map[string]any{
			"OK":   counts.Succeeded,
			"Fail": counts.Failed,
		})
	}
	if err == nil && p.matched == 0 {
		text = i18n.T(i18nk.BotMsgCopyNoMatches)
	}
	if p.Requested > 0 {
		text += "\n" + i18n.T(i18nk.BotMsgCopySelection, map[string]any{"Requested": p.Requested, "Matched": p.matched})
		if err != nil {
			text += "\n" + i18n.T(i18nk.BotMsgCopyCounts, map[string]any{"OK": counts.Succeeded, "Fail": counts.Failed, "Pending": counts.Pending + counts.Cancelled + counts.Interrupted})
		}
	}
	p.edit(ctx, text, false, true)
}

func (p *Progress) edit(ctx context.Context, text string, withCancel bool, force bool) {
	if p.MessageID == 0 || p.ChatID == 0 {
		return
	}
	if !force {
		last := p.lastEdit.Load()
		if last > 0 && time.Since(time.Unix(0, last)) < progressMinInterval {
			return
		}
	}
	p.lastEdit.Store(time.Now().UnixNano())

	req := &tg.MessagesEditMessageRequest{
		ID: p.MessageID,
	}
	if p.Title != "" {
		text = p.Title + "\n\n" + text
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
	} else {
		// Empty inline keyboard clears the cancel button (nil Rows, not Rows: []).
		req.SetReplyMarkup(&tg.ReplyInlineMarkup{})
	}
	ext, cancel := tgutil.ProgressClient(ctx, !withCancel)
	defer cancel()
	if ext == nil {
		return
	}
	if _, err := ext.EditMessage(p.ChatID, req); err != nil {
		if tgerr.Is(err, "MESSAGE_NOT_MODIFIED") {
			return
		}
		// Some clients reject empty markup; still apply the terminal text.
		if !withCancel && tgerr.Is(err, "REPLY_MARKUP_INVALID") {
			retry := &tg.MessagesEditMessageRequest{ID: p.MessageID}
			retry.SetMessage(text)
			if _, err2 := ext.EditMessage(p.ChatID, retry); err2 != nil && !tgerr.Is(err2, "MESSAGE_NOT_MODIFIED") {
				log.FromContext(ctx).Debugf("edit copy progress message: %v", err2)
			}
			return
		}
		log.FromContext(ctx).Debugf("edit copy progress message: %v", err)
	}
}

package copyfwd

import "context"

type ProgressTracker interface {
	OnScan(ctx context.Context, matched, count, atMsgID int)
	OnForward(ctx context.Context, done, total int)
	OnDone(ctx context.Context, forwarded, failed int, err error)
}

type Progress struct {
	MessageID int
	ChatID    int64
}

func NewProgressTracker(messageID int, chatID int64) ProgressTracker {
	return &Progress{
		MessageID: messageID,
		ChatID:    chatID,
	}
}

func (p *Progress) OnScan(ctx context.Context, matched, count, atMsgID int) {}

func (p *Progress) OnForward(ctx context.Context, done, total int) {}

func (p *Progress) OnDone(ctx context.Context, forwarded, failed int, err error) {}

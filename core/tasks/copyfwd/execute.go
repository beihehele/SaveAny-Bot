package copyfwd

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/user"
)

// Execute searches source history, then forwards matched messages (DropAuthor) with [转] link.
// Album hits are already deduped in collect (one id per grouped_id); ForwardMessage expands the group.
func (t *Task) Execute(ctx context.Context) error {
	defer End(t.UserChatID, t.ID)

	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("copyfwd[%s]", t.ID))
	logger.Info("Starting copy-forward task")

	var (
		execErr error
		copied  int
		failed  int
	)
	defer func() {
		if t.Progress != nil {
			t.Progress.OnDone(ctx, copied, failed, execErr)
		}
	}()

	uctx := user.GetCtx()
	if uctx == nil {
		execErr = fmt.Errorf("userbot unavailable")
		return execErr
	}

	var onProgress scanProgressFn
	if t.Progress != nil {
		onProgress = func(matched, atMsgID int) {
			t.Progress.OnScan(ctx, matched, t.Count, atMsgID)
		}
	}
	ids, err := collectMatchedIDs(ctx, uctx, t.SourceID, t.Filter, t.Count, onProgress)
	if err != nil {
		execErr = fmt.Errorf("collect messages: %w", err)
		return execErr
	}
	if t.Progress != nil {
		at := 0
		if len(ids) > 0 {
			at = ids[0]
		}
		t.Progress.OnScan(ctx, len(ids), t.Count, at)
	}

	logger.Infof("Scan done: matched=%d want=%d", len(ids), t.Count)

	total := len(ids)
	for i, msgID := range ids {
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}
		if err := user.ForwardMessage(uctx, t.SourceID, t.TargetID, msgID, t.TargetTopicID); err != nil {
			logger.Errorf("Forward msg %d failed: %v", msgID, err)
			failed++
		} else {
			copied++
		}
		if t.Progress != nil {
			t.Progress.OnForward(ctx, i+1, total)
		}
	}

	logger.Infof("Copy-forward done: ok=%d fail=%d", copied, failed)
	return nil
}

package copyfwd

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/user"
)

// Pace forwards to reduce FLOOD_WAIT (each item is forward + caption edit).
const forwardGap = 500 * time.Millisecond

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
		// Force final scan line even when matched < want (history exhausted early).
		t.Progress.OnScanDone(ctx, len(ids), t.Count, at)
	}

	logger.Infof("Scan done: matched=%d want=%d", len(ids), t.Count)

	total := len(ids)
	if total == 0 {
		return nil
	}
	// Switch UI off "scanning" before the first forward (may block on FLOOD_WAIT / album fetch).
	if t.Progress != nil {
		t.Progress.OnForward(ctx, 0, total)
	}
	logger.Infof("Forwarding %d message(s) to %d", total, t.TargetID)

	for i, msgID := range ids {
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}
		if i > 0 {
			select {
			case <-ctx.Done():
				execErr = ctx.Err()
				return execErr
			case <-time.After(forwardGap):
			}
		}
		logger.Debugf("Forwarding %d/%d msg=%d", i+1, total, msgID)
		started := time.Now()
		if err := user.ForwardMessage(ctx, uctx, t.SourceID, t.TargetID, msgID, t.TargetTopicID); err != nil {
			logger.Errorf("Forward msg %d failed: %v", msgID, err)
			failed++
		} else {
			copied++
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			logger.Warnf("Forward msg %d took %s (rate limit or slow RPC)", msgID, elapsed.Round(time.Millisecond))
		}
		if t.Progress != nil {
			t.Progress.OnForward(ctx, i+1, total)
		}
	}

	logger.Infof("Copy-forward done: ok=%d fail=%d", copied, failed)
	return nil
}

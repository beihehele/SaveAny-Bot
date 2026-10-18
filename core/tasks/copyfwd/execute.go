package copyfwd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/pkg/taskevent"
)

// Pace forwards to reduce FLOOD_WAIT (each item is forward + caption edit).
const forwardGap = 500 * time.Millisecond

// Execute searches source history, then forwards matched messages (DropAuthor) with [转] link.
// Album hits are already deduped in collect (one id per grouped_id); ForwardMessage expands the group.
func (t *Task) Execute(ctx context.Context) error {
	return t.executeWithClient(ctx, user.GetCtx)
}

func (t *Task) executeWithClient(ctx context.Context, client func() *ext.Context) error {
	defer End(t.UserChatID, t.ID)
	t.resetResults(0)

	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("copyfwd[%s]", t.ID))
	logger.Info("Starting copy-forward task")

	var execErr error
	defer func() {
		if t.Progress != nil {
			t.Progress.OnDone(ctx, t.ResultSummary().Counts(), execErr)
		}
	}()

	uctx := client()
	if uctx == nil {
		execErr = fmt.Errorf("userbot unavailable")
		return execErr
	}

	onProgress := func(matched, atMsgID int) {
		t.emitProgress(ctx, "scanning", matched, 0)
		if t.Progress != nil {
			t.Progress.OnScan(ctx, matched, t.Count, atMsgID)
		}
	}
	onProgress(0, 0)
	ids, err := collectMatchedIDs(ctx, uctx, t.SourceID, t.Filter, t.Count, onProgress)
	if err != nil {
		execErr = fmt.Errorf("collect messages: %w", err)
		return execErr
	}
	t.resetResults(len(ids))
	t.emitProgress(ctx, "scanning", len(ids), 0)
	if t.Progress != nil {
		at := 0
		if len(ids) > 0 {
			at = ids[0]
		}
		// Force final scan line even when matched < want (history exhausted early).
		t.Progress.OnScanDone(ctx, len(ids), t.Count, at)
	}
	if err := ctx.Err(); err != nil {
		execErr = err
		return execErr
	}

	logger.Infof("Scan done: matched=%d want=%d", len(ids), t.Count)

	total := len(ids)
	if total == 0 {
		return nil
	}
	// Switch UI off "scanning" before the first forward (may block on FLOOD_WAIT / album fetch).
	t.emitProgress(ctx, "forwarding", total, 0)
	if t.Progress != nil {
		t.Progress.OnForward(ctx, 0, total, t.ResultSummary().Counts())
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
		t.startResult()
		err := user.ForwardMessage(ctx, uctx, t.SourceID, t.TargetID, msgID, t.TargetTopicID)
		t.finishResult(err, ctx.Err())
		if err != nil {
			if !errors.Is(ctx.Err(), context.Canceled) || !errors.Is(err, context.Canceled) {
				logger.Errorf("Forward msg %d failed: %v", msgID, err)
			}
		}
		if elapsed := time.Since(started); elapsed > 5*time.Second {
			logger.Warnf("Forward msg %d took %s (rate limit or slow RPC)", msgID, elapsed.Round(time.Millisecond))
		}
		t.emitProgress(ctx, "forwarding", total, i+1)
		if t.Progress != nil {
			t.Progress.OnForward(ctx, i+1, total, t.ResultSummary().Counts())
		}
		// There may be no next iteration to observe cancellation during the last
		// RPC or feedback callback. Keep confirmed counts, but preserve its cause.
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}
	}

	counts := t.ResultSummary().Counts()
	logger.Infof("Copy-forward done: ok=%d fail=%d", counts.Succeeded, counts.Failed)
	return nil
}

func (t *Task) emitProgress(ctx context.Context, stage string, matched, processed int) {
	counts := t.ResultSummary().Counts()
	taskevent.Emit(ctx, taskevent.Event{TaskID: t.ID, Phase: taskevent.PhaseProgress, ResultSummary: &counts,
		CopyProgress: &taskevent.CopyProgress{Stage: stage, Requested: t.Count, Matched: matched, Processed: processed}})
}

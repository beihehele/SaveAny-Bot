package copyfwd

import (
	"context"
	"fmt"

	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
)

const (
	windowSize   = 100
	maxContinue  = 5
	forwardBatch = 100
)

// Execute scans source history in windows, then forwards matched messages in batches.
func (t *Task) Execute(ctx context.Context) error {
	defer End(t.UserChatID, t.ID)

	logger := log.FromContext(ctx).WithPrefix(fmt.Sprintf("copyfwd[%s]", t.ID))
	logger.Info("Starting copy-forward task")

	var (
		execErr   error
		forwarded int
		failed    int
	)
	defer func() {
		if t.Progress != nil {
			t.Progress.OnDone(ctx, forwarded, failed, execErr)
		}
	}()

	uctx := user.GetCtx()
	if uctx == nil {
		execErr = fmt.Errorf("userbot unavailable")
		return execErr
	}

	maxID, err := user.GetLatestMessageID(uctx, t.SourceID)
	if err != nil {
		execErr = fmt.Errorf("get latest message id: %w", err)
		return execErr
	}

	var all []ScanMsg
	hi := maxID
	var ids []int
	var matched int

	for hi >= 1 {
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}

		lo := max(1, hi-windowSize+1)
		msgs, err := tgutil.GetMessagesRange(uctx, t.SourceID, lo, hi)
		if err != nil {
			execErr = fmt.Errorf("get messages range [%d,%d]: %w", lo, hi, err)
			return execErr
		}

		for _, m := range msgs {
			if m == nil {
				continue
			}
			all = append(all, scanMsgFromTG(m))
		}

		ids, matched = CollectForwardIDs(all, t.Filter, t.Count, maxContinue)
		if t.Progress != nil {
			t.Progress.OnScan(ctx, matched, t.Count, lo)
		}

		if matched >= t.Count || lo == 1 {
			break
		}
		hi = lo - 1
	}

	logger.Infof("Scan done: matched=%d forwardIDs=%d", matched, len(ids))

	total := len(ids)
	for i := 0; i < total; i += forwardBatch {
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}

		end := min(i+forwardBatch, total)
		batch := ids[i:end]
		if err := user.ForwardMessagesDropAuthor(uctx, t.SourceID, t.TargetID, batch, t.TargetTopicID); err != nil {
			logger.Errorf("Forward batch [%d:%d] failed: %v", i, end, err)
			failed += len(batch)
		} else {
			forwarded += len(batch)
		}
		if t.Progress != nil {
			t.Progress.OnForward(ctx, forwarded+failed, total)
		}
	}

	logger.Infof("Copy-forward done: ok=%d fail=%d", forwarded, failed)
	return nil
}

func scanMsgFromTG(m *tg.Message) ScanMsg {
	sm := ScanMsg{
		ID:   m.GetID(),
		Text: m.GetMessage(),
	}
	if gid, ok := m.GetGroupedID(); ok {
		sm.GroupedID = gid
		sm.HasGroup = true
	}
	return sm
}

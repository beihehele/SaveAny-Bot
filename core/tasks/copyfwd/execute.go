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
	windowSize      = 100
	maxContinue     = 5
	forwardBatch    = 100
	maxScanWindows  = 200 // soft cap: ~20k message IDs scanned
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
	meta := make(map[int]ScanMsg)
	hi := maxID
	var ids []int
	var matched int
	windows := 0

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
			sm := scanMsgFromTG(m)
			all = append(all, sm)
			meta[sm.ID] = sm
		}
		windows++

		ids, matched = CollectForwardIDs(all, t.Filter, t.Count, maxContinue)
		if t.Progress != nil {
			t.Progress.OnScan(ctx, matched, t.Count, lo)
		}

		if matched >= t.Count {
			// One more older window so albums/continuations that straddle
			// the stop boundary are still attached before we halt.
			if lo > 1 {
				if err := ctx.Err(); err != nil {
					execErr = err
					return execErr
				}
				extraLo := max(1, lo-windowSize)
				extraHi := lo - 1
				extra, err := tgutil.GetMessagesRange(uctx, t.SourceID, extraLo, extraHi)
				if err != nil {
					execErr = fmt.Errorf("get messages range [%d,%d]: %w", extraLo, extraHi, err)
					return execErr
				}
				for _, m := range extra {
					if m == nil {
						continue
					}
					sm := scanMsgFromTG(m)
					all = append(all, sm)
					meta[sm.ID] = sm
				}
				ids, matched = CollectForwardIDs(all, t.Filter, t.Count, maxContinue)
				if t.Progress != nil {
					t.Progress.OnScan(ctx, matched, t.Count, extraLo)
				}
			}
			break
		}
		if lo == 1 || windows >= maxScanWindows {
			break
		}
		hi = lo - 1
	}

	logger.Infof("Scan done: matched=%d forwardIDs=%d windows=%d", matched, len(ids), windows)

	batches := packForwardBatches(ids, meta, forwardBatch)
	total := len(ids)
	done := 0
	for _, batch := range batches {
		if err := ctx.Err(); err != nil {
			execErr = err
			return execErr
		}

		if err := user.ForwardMessagesDropAuthor(uctx, t.SourceID, t.TargetID, batch, t.TargetTopicID); err != nil {
			logger.Errorf("Forward batch ids=%v failed: %v", batch, err)
			failed += len(batch)
		} else {
			forwarded += len(batch)
		}
		done += len(batch)
		if t.Progress != nil {
			t.Progress.OnForward(ctx, done, total)
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

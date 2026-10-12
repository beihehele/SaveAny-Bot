package copyfwd

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/celestix/gotgproto/ext"
	"github.com/charmbracelet/log"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/common/utils/tgutil"
	"github.com/krau/SaveAny-Bot/config"
	"github.com/krau/SaveAny-Bot/pkg/msgfilter"
)

type scanProgressFn func(matched, atMsgID int)

func scanPage(ctx context.Context, uctx *ext.Context, sourceID int64, offsetID int, search bool, query string) (user.MessagePage, error) {
	timeout := config.C().Telegram.ScanTimeout
	if timeout <= 0 {
		timeout = config.DefaultTelegramScanTimeout
	}
	pageCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	client := tgutil.ClientWithContext(pageCtx, uctx)
	mode := "history"
	if search {
		mode = "search"
	}
	started := time.Now()
	logger := log.FromContext(ctx)
	logger.Debug("Copy scan page started", "mode", mode, "source", sourceID, "offset_id", offsetID, "timeout", timeout)
	var page user.MessagePage
	var err error
	if search {
		page, err = user.SearchMessages(client, sourceID, query, offsetID)
	} else {
		page, err = user.HistoryMessages(client, sourceID, offsetID)
	}
	if err == nil {
		err = pageCtx.Err()
	}
	if err == nil && page.RawCount > 0 && (page.NextOffsetID <= 0 || offsetID > 0 && page.NextOffsetID >= offsetID) {
		err = fmt.Errorf("pagination did not advance: next offset %d", page.NextOffsetID)
	}
	elapsed := time.Since(started)
	scanStats.record(search, elapsed, err)
	logger.Debug("Copy scan page finished", "mode", mode, "source", sourceID, "offset_id", offsetID, "messages", len(page.Messages), "raw_count", page.RawCount, "duration", elapsed, "error", err)
	if err != nil {
		return user.MessagePage{}, fmt.Errorf("copy %s page (source %d offset %d): %w", mode, sourceID, offsetID, err)
	}
	return page, nil
}

func collectMatchedIDs(ctx context.Context, uctx *ext.Context, sourceID int64, filter string, count int, onProgress scanProgressFn) ([]int, error) {
	uctx = tgutil.ClientWithContext(ctx, uctx)
	node, err := msgfilter.ParseFilter(filter)
	if err != nil {
		return nil, err
	}
	if filter == "" {
		return collectFromHistory(ctx, uctx, sourceID, count, nil, onProgress)
	}
	seeds := msgfilter.SearchSeeds(node)
	if len(seeds) == 0 || !msgfilter.SeedsCoverExpression(node) {
		return collectFromHistory(ctx, uctx, sourceID, count, node, onProgress)
	}
	return collectFromSearch(ctx, uctx, sourceID, count, node, seeds, onProgress)
}

func collectFromHistory(ctx context.Context, uctx *ext.Context, sourceID int64, count int, node msgfilter.Node, onProgress scanProgressFn) ([]int, error) {
	matched := make([]int, 0, count)
	groupRep := make(map[int64]int) // grouped_id -> representative msg id
	seen := make(map[int]struct{})
	offsetID := 0
	for len(matched) < count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		page, err := scanPage(ctx, uctx, sourceID, offsetID, false, "")
		if err != nil {
			return nil, err
		}
		if page.RawCount == 0 {
			break
		}
		for _, m := range page.Messages {
			if _, duplicate := seen[m.ID]; duplicate || m.ID <= 0 || offsetID > 0 && m.ID >= offsetID {
				continue
			}
			seen[m.ID] = struct{}{}
			if !matchesNode(node, m.GetMessage()) {
				continue
			}
			if !acceptHit(m, &matched, groupRep) {
				continue
			}
			if onProgress != nil {
				onProgress(len(matched), m.GetID())
			}
			if len(matched) >= count {
				break
			}
		}
		if page.RawCount < user.SearchPageSize() {
			break
		}
		offsetID = page.NextOffsetID
	}
	sort.Sort(sort.Reverse(sort.IntSlice(matched)))
	return matched, nil
}

func collectFromSearch(ctx context.Context, uctx *ext.Context, sourceID int64, count int, node msgfilter.Node, seeds []string, onProgress scanProgressFn) ([]int, error) {
	matched := make([]int, 0, count)
	seen := make(map[int]struct{})
	groupRep := make(map[int64]int)
	offsets := make(map[string]int, len(seeds))
	exhausted := make(map[string]bool, len(seeds))

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		progress := false
		for _, seed := range seeds {
			if exhausted[seed] {
				continue
			}
			page, err := scanPage(ctx, uctx, sourceID, offsets[seed], true, seed)
			if err != nil {
				return nil, err
			}
			if page.RawCount == 0 {
				exhausted[seed] = true
				continue
			}
			progress = true
			for _, m := range page.Messages {
				if m.ID <= 0 || offsets[seed] > 0 && m.ID >= offsets[seed] {
					continue
				}
				if _, ok := seen[m.GetID()]; ok {
					continue
				}
				if !msgfilter.Eval(node, m.GetMessage()) {
					continue
				}
				seen[m.GetID()] = struct{}{}
				acceptHit(m, &matched, groupRep)
			}
			if page.RawCount < user.SearchPageSize() {
				exhausted[seed] = true
			} else {
				offsets[seed] = page.NextOffsetID
			}
		}

		sort.Sort(sort.Reverse(sort.IntSlice(matched)))
		if len(matched) > count {
			matched = matched[:count]
		}
		if onProgress != nil {
			at := 0
			if len(matched) > 0 {
				at = matched[0]
			}
			onProgress(len(matched), at)
		}

		if len(matched) >= count {
			cutoff := matched[len(matched)-1]
			for _, seed := range seeds {
				if exhausted[seed] {
					continue
				}
				if off, ok := offsets[seed]; ok && off > 0 && off <= cutoff {
					exhausted[seed] = true
				}
			}
		}

		allDone := true
		for _, seed := range seeds {
			if !exhausted[seed] {
				allDone = false
				break
			}
		}
		if allDone || !progress {
			break
		}
		if len(matched) >= count {
			canImprove := false
			for _, seed := range seeds {
				if !exhausted[seed] {
					canImprove = true
					break
				}
			}
			if !canImprove {
				break
			}
		}
	}
	return matched, nil
}

// acceptHit records one logical hit. Same album (grouped_id) counts once;
// prefers a caption-bearing message as the representative id.
func acceptHit(m *tg.Message, matched *[]int, groupRep map[int64]int) bool {
	if gid, ok := m.GetGroupedID(); ok && gid != 0 {
		if oldID, seen := groupRep[gid]; seen {
			if strings.TrimSpace(m.GetMessage()) != "" {
				for i, id := range *matched {
					if id == oldID {
						(*matched)[i] = m.GetID()
						groupRep[gid] = m.GetID()
						break
					}
				}
			}
			return false
		}
		groupRep[gid] = m.GetID()
	}
	*matched = append(*matched, m.GetID())
	return true
}

func matchesNode(node msgfilter.Node, text string) bool {
	if node == nil {
		return true
	}
	return msgfilter.Eval(node, text)
}

package copyfwd

import (
	"context"
	"sort"
	"strings"

	"github.com/celestix/gotgproto/ext"
	"github.com/gotd/td/tg"
	"github.com/krau/SaveAny-Bot/client/user"
	"github.com/krau/SaveAny-Bot/pkg/msgfilter"
)

type scanProgressFn func(matched, atMsgID int)

func collectMatchedIDs(ctx context.Context, uctx *ext.Context, sourceID int64, filter string, count int, onProgress scanProgressFn) ([]int, error) {
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
	offsetID := 0
	for len(matched) < count {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		msgs, err := user.HistoryMessages(uctx, sourceID, offsetID)
		if err != nil {
			return nil, err
		}
		if len(msgs) == 0 {
			break
		}
		lastID := 0
		for _, m := range msgs {
			lastID = m.GetID()
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
		if len(msgs) < user.SearchPageSize() {
			break
		}
		offsetID = lastID
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
			msgs, err := user.SearchMessages(uctx, sourceID, seed, offsets[seed])
			if err != nil {
				return nil, err
			}
			if len(msgs) == 0 {
				exhausted[seed] = true
				continue
			}
			progress = true
			lastID := 0
			for _, m := range msgs {
				lastID = m.GetID()
				if _, ok := seen[m.GetID()]; ok {
					continue
				}
				if !msgfilter.Eval(node, m.GetMessage()) {
					continue
				}
				seen[m.GetID()] = struct{}{}
				acceptHit(m, &matched, groupRep)
			}
			if len(msgs) < user.SearchPageSize() {
				exhausted[seed] = true
			} else {
				offsets[seed] = lastID
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

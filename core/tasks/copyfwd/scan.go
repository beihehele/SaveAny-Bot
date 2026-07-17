package copyfwd

import (
	"sort"
	"strings"
)

// ScanMsg is a lightweight message view for copy-forward scanning.
type ScanMsg struct {
	ID        int
	Text      string
	GroupedID int64
	HasGroup  bool
}

type groupRange struct {
	groupedID int64
	ids       []int
	min       int
	max       int
}

// CollectForwardIDs scans buffered messages (high to low by ID), collects ids to
// forward when filter matches, including album groups and caption-less continuations.
func CollectForwardIDs(msgs []ScanMsg, filter string, count int, maxContinue int) (ids []int, matched int) {
	if len(msgs) == 0 {
		return nil, 0
	}

	byID := make(map[int]ScanMsg, len(msgs))
	allIDs := make([]int, 0, len(msgs))
	groups := make(map[int64]*groupRange)

	for _, m := range msgs {
		byID[m.ID] = m
		allIDs = append(allIDs, m.ID)
		if !m.HasGroup {
			continue
		}
		gr, ok := groups[m.GroupedID]
		if !ok {
			groups[m.GroupedID] = &groupRange{
				groupedID: m.GroupedID,
				ids:       []int{m.ID},
				min:       m.ID,
				max:       m.ID,
			}
			continue
		}
		gr.ids = append(gr.ids, m.ID)
		if m.ID < gr.min {
			gr.min = m.ID
		}
		if m.ID > gr.max {
			gr.max = m.ID
		}
	}

	sort.Sort(sort.Reverse(sort.IntSlice(allIDs)))

	collected := make(map[int]struct{})
	processedGroups := make(map[int64]struct{})
	seenGroups := make(map[int64]struct{})

	addIDs := func(toAdd ...int) {
		for _, id := range toAdd {
			collected[id] = struct{}{}
		}
	}

	for _, id := range allIDs {
		if matched >= count {
			break
		}
		m := byID[id]
		if m.HasGroup {
			if _, done := processedGroups[m.GroupedID]; done {
				continue
			}
			if _, seen := seenGroups[m.GroupedID]; seen {
				continue
			}
			seenGroups[m.GroupedID] = struct{}{}
			gr := groups[m.GroupedID]
			repText := albumRepresentativeText(byID, gr.ids)
			if repText == "" {
				continue
			}
			if !filterMatches(filter, repText) {
				continue
			}
			processedGroups[m.GroupedID] = struct{}{}
			matched++
			addIDs(gr.ids...)
			collectContinuations(gr, groups, byID, processedGroups, maxContinue, addIDs)
			continue
		}
		if !filterMatches(filter, m.Text) {
			continue
		}
		matched++
		addIDs(m.ID)
	}

	ids = make([]int, 0, len(collected))
	for id := range collected {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, matched
}

func albumRepresentativeText(byID map[int]ScanMsg, ids []int) string {
	for _, id := range ids {
		if t := strings.TrimSpace(byID[id].Text); t != "" {
			return t
		}
	}
	return ""
}

func groupAllNoCaption(byID map[int]ScanMsg, ids []int) bool {
	for _, id := range ids {
		if strings.TrimSpace(byID[id].Text) != "" {
			return false
		}
	}
	return true
}

func groupsContiguous(aMin, aMax, bMin, bMax int) bool {
	return bMin == aMax+1 || aMin == bMax+1
}

func collectContinuations(
	anchor *groupRange,
	groups map[int64]*groupRange,
	byID map[int]ScanMsg,
	processedGroups map[int64]struct{},
	maxContinue int,
	addIDs func(...int),
) {
	minID, maxID := anchor.min, anchor.max
	segments := 0

	for segments < maxContinue {
		expanded := false

		for _, gr := range groups {
			if _, done := processedGroups[gr.groupedID]; done {
				continue
			}
			if !groupsContiguous(minID, maxID, gr.min, gr.max) {
				continue
			}
			if gr.min != maxID+1 {
				continue
			}
			if !groupAllNoCaption(byID, gr.ids) {
				continue
			}
			processedGroups[gr.groupedID] = struct{}{}
			addIDs(gr.ids...)
			maxID = gr.max
			segments++
			expanded = true
			break
		}
		if segments >= maxContinue {
			break
		}

		for _, gr := range groups {
			if _, done := processedGroups[gr.groupedID]; done {
				continue
			}
			if !groupsContiguous(minID, maxID, gr.min, gr.max) {
				continue
			}
			if gr.max != minID-1 {
				continue
			}
			if !groupAllNoCaption(byID, gr.ids) {
				continue
			}
			processedGroups[gr.groupedID] = struct{}{}
			addIDs(gr.ids...)
			minID = gr.min
			segments++
			expanded = true
			break
		}

		if !expanded {
			break
		}
	}
}

package copyfwd

import (
	"reflect"
	"testing"
)

func TestCollectForwardIDs_PlainAndAlbum(t *testing.T) {
	msgs := []ScanMsg{
		{ID: 10, Text: "hello plana"},
		{ID: 20, Text: "cap", GroupedID: 1, HasGroup: true},
		{ID: 21, Text: "", GroupedID: 1, HasGroup: true},
		{ID: 22, Text: "", GroupedID: 1, HasGroup: true},
	}
	ids, matched := CollectForwardIDs(msgs, "msgre:plana", 10, 5)
	if matched != 1 || !reflect.DeepEqual(ids, []int{10}) {
		t.Fatalf("matched=%d ids=%v", matched, ids)
	}
	ids, matched = CollectForwardIDs(msgs, "msgre:cap", 10, 5)
	if matched != 1 || !reflect.DeepEqual(ids, []int{20, 21, 22}) {
		t.Fatalf("matched=%d ids=%v", matched, ids)
	}
}

func TestCollectForwardIDs_Continuation(t *testing.T) {
	msgs := []ScanMsg{
		{ID: 30, Text: "hit", GroupedID: 1, HasGroup: true},
		{ID: 31, Text: "", GroupedID: 1, HasGroup: true},
		{ID: 32, Text: "", GroupedID: 1, HasGroup: true},
		{ID: 33, Text: "", GroupedID: 2, HasGroup: true},
		{ID: 34, Text: "", GroupedID: 2, HasGroup: true},
		{ID: 35, Text: "", GroupedID: 2, HasGroup: true},
		{ID: 36, Text: "", GroupedID: 3, HasGroup: true},
		{ID: 37, Text: "", GroupedID: 3, HasGroup: true},
		{ID: 39, Text: "other"},
	}
	ids, matched := CollectForwardIDs(msgs, "msgre:hit", 1, 5)
	want := []int{30, 31, 32, 33, 34, 35, 36, 37}
	if matched != 1 || !reflect.DeepEqual(ids, want) {
		t.Fatalf("matched=%d ids=%v", matched, ids)
	}
}

func TestCollectForwardIDs_ContinuationStopsOnCaptionOrGap(t *testing.T) {
	msgs := []ScanMsg{
		{ID: 10, Text: "hit", GroupedID: 1, HasGroup: true},
		{ID: 11, Text: "", GroupedID: 1, HasGroup: true},
		{ID: 12, Text: "next cap", GroupedID: 2, HasGroup: true},
		{ID: 13, Text: "", GroupedID: 2, HasGroup: true},
	}
	ids, matched := CollectForwardIDs(msgs, "msgre:hit", 1, 5)
	if matched != 1 || !reflect.DeepEqual(ids, []int{10, 11}) {
		t.Fatalf("matched=%d ids=%v", matched, ids)
	}
}

func TestCollectForwardIDs_NoFilterAlbumCountsOnce(t *testing.T) {
	msgs := []ScanMsg{
		{ID: 1, Text: "a", GroupedID: 9, HasGroup: true},
		{ID: 2, Text: "", GroupedID: 9, HasGroup: true},
		{ID: 3, Text: "solo"},
	}
	ids, matched := CollectForwardIDs(msgs, "", 10, 5)
	if matched != 2 || !reflect.DeepEqual(ids, []int{1, 2, 3}) {
		t.Fatalf("matched=%d ids=%v", matched, ids)
	}
}

func TestCollectForwardIDs_ContinuationMax5(t *testing.T) {
	msgs := []ScanMsg{
		{ID: 100, Text: "hit", GroupedID: 1, HasGroup: true},
		{ID: 101, Text: "", GroupedID: 1, HasGroup: true},
	}
	id := 102
	for g := int64(2); g <= 8; g++ {
		msgs = append(msgs, ScanMsg{ID: id, Text: "", GroupedID: g, HasGroup: true})
		id++
		msgs = append(msgs, ScanMsg{ID: id, Text: "", GroupedID: g, HasGroup: true})
		id++
	}
	ids, matched := CollectForwardIDs(msgs, "msgre:hit", 1, 5)
	if matched != 1 || len(ids) != 12 {
		t.Fatalf("matched=%d len=%d ids=%v", matched, len(ids), ids)
	}
}

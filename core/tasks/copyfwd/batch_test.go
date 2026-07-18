package copyfwd

import (
	"reflect"
	"testing"
)

func TestPackForwardBatchesKeepsAlbumTogether(t *testing.T) {
	ids := []int{1, 2, 3, 10, 11, 12, 13}
	meta := map[int]ScanMsg{
		1:  {ID: 1, Text: "a"},
		2:  {ID: 2, Text: "b"},
		3:  {ID: 3, Text: "c"},
		10: {ID: 10, Text: "cap", GroupedID: 1, HasGroup: true},
		11: {ID: 11, GroupedID: 1, HasGroup: true},
		12: {ID: 12, GroupedID: 1, HasGroup: true},
		13: {ID: 13, Text: "solo"},
	}
	got := packForwardBatches(ids, meta, 3)
	// plain 1,2,3 fit in one batch of 3; album 10-12 must stay together → own batch; 13 alone
	want := [][]int{{1, 2, 3}, {10, 11, 12}, {13}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestPackForwardBatchesKeepsContinuationRun(t *testing.T) {
	ids := []int{10, 11, 12, 13}
	meta := map[int]ScanMsg{
		10: {ID: 10, Text: "cap", GroupedID: 1, HasGroup: true},
		11: {ID: 11, GroupedID: 1, HasGroup: true},
		12: {ID: 12, GroupedID: 2, HasGroup: true},
		13: {ID: 13, GroupedID: 2, HasGroup: true},
	}
	got := packForwardBatches(ids, meta, 3)
	// 4 IDs contiguous media run > maxBatch 3 → emit cluster alone
	want := [][]int{{10, 11, 12, 13}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

package taskresult

import (
	"math"
	"testing"
)

func TestCountsRejectNegativeInconsistentAndOverflowValues(t *testing.T) {
	for _, counts := range []Counts{
		{Total: -1}, {Total: 1, Failed: -1, Succeeded: 2}, {Total: 2, Succeeded: 1},
		{Total: math.MaxInt, Succeeded: math.MaxInt, Failed: math.MaxInt},
	} {
		if counts.Valid() {
			t.Fatalf("invalid counts accepted: %+v", counts)
		}
	}
	if !(Counts{}).Valid() || !(Counts{Total: 7, Pending: 1, Running: 1, Succeeded: 1, Failed: 1, Cancelled: 1, Interrupted: 2}).Valid() {
		t.Fatal("consistent counts rejected")
	}
}

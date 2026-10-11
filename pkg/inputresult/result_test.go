package inputresult

import "testing"

func TestReportBoundsSnapshotsAndDistinguishesPartialSelection(t *testing.T) {
	var report Report
	report.Add(1, "ready", 1)
	report.Add(2, "ready", 0)
	report.Add(3, "unsupported_media", 1)
	report.Add(4, "no_media", 0)
	want := []string{"ready", "duplicate", "partial", "failed"}
	for index, status := range want {
		if report.InputResults[index].Status != status {
			t.Fatalf("result %d: %+v", index, report.InputResults[index])
		}
	}
	clone := report.Clone()
	clone.InputResults[0].Code = "mutated"
	if report.InputResults[0].Code != "ready" {
		t.Fatal("snapshot aliases retained report")
	}
	for index := 5; index <= Limit+9; index++ {
		report.Add(index, "ready", 1)
	}
	if len(report.InputResults) != Limit || report.InputResultsOmitted != 9 {
		t.Fatal("metadata unbounded")
	}
}

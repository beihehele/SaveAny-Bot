package inputresult

import "testing"

func TestReportBoundsSnapshotsAndDistinguishesPartialSelection(t *testing.T) {
	var report Report
	report.Add(1, "ready", 1)
	report.AddWithAlbumFallback(2, "ready", 0)
	report.AddWithAlbumFallback(3, "unsupported_media", 1)
	report.AddWithAlbumFallback(4, "no_media", 0)
	want := []string{"ready", "duplicate", "partial", "failed"}
	for index, status := range want {
		if report.InputResults[index].Status != status {
			t.Fatalf("result %d: %+v", index, report.InputResults[index])
		}
	}
	clone := report.Clone()
	clone.InputResults[0].Code = "mutated"
	clone.InputResults[1].AlbumFallback = false
	if report.InputResults[0].Code != "ready" || report.InputResults[0].AlbumFallback || !report.InputResults[1].AlbumFallback || !report.InputResults[3].AlbumFallback {
		t.Fatal("snapshot aliases retained report")
	}
	for index := 5; index <= Limit+9; index++ {
		report.AddWithAlbumFallback(index, "ready", 1)
	}
	if len(report.InputResults) != Limit || report.InputResultsOmitted != 9 {
		t.Fatal("metadata unbounded")
	}
}

package copyfwd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

func TestScanStatsClassifiesFailuresWithoutLeakingOrSharingRecords(t *testing.T) {
	s := newScanStatsStore()
	s.record(false, 1500*time.Microsecond, nil)
	s.record(false, 2*time.Millisecond, fmt.Errorf("scan: %w", context.DeadlineExceeded))
	s.record(false, 3*time.Millisecond, context.Canceled)
	s.record(true, 4*time.Millisecond, errors.New("sensitive-source failure"))
	s.record(true, time.Millisecond, fmt.Errorf("RPC: %w", &net.OpError{Op: "read", Err: os.ErrDeadlineExceeded}))
	first := s.snapshot()
	if got := first[0]; got.Pages != 3 || got.Succeeded != 1 || got.Failed != 2 || got.TimedOut != 1 || got.Cancelled != 1 || got.LastDurationMS != 3 || got.MaxDurationMS != 3 || got.TotalDurationMS != 6.5 {
		t.Fatalf("invalid history statistics: %+v", got)
	}
	if got := first[1]; got.Pages != 2 || got.Failed != 2 || got.TimedOut != 1 || got.Cancelled != 0 {
		t.Fatalf("search failure misclassified: %+v", got)
	}
	first[0].Mode = "changed"
	first[0].Pages = 0
	if got := s.snapshot()[0]; got.Mode != "history" || got.Pages != 3 {
		t.Fatal("snapshot mutated stored observations")
	}
}

func TestScanStatsConcurrentRecordingAndSampling(t *testing.T) {
	s := newScanStatsStore()
	var wg sync.WaitGroup
	for index := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.record(index%2 == 1, time.Millisecond, nil)
				_ = s.snapshot()
			}
		}()
	}
	wg.Wait()
	for _, got := range s.snapshot() {
		if got.Pages != 400 || got.Succeeded != 400 || got.TotalDurationMS != 400 {
			t.Fatalf("lost concurrent observations: %+v", got)
		}
	}
}

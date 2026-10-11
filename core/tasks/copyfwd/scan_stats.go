package copyfwd

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"
)

// ScanStats summarizes completed scan pages since process start. It contains no
// chat IDs, message content, raw errors, or unbounded per-request records.
type ScanStats struct {
	Mode            string  `json:"mode"`
	Pages           uint64  `json:"pages"`
	Succeeded       uint64  `json:"succeeded"`
	Failed          uint64  `json:"failed"`
	TimedOut        uint64  `json:"timed_out"`
	Cancelled       uint64  `json:"cancelled"`
	LastDurationMS  float64 `json:"last_duration_ms"`
	MaxDurationMS   float64 `json:"max_duration_ms"`
	TotalDurationMS float64 `json:"total_duration_ms"`
}

type scanStatsStore struct {
	mu    sync.Mutex
	stats [2]ScanStats
}

func newScanStatsStore() *scanStatsStore {
	return &scanStatsStore{stats: [2]ScanStats{{Mode: "history"}, {Mode: "search"}}}
}

var scanStats = newScanStatsStore()

func (s *scanStatsStore) record(search bool, elapsed time.Duration, err error) {
	index := 0
	if search {
		index = 1
	}
	ms := float64(max(0, elapsed)) / float64(time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	stats := &s.stats[index]
	stats.Pages++
	stats.LastDurationMS = ms
	stats.MaxDurationMS = max(stats.MaxDurationMS, ms)
	stats.TotalDurationMS += ms
	if err == nil {
		stats.Succeeded++
	} else {
		stats.Failed++
		var timeoutError net.Error
		if errors.Is(err, context.Canceled) {
			stats.Cancelled++
		} else if errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &timeoutError) && timeoutError.Timeout()) {
			stats.TimedOut++
		}
	}
}

func (s *scanStatsStore) snapshot() []ScanStats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return []ScanStats{s.stats[0], s.stats[1]}
}

// GetScanStats returns detached, fixed-size observations. It performs no RPC
// and does not change scan deadlines, cancellation, ordering, or forwarding.
func GetScanStats() []ScanStats { return scanStats.snapshot() }

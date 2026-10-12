package msgfilter

import (
	"fmt"
	"sync"
	"testing"
)

func TestFilterCacheRemainsBoundedThroughConcurrentRouteChanges(t *testing.T) {
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			for id := range 100 {
				value := fmt.Sprintf("msgre:word-%d-%d", worker, id)
				if !Match(value, fmt.Sprintf("word-%d-%d", worker, id)) {
					t.Errorf("valid filter mismatch: %s", value)
				}
				if Match("msgre:("+value, "anything") {
					t.Error("invalid expression matched")
				}
			}
		})
	}
	wg.Wait()
	parsedFilterCache.Lock()
	defer parsedFilterCache.Unlock()
	if len(parsedFilterCache.entries) > filterCacheLimit || len(parsedFilterCache.entries) != len(parsedFilterCache.order) {
		t.Fatalf("unbounded cache: %d %d", len(parsedFilterCache.entries), len(parsedFilterCache.order))
	}
	for _, key := range parsedFilterCache.order {
		if _, ok := parsedFilterCache.entries[key]; !ok {
			t.Fatal("eviction left stale cache key")
		}
	}
}

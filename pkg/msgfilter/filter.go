package msgfilter

import (
	"fmt"
	"strings"
	"sync"
)

const Prefix = "msgre:"

type cachedFilter struct {
	node Node
	err  error
}

const filterCacheLimit = 256

// Entries include invalid expressions and are bounded across route changes.
var parsedFilterCache = struct {
	sync.Mutex
	entries map[string]cachedFilter
	order   []string
}{entries: make(map[string]cachedFilter)}

// ParseFilter parses a stored filter string (msgre:...). Empty filter matches all.
func ParseFilter(filter string) (Node, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return nil, nil
	}
	if !strings.HasPrefix(filter, Prefix) {
		return nil, fmt.Errorf("filter must start with %s", Prefix)
	}
	expr := strings.TrimSpace(filter[len(Prefix):])
	if expr == "" {
		return nil, errInvalidExpr
	}
	return Parse(expr)
}

// Match reports whether text satisfies the filter string.
// Parsed ASTs are cached by filter string for hot paths like /watch.
func Match(filter, text string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	parsedFilterCache.Lock()
	c, ok := parsedFilterCache.entries[filter]
	parsedFilterCache.Unlock()
	if ok {
		if c.err != nil || c.node == nil {
			return false
		}
		return Eval(c.node, text)
	}
	node, err := ParseFilter(filter)
	parsedFilterCache.Lock()
	if _, exists := parsedFilterCache.entries[filter]; !exists {
		if len(parsedFilterCache.order) == filterCacheLimit {
			delete(parsedFilterCache.entries, parsedFilterCache.order[0])
			copy(parsedFilterCache.order, parsedFilterCache.order[1:])
			parsedFilterCache.order = parsedFilterCache.order[:len(parsedFilterCache.order)-1]
		}
		parsedFilterCache.order = append(parsedFilterCache.order, filter)
		parsedFilterCache.entries[filter] = cachedFilter{node: node, err: err}
	}
	parsedFilterCache.Unlock()
	if err != nil || node == nil {
		return false
	}
	return Eval(node, text)
}

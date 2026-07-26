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

var parsedFilterCache sync.Map // string -> cachedFilter

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
	if v, ok := parsedFilterCache.Load(filter); ok {
		c := v.(cachedFilter)
		if c.err != nil || c.node == nil {
			return false
		}
		return Eval(c.node, text)
	}
	node, err := ParseFilter(filter)
	parsedFilterCache.Store(filter, cachedFilter{node: node, err: err})
	if err != nil || node == nil {
		return false
	}
	return Eval(node, text)
}

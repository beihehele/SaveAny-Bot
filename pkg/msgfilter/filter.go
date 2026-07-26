package msgfilter

import (
	"fmt"
	"strings"
)

const Prefix = "msgre:"

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
func Match(filter, text string) bool {
	if strings.TrimSpace(filter) == "" {
		return true
	}
	node, err := ParseFilter(filter)
	if err != nil {
		return false
	}
	return Eval(node, text)
}

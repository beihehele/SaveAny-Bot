package msgfilter

import "strings"

// SearchSeeds returns distinct keywords for messages.search (positive terms only).
func SearchSeeds(node Node) []string {
	if node == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	add := func(s string) {
		if s == "" {
			return
		}
		key := strings.ToLower(s)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, s)
	}
	for _, s := range seedsFromNode(node) {
		add(s)
	}
	return out
}

// SeedsCoverExpression reports whether SearchSeeds can discover every matchable
// message. Pure-NOT OR branches (e.g. !spam|vip) are not coverable by search.
func SeedsCoverExpression(node Node) bool {
	if node == nil {
		return true
	}
	switch n := node.(type) {
	case *Term:
		return true
	case *Not:
		return false
	case *And:
		return andSearchSeed(n) != ""
	case *Or:
		if len(n.Children) == 0 {
			return false
		}
		for _, ch := range n.Children {
			if !SeedsCoverExpression(ch) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func seedsFromNode(node Node) []string {
	switch n := node.(type) {
	case *Term:
		return []string{n.Value}
	case *Not:
		return nil
	case *And:
		if s := andSearchSeed(n); s != "" {
			return []string{s}
		}
		return nil
	case *Or:
		var out []string
		for _, ch := range n.Children {
			out = append(out, branchSearchSeed(ch)...)
		}
		return out
	default:
		return nil
	}
}

func andSearchSeed(n *And) string {
	for _, ch := range n.Children {
		if _, ok := ch.(*Not); ok {
			continue
		}
		if t, ok := ch.(*Term); ok {
			return t.Value
		}
		if _, ok := ch.(*Or); ok {
			continue
		}
		if a, ok := ch.(*And); ok {
			if s := andSearchSeed(a); s != "" {
				return s
			}
		}
	}
	// No single positive term is required by every branch. Searching only one
	// alternative of (a|b)&(c|d), for example, would miss b&c. Scan history instead.
	return ""
}

func branchSearchSeed(node Node) []string {
	switch n := node.(type) {
	case *Term:
		return []string{n.Value}
	case *Not:
		return nil
	case *And:
		if s := andSearchSeed(n); s != "" {
			return []string{s}
		}
		return nil
	case *Or:
		var out []string
		for _, ch := range n.Children {
			out = append(out, branchSearchSeed(ch)...)
		}
		return out
	default:
		return nil
	}
}

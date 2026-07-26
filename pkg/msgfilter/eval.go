package msgfilter

import "strings"

// Eval evaluates the expression against text (case-insensitive substring match).
func Eval(node Node, text string) bool {
	if node == nil {
		return true
	}
	switch n := node.(type) {
	case *Term:
		return containsFold(text, n.Value)
	case *Not:
		return !Eval(n.Child, text)
	case *And:
		for _, ch := range n.Children {
			if !Eval(ch, text) {
				return false
			}
		}
		return true
	case *Or:
		for _, ch := range n.Children {
			if Eval(ch, text) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func containsFold(s, substr string) bool {
	if substr == "" {
		return true
	}
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

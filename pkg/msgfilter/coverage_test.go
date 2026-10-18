package msgfilter

import (
	"strings"
	"testing"
)

func TestSearchSeedsCoverEveryMatchingAlternative(t *testing.T) {
	for _, expr := range []string{"a&(b|c)", "(a|b)&(c|d)", "(a|b)&!c", "((a|b)&(c|d))|a", "!a|b", "a&!(b|c)", "(a|b)&c", "(a&b)|(c&d)", "!a&(b|c)"} {
		node, err := Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		if !SeedsCoverExpression(node) {
			continue
		}
		seeds := SearchSeeds(node)
		for mask := 0; mask < 16; mask++ {
			var terms []string
			for i, term := range []string{"a", "b", "c", "d"} {
				if mask&(1<<i) != 0 {
					terms = append(terms, term)
				}
			}
			text := strings.Join(terms, " ")
			if !Eval(node, text) {
				continue
			}
			covered := false
			for _, seed := range seeds {
				covered = covered || containsFold(text, seed)
			}
			if !covered {
				t.Fatalf("%s matches %q but seeds %v miss it", expr, text, seeds)
			}
		}
	}
}

func TestChineseCombinedExpression(t *testing.T) {
	node, err := Parse("舞团&(king8|k8)")
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]bool{"舞团 KING8": true, "k8 舞团": true, "只有舞团": false, "king8": false} {
		if Eval(node, text) != want {
			t.Fatalf("text=%q", text)
		}
	}
	if seeds := SearchSeeds(node); len(seeds) != 1 || seeds[0] != "舞团" {
		t.Fatalf("unexpected seed %v", seeds)
	}
}

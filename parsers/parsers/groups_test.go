package parsers

import (
	"context"
	"slices"
	"testing"

	"github.com/krau/SaveAny-Bot/pkg/parser"
)

// A parser value may contain a slice; registry ownership must not assume that
// every Parser interface value can be compared or used as a map key.
type groupTestParser struct{ names []string }

func (p groupTestParser) CanHandle(string) bool { return true }
func (p groupTestParser) Parse(context.Context, string) (*parser.Item, error) {
	return &parser.Item{Title: p.names[0]}, nil
}

func TestGroupReplacementKeepsOtherParsersAndRejectsRetiredRegistration(t *testing.T) {
	mu.Lock()
	oldParsers, oldGroups := parsers, groups
	parsers = nil
	groups = make(map[string]*Group)
	mu.Unlock()
	t.Cleanup(func() { mu.Lock(); parsers, groups = oldParsers, oldGroups; mu.Unlock() })
	p := func(name string) parser.Parser { return groupTestParser{names: []string{name}} }
	Add(p("native"))
	first := NewGroup("a.js")
	first.Replace(p("a-v1"))
	other := NewGroup("b.js")
	other.Replace(p("b"))
	if !first.Add(p("a-later")) {
		t.Fatal("current group rejected registration")
	}
	second := NewGroup("a.js")
	retired := second.Replace(p("a-v2"), p("a-extra"))
	if len(retired) != 2 {
		t.Fatalf("retired=%d", len(retired))
	}
	if first.Add(p("stale")) {
		t.Fatal("replaced group appended stale parser")
	}
	var names []string
	for _, registered := range Get() {
		item, err := registered.Parse(t.Context(), "")
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, item.Title)
	}
	if !slices.Equal(names, []string{"native", "a-v2", "a-extra", "b"}) {
		t.Fatalf("order=%v", names)
	}
	NewGroup("a.js").Replace()
	if len(Get()) != 2 {
		t.Fatal("empty replacement retained old parsers")
	}
}

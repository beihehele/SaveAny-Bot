package msgfilter

import (
	"reflect"
	"testing"
)

func TestParseAndEval(t *testing.T) {
	node, err := Parse("plana&(planb|planc|pland)")
	if err != nil {
		t.Fatal(err)
	}
	if !Eval(node, "hello plana world planb") {
		t.Fatal("expected match")
	}
	if Eval(node, "only plana") {
		t.Fatal("expected no match without planb/planc/pland")
	}
	if Eval(node, "only planb") {
		t.Fatal("expected no match without plana")
	}
}

func TestParseOr(t *testing.T) {
	node, err := Parse("A|B|C|D")
	if err != nil {
		t.Fatal(err)
	}
	if !Eval(node, "xxx B yyy") {
		t.Fatal("expected B match")
	}
	if Eval(node, "zzz") {
		t.Fatal("expected no match")
	}
}

func TestParseNot(t *testing.T) {
	node, err := Parse("A&B&(!C)")
	if err != nil {
		t.Fatal(err)
	}
	if !Eval(node, "A and B ok") {
		t.Fatal("expected match")
	}
	if Eval(node, "A B C") {
		t.Fatal("expected no match with C")
	}
}

func TestCaseFold(t *testing.T) {
	node, err := Parse("Plana")
	if err != nil {
		t.Fatal(err)
	}
	if !Eval(node, "PLANA") {
		t.Fatal("expected case-insensitive match")
	}
}

func TestSearchSeeds(t *testing.T) {
	n, err := Parse("A&(B|C|D)")
	if err != nil {
		t.Fatal(err)
	}
	got := SearchSeeds(n)
	if !reflect.DeepEqual(got, []string{"A"}) {
		t.Fatalf("got %v want [A]", got)
	}
	n, err = Parse("A|B|C|D")
	if err != nil {
		t.Fatal(err)
	}
	got = SearchSeeds(n)
	want := []string{"A", "B", "C", "D"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSeedsCoverExpression(t *testing.T) {
	cases := []struct {
		expr string
		ok   bool
	}{
		{"plana&(planb|planc)", true},
		{"plana|planb|planc", true},
		{"plana&(!spam)", true},
		{"!spam|vip", false},
		{"!spam", false},
		{"!a&!b", false},
		{"vip|(!spam&plana)", true},
		{"vip|(!spam)", false},
	}
	for _, tt := range cases {
		n, err := Parse(tt.expr)
		if err != nil {
			t.Fatalf("parse %q: %v", tt.expr, err)
		}
		if got := SeedsCoverExpression(n); got != tt.ok {
			t.Fatalf("%q SeedsCoverExpression=%v want %v", tt.expr, got, tt.ok)
		}
	}
}

func TestMatchFilter(t *testing.T) {
	if !Match("msgre:hello", "say hello") {
		t.Fatal("expected match")
	}
	if Match("msgre:hello", "world") {
		t.Fatal("expected no match")
	}
}

func TestQuotedTerm(t *testing.T) {
	node, err := Parse("\"plan a\"&b")
	if err != nil {
		t.Fatal(err)
	}
	if !Eval(node, "this plan a and b") {
		t.Fatal("expected quoted term match")
	}
}

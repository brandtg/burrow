package whitelist

import (
	"strings"
	"testing"
)

func TestParseIgnoresCommentsAndBlanks(t *testing.T) {
	input := "# a comment\n\n/home/u/.cache/keep\n   /home/u/src  \n"
	set, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if !set.Contains("/home/u/.cache/keep") || !set.Contains("/home/u/src") {
		t.Fatalf("expected both entries to match, got none")
	}
	if set.Contains("/other") {
		t.Fatal("unexpected extra entry matched")
	}
}

func TestParseRejectsRelative(t *testing.T) {
	if _, err := Parse(strings.NewReader("relative/path\n")); err == nil {
		t.Fatal("expected relative path error")
	}
}

func TestContainsBoundary(t *testing.T) {
	set, err := Parse(strings.NewReader("/home/u/.cache/keep\n/home/ab\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		path string
		want bool
	}{
		{"/home/u/.cache/keep", true},     // exact match
		{"/home/u/.cache/keep/sub", true}, // descendant
		{"/home/u/.cache/keep/deep/sub", true},
		{"/home/u/.cache/keeper", false}, // sibling prefix
		{"/home/u/.cache", false},        // parent is not protected
		{"/home/ab", true},
		{"/home/abc", false}, // sibling prefix must respect boundary
		{"/home/u/.cache/other", false},
	}
	for _, c := range cases {
		if got := set.Contains(c.path); got != c.want {
			t.Errorf("Contains(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}

func TestEmptySet(t *testing.T) {
	var s *Set
	if s.Contains("/x") || !s.Empty() {
		t.Fatal("nil set must behave as empty")
	}
	set, err := Parse(strings.NewReader(""))
	if err != nil {
		t.Fatal(err)
	}
	if set.Contains("/x") || !set.Empty() {
		t.Fatal("parsed empty set must behave as empty")
	}
}

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
	got := set.Entries()
	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %v", got)
	}
	if got[0] != "/home/u/.cache/keep" || got[1] != "/home/u/src" {
		t.Fatalf("entries = %v", got)
	}
}

func TestParseRejectsRelative(t *testing.T) {
	if _, err := Parse(strings.NewReader("relative/path\n")); err == nil {
		t.Fatal("expected relative path error")
	}
}

func TestContainsPrefix(t *testing.T) {
	set, _ := Parse(strings.NewReader("/home/u/.cache/keep\n"))
	if !set.Contains("/home/u/.cache/keep") {
		t.Fatal("expected exact match")
	}
	if !set.Contains("/home/u/.cache/keep/sub") {
		t.Fatal("expected child match")
	}
	if set.Contains("/home/u/.cache/keeper") {
		t.Fatal("prefix must respect path boundary")
	}
	if set.Contains("/home/u/.cache/other") {
		t.Fatal("unexpected match")
	}
}

func TestProtectedParent(t *testing.T) {
	set, _ := Parse(strings.NewReader("/home/u/.cache/keep\n"))
	if !set.Protected("/home/u/.cache") {
		t.Fatal("expected parent of a whitelisted entry to be protected")
	}
	if !set.Protected("/home/u/.cache/keep") {
		t.Fatal("expected entry itself protected")
	}
	if set.Protected("/home/u/.cache/other") {
		t.Fatal("unrelated path must not be protected")
	}
}

func TestEmptySet(t *testing.T) {
	var s *Set
	if s.Contains("/x") || s.Protected("/x") || !s.Empty() {
		t.Fatal("nil set must behave as empty")
	}
}

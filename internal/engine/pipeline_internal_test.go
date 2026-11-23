package engine

import "testing"

// TestCommonAncestor covers the df mount selection: the common parent of the
// run's target paths, or "" when there is nothing to anchor on.
func TestCommonAncestor(t *testing.T) {
	cases := []struct {
		paths []string
		want  string
	}{
		{nil, ""},
		{[]string{"/home/u/.cache/x", "/home/u/.cache/y"}, "/home/u/.cache"},
		{[]string{"/home/u/.cache/x", "/home/u/.config/y"}, "/home/u"},
		{[]string{"/a", "/b"}, ""},
		{[]string{"/only/one"}, "/only/one"},
		{[]string{"/home/u/x", ""}, "/home/u/x"},
	}
	for _, tc := range cases {
		if got := commonAncestor(tc.paths); got != tc.want {
			t.Errorf("commonAncestor(%v) = %q, want %q", tc.paths, got, tc.want)
		}
	}
}

// TestEstimateFreedPrecedence pins the freed-bytes rule: an action-reported
// byte count wins (it knows what its command actually reclaimed), the
// measured target size is the fallback, and an unknown size estimates 0
// rather than guessing.
func TestEstimateFreedPrecedence(t *testing.T) {
	it := &PlannedItem{Target: Target{SizeKnown: true, SizeBytes: 100}}
	if got := estimateFreed(it, Result{Bytes: 7}); got != 7 {
		t.Fatalf("action bytes must win: got %d, want 7", got)
	}
	if got := estimateFreed(it, Result{}); got != 100 {
		t.Fatalf("measured size is the fallback: got %d, want 100", got)
	}
	unknown := &PlannedItem{Target: Target{SizeKnown: false}}
	if got := estimateFreed(unknown, Result{}); got != 0 {
		t.Fatalf("unknown size must estimate 0, got %d", got)
	}
}

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

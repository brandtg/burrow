package scan

import "testing"

func TestApplyExtentAccountingFairShare(t *testing.T) {
	a := &Node{Name: "a", Path: "/a"}
	b := &Node{Name: "b", Path: "/b"}
	shared := extent{physical: 100, length: 4096, shared: true}
	applyExtentAccounting([]fileExtents{
		{node: a, blocks: 1000, extents: []extent{shared}},
		{node: b, blocks: 1000, extents: []extent{shared}},
	})
	if a.Size != 500 || b.Size != 500 {
		t.Fatalf("shared extent not split evenly: a=%d b=%d, want 500/500", a.Size, b.Size)
	}
}

func TestApplyExtentAccountingExclusiveUntouched(t *testing.T) {
	a := &Node{Name: "a", Path: "/a"}
	only := extent{physical: 100, length: 4096, shared: false}
	applyExtentAccounting([]fileExtents{{node: a, blocks: 1000, extents: []extent{only}}})
	if a.Size != 1000 {
		t.Fatalf("exclusive extent size = %d, want 1000", a.Size)
	}
}

func TestApplyExtentAccountingMixed(t *testing.T) {
	a := &Node{Name: "a", Path: "/a"}
	b := &Node{Name: "b", Path: "/b"}
	// a holds one exclusive and one shared extent; b shares the second.
	shared := extent{physical: 200, length: 4096, shared: true}
	exclusive := extent{physical: 100, length: 4096, shared: false}
	applyExtentAccounting([]fileExtents{
		{node: a, blocks: 2000, extents: []extent{exclusive, shared}},
		{node: b, blocks: 2000, extents: []extent{shared}},
	})
	// a: exclusive 2000*4096/8192 = 1000, plus shared 2000*4096/8192/2 = 500.
	if a.Size != 1500 {
		t.Fatalf("a.Size = %d, want 1500", a.Size)
	}
	if b.Size != 1000 {
		t.Fatalf("b.Size = %d, want 1000 (half of its only extent)", b.Size)
	}
}

func TestRecomputeDirSizes(t *testing.T) {
	sub := &Node{Name: "sub", Path: "/root/sub", IsDir: true, Children: []*Node{
		{Name: "x", Path: "/root/sub/x", Size: 200},
	}}
	root := &Node{Name: "root", Path: "/root", IsDir: true, Children: []*Node{
		{Name: "f", Path: "/root/f", Size: 100},
		sub,
	}}
	if got := recomputeDirSizes(root); got != 300 || root.Size != 300 {
		t.Fatalf("root size = %d (returned %d), want 300", root.Size, got)
	}
	if sub.Size != 200 {
		t.Fatalf("sub size = %d, want 200", sub.Size)
	}
}

package dev

import (
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/engine"
)

// TestCacheRootsMatchTable pins the single-source rule: the user-cache sweep
// skips exactly the top-level XDG caches a dev item owns, and every one of them
// comes from the same itemSpecs table that builds the dev targets. A cache added
// to one list but not the other would double-list the same bytes.
func TestCacheRootsMatchTable(t *testing.T) {
	env := &engine.Env{Home: "/home/u", XDGCache: "/home/u/.cache"}

	want := map[string]bool{}
	for _, s := range itemSpecs {
		if root := s.cacheRoot(); root != "" {
			want[root] = true
		}
	}

	got := map[string]bool{}
	for _, p := range CacheRoots(env) {
		if filepath.Dir(p) != env.XDGCache {
			t.Fatalf("cache root %q is not a direct child of %q", p, env.XDGCache)
		}
		name := filepath.Base(p)
		if got[name] {
			t.Fatalf("cache root %q listed twice", name)
		}
		got[name] = true
	}

	for name := range want {
		if !got[name] {
			t.Fatalf("table cache root %q missing from CacheRoots", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Fatalf("CacheRoots has %q but no table item owns it", name)
		}
	}

	// Pin the exercised set so an accidental table edit is visible.
	expected := []string{
		"yarn", "go-build", "pip", "uv", "pypoetry", "mise",
		"typescript", "electron", "node-gyp", "vite", "webpack", "eslint",
		"prettier", "parcel-cache", "node", "gh",
	}
	for _, name := range expected {
		if !got[name] {
			t.Fatalf("expected cache root %q, got %v", name, got)
		}
	}
	if len(got) != len(expected) {
		t.Fatalf("cache roots = %v, want %v", got, expected)
	}
}

func TestCacheRootsEmptyWithoutXDGCache(t *testing.T) {
	if got := CacheRoots(&engine.Env{}); len(got) != 0 {
		t.Fatalf("CacheRoots = %v, want none", got)
	}
	if got := CacheRoots(nil); len(got) != 0 {
		t.Fatalf("CacheRoots(nil) = %v, want none", got)
	}
}

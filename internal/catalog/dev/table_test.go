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

// TestRegenPinnedForEverySpec pins the regen column of itemSpecs: every row
// must declare its rebuild cost explicitly and match this table. A new spec
// without an entry here fails the test, so a cache can never silently default
// to free (the most pre-selectable classification).
func TestRegenPinnedForEverySpec(t *testing.T) {
	want := map[string]engine.Regen{
		"dev.npm":                          engine.RegenCheap,
		"dev.pnpm":                         engine.RegenCheap,
		"dev.yarn":                         engine.RegenFree,
		"dev.yarn-cache":                   engine.RegenFree,
		"dev.bun":                          engine.RegenCheap,
		"dev.go-mod":                       engine.RegenExpensive,
		"dev.go-build":                     engine.RegenFree,
		"dev.rust-registry-cache":          engine.RegenExpensive,
		"dev.rustup-downloads":             engine.RegenExpensive,
		"dev.pip":                          engine.RegenCheap,
		"dev.uv":                           engine.RegenCheap,
		"dev.poetry.artifacts":             engine.RegenCheap,
		"dev.poetry.cache":                 engine.RegenCheap,
		"dev.conda":                        engine.RegenExpensive,
		"dev.gem":                          engine.RegenCheap,
		"dev.gem-bundle":                   engine.RegenCheap,
		"dev.rbenv":                        engine.RegenFree,
		"dev.cpan-build":                   engine.RegenFree,
		"dev.nix":                          engine.RegenExpensive,
		"dev.corepack":                     engine.RegenCheap,
		"dev.mise":                         engine.RegenCheap,
		"dev.frontend-caches.typescript":   engine.RegenFree,
		"dev.frontend-caches.electron":     engine.RegenFree,
		"dev.frontend-caches.node-gyp":     engine.RegenFree,
		"dev.frontend-caches.vite":         engine.RegenFree,
		"dev.frontend-caches.webpack":      engine.RegenFree,
		"dev.frontend-caches.eslint":       engine.RegenFree,
		"dev.frontend-caches.prettier":     engine.RegenFree,
		"dev.frontend-caches.parcel-cache": engine.RegenFree,
		"dev.docker-buildx":                engine.RegenFree,
		"dev.cloud-cli.kube":               engine.RegenFree,
		"dev.cloud-cli.aws":                engine.RegenFree,
		"dev.cloud-cli.gcloud":             engine.RegenFree,
		"dev.cloud-cli.azure":              engine.RegenFree,
		"dev.gh-cli":                       engine.RegenCheap,
	}
	seen := map[string]bool{}
	for _, s := range itemSpecs {
		got, ok := want[s.id]
		if !ok {
			t.Errorf("spec %s has no pinned regen; add it to TestRegenPinnedForEverySpec", s.id)
			continue
		}
		if s.regen != got {
			t.Errorf("spec %s regen = %v, want %v", s.id, s.regen, got)
		}
		seen[s.id] = true
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("pinned regen for %s but no such spec exists", id)
		}
	}
}

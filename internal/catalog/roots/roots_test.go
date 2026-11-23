package roots_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/catalog/roots"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
)

func testEnv(t *testing.T) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	cache := filepath.Join(home, ".cache")
	data := filepath.Join(home, ".local", "share")
	for _, d := range []string{
		home, cache,
		filepath.Join(home, "Projects", "alpha"),
		filepath.Join(home, "Downloads"),
		filepath.Join(data, "containers"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &engine.Env{
		Home:     home,
		XDGCache: cache,
		XDGData:  data,
		Cfg:      config.Default(),
	}
}

func TestAllListsExistingReclaimableRoots(t *testing.T) {
	env := testEnv(t)
	got := roots.All(env)
	paths := map[string]string{}
	for _, r := range got {
		paths[r.Path] = r.Selector
	}
	for _, want := range []string{
		filepath.Join(env.Home, "Projects"),
		filepath.Join(env.Home, ".cache"),
		filepath.Join(env.Home, "Downloads"),
		filepath.Join(env.XDGData, "containers"),
	} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("missing reclaimable root %s in %v", want, paths)
		}
	}
	// Missing directories never appear.
	if _, ok := paths[filepath.Join(env.XDGData, "docker")]; ok {
		t.Fatal("a nonexistent root must not be listed")
	}
}

func TestAllDeduplicates(t *testing.T) {
	env := testEnv(t)
	// A project root that is also the whole XDG cache would be listed twice
	// (projects + user-cache); All must keep one entry.
	env.Cfg.Purge.Paths = []string{env.XDGCache}
	got := roots.All(env)
	seen := map[string]bool{}
	for _, r := range got {
		if seen[r.Path] {
			t.Fatalf("root %s listed twice", r.Path)
		}
		seen[r.Path] = true
	}
}

func TestSelectorForMostSpecificRootWins(t *testing.T) {
	env := testEnv(t)
	env.Cfg.Purge.Paths = []string{filepath.Join(env.Home, "Projects")}
	rs := roots.All(env)

	// A path deep inside a project root selects the projects category: the
	// configured root is the tightest containing reclaimable root.
	got := roots.SelectorFor(filepath.Join(env.Home, "Projects", "alpha", "node_modules"), rs)
	if got != "projects" {
		t.Fatalf("SelectorFor deep path = %q, want projects", got)
	}

	// A cache path selects the sweep, not the downloads root.
	got = roots.SelectorFor(filepath.Join(env.Home, ".cache", "fontconfig"), rs)
	if got != "user-cache" {
		t.Fatalf("SelectorFor cache path = %q, want user-cache", got)
	}

	// No containing root: empty.
	if got := roots.SelectorFor("/etc/passwd", rs); got != "" {
		t.Fatalf("SelectorFor outside all roots = %q, want empty", got)
	}
}

func TestSelectorForPrefersLongerRoot(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	outer := filepath.Join(home, "work")
	inner := filepath.Join(outer, "cache")
	for _, d := range []string{inner} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	rs := []roots.Root{
		{Path: outer, Selector: "projects"},
		{Path: inner, Selector: "user-cache"},
	}
	if got := roots.SelectorFor(filepath.Join(inner, "x"), rs); got != "user-cache" {
		t.Fatalf("the most specific root must win, got %q", got)
	}
	if got := roots.SelectorFor(filepath.Join(outer, "src"), rs); got != "projects" {
		t.Fatalf("a path only in the outer root must select it, got %q", got)
	}
}

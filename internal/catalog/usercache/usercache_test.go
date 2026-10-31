package usercache_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/catalog/dev"
	"github.com/brandtg/burrow/internal/catalog/usercache"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/trash"
	"github.com/brandtg/burrow/internal/whitelist"
)

func testEnv(t *testing.T) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	for _, d := range []string{
		home,
		filepath.Join(dir, "cache"),
		filepath.Join(dir, "config"),
		filepath.Join(dir, "data"),
		filepath.Join(dir, "state"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &engine.Env{
		Home:      home,
		XDGCache:  filepath.Join(dir, "cache"),
		XDGConfig: filepath.Join(dir, "config"),
		XDGData:   filepath.Join(dir, "data"),
		XDGState:  filepath.Join(dir, "state"),
		Run:       owner.New(true),
		Cfg:       config.Default(),
		Whitelist: &whitelist.Set{},
		Paths: config.Paths{
			TrashDir: filepath.Join(dir, "data", "Trash"),
		},
		DryRun: true,
	}
}

func mkdir(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func ids(t *testing.T, env *engine.Env) map[string]engine.Target {
	t.Helper()
	targets, err := usercache.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]engine.Target{}
	for _, tg := range targets {
		out[tg.ID] = tg
	}
	return out
}

func TestAppCachesExcludeDevAndProtected(t *testing.T) {
	env := testEnv(t)
	mkdir(t,
		filepath.Join(env.XDGCache, "typescript"),
		filepath.Join(env.XDGCache, "vite"),
		filepath.Join(env.XDGCache, "thumbnails"),
		filepath.Join(env.XDGCache, "burrow"),
		filepath.Join(env.XDGCache, "dconf"),
		filepath.Join(env.XDGCache, "randomcache"),
	)
	got := ids(t, env)
	if _, ok := got["user-cache.app-caches.randomcache"]; !ok {
		t.Fatalf("randomcache should be an app-cache row: %v", keys(got))
	}
	for _, id := range []string{"user-cache.app-caches.typescript", "user-cache.app-caches.vite", "user-cache.app-caches.thumbnails", "user-cache.app-caches.burrow", "user-cache.app-caches.dconf"} {
		if _, ok := got[id]; ok {
			t.Fatalf("unexpected row %s", id)
		}
	}
	if _, ok := got["user-cache.thumbnails"]; !ok {
		t.Fatalf("thumbnails must have its own row: %v", keys(got))
	}
}

func TestWhitelistSkipsEntry(t *testing.T) {
	env := testEnv(t)
	mkdir(t,
		filepath.Join(env.XDGCache, "keep"),
		filepath.Join(env.XDGCache, "other"),
	)
	wl, err := whitelist.Parse(strings.NewReader(filepath.Join(env.XDGCache, "keep") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	env.Whitelist = wl
	got := ids(t, env)
	if _, ok := got["user-cache.app-caches.keep"]; ok {
		t.Fatal("whitelisted cache entry must be skipped")
	}
	if _, ok := got["user-cache.app-caches.other"]; !ok {
		t.Fatalf("unrelated cache should remain: %v", keys(got))
	}
}

// TestWhitelistDescendantDoesNotProtectParent pins the single prefix rule: a
// whitelisted descendant protects itself, not the top-level cache that contains
// it.
func TestWhitelistDescendantDoesNotProtectParent(t *testing.T) {
	env := testEnv(t)
	mkdir(t, filepath.Join(env.XDGCache, "keep", "sub"))
	wl, err := whitelist.Parse(strings.NewReader(filepath.Join(env.XDGCache, "keep", "sub") + "\n"))
	if err != nil {
		t.Fatal(err)
	}
	env.Whitelist = wl
	got := ids(t, env)
	if _, ok := got["user-cache.app-caches.keep"]; !ok {
		t.Fatal("a descendant whitelist must not protect the parent cache row")
	}
}

func TestTrashIsDefaultOffAndHighRisk(t *testing.T) {
	env := testEnv(t)
	env.DryRun = false
	tr := trash.New(env.Paths.TrashDir, os.Getuid())
	src := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Put(src); err != nil {
		t.Fatal(err)
	}
	got := ids(t, env)
	item, ok := got["user-cache.trash"]
	if !ok {
		t.Fatalf("expected user-cache.trash: %v", keys(got))
	}
	if item.Recovery != engine.RecoveryDelete || item.DefaultOn || item.Risk != engine.RiskHigh {
		t.Fatalf("trash contract wrong: recovery=%v default=%v risk=%v", item.Recovery, item.DefaultOn, item.Risk)
	}
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{usercache.New()},
		Only:       []string{"user-cache.trash"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Selected()) != 0 {
		t.Fatal("user-cache.trash must never be selected by default")
	}
	// Explicit selection is required and, once given, empties the trash.
	plan.Items[0].Selected = true
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.OK != 1 {
		t.Fatalf("explicit selection should empty the trash: %+v", sum)
	}
	if entries, err := tr.List(); err != nil || len(entries) != 0 {
		t.Fatalf("trash not emptied: entries=%v err=%v", entries, err)
	}
}

func keys(m map[string]engine.Target) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestNoOverlapWithDevCategory proves dev items win: a cache owned by dev is
// not also offered by the generic sweep, so it is never double-counted or
// trashed twice.
func TestNoOverlapWithDevCategory(t *testing.T) {
	env := testEnv(t)
	t.Setenv("PATH", t.TempDir())
	mkdir(t,
		filepath.Join(env.XDGCache, "typescript"),
		filepath.Join(env.XDGCache, "randomcache"),
	)
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev.New(), usercache.New()},
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, it := range plan.Items {
		seen[it.Target.ID] = true
	}
	if !seen["dev.frontend-caches.typescript"] {
		t.Fatalf("dev should own the typescript cache: %v", keys2(plan))
	}
	if seen["user-cache.app-caches.typescript"] {
		t.Fatal("user-cache must not double-count a dev-owned cache")
	}
	if !seen["user-cache.app-caches.randomcache"] {
		t.Fatalf("randomcache should remain a user-cache row: %v", keys2(plan))
	}
}

func keys2(plan *engine.Plan) []string {
	var out []string
	for _, it := range plan.Items {
		out = append(out, it.Target.ID)
	}
	return out
}

func TestSweepCuratesUnknownAndExpensive(t *testing.T) {
	env := testEnv(t)
	mkdir(t,
		filepath.Join(env.XDGCache, "huggingface"),
		filepath.Join(env.XDGCache, "randomcache"),
		filepath.Join(env.XDGCache, "fontconfig"),
	)
	got := ids(t, env)

	hf := got["user-cache.app-caches.huggingface"]
	if hf.DefaultOn {
		t.Fatal("huggingface must not be pre-selected")
	}
	if hf.Regen != engine.RegenExpensive || hf.Risk != engine.RiskMed {
		t.Fatalf("huggingface classification wrong: regen=%v risk=%v", hf.Regen, hf.Risk)
	}
	if hf.AutoSelectable() {
		t.Fatal("expensive huggingface must not be auto-selectable")
	}

	unknown := got["user-cache.app-caches.randomcache"]
	if unknown.DefaultOn {
		t.Fatal("unknown cache must not be pre-selected by default")
	}

	cheap := got["user-cache.app-caches.fontconfig"]
	if !cheap.DefaultOn || !cheap.AutoSelectable() {
		t.Fatalf("allowlisted cheap cache should be pre-selected: %+v", cheap)
	}
}

func TestSweepUnknownConfigPreSelects(t *testing.T) {
	env := testEnv(t)
	env.Cfg.UserCache.SweepUnknown = true
	mkdir(t, filepath.Join(env.XDGCache, "randomcache"))
	got := ids(t, env)
	if !got["user-cache.app-caches.randomcache"].DefaultOn {
		t.Fatal("sweep_unknown should pre-select an unknown cache")
	}
}

func TestExpensiveCacheIdleGate(t *testing.T) {
	env := testEnv(t)
	mkdir(t, filepath.Join(env.XDGCache, "huggingface"))
	got := ids(t, env)
	if !got["user-cache.app-caches.huggingface"].Gated {
		t.Fatal("freshly-used expensive cache should be gated")
	}
}

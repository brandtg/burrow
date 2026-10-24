package dev_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/catalog/dev"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

func testEnv(t *testing.T, dryRun bool) *engine.Env {
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
		Run:       owner.New(dryRun),
		Cfg:       config.Default(),
		Whitelist: &whitelist.Set{},
		Paths: config.Paths{
			Journal:  filepath.Join(dir, "operations.jsonl"),
			TrashDir: filepath.Join(dir, "Trash"),
		},
		DryRun: dryRun,
	}
}

func useTestbin(t *testing.T, env *engine.Env) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "argv.log")
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "testbin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BURROW_TESTBIN_LOG", logPath)
	t.Setenv("BURROW_TEST_HOME", env.Home)
	t.Setenv("PATH", abs+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func discover(t *testing.T, env *engine.Env) map[string]engine.Target {
	t.Helper()
	targets, err := dev.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]engine.Target{}
	for _, tg := range targets {
		byID[tg.ID] = tg
	}
	return byID
}

func runOnly(t *testing.T, env *engine.Env, id string) *engine.Summary {
	t.Helper()
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev.New()},
		Only:       []string{id},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The test exercises execution and guards; force-select regardless of the
	// selection policy (idle gating, regeneration cost).
	for _, it := range plan.Items {
		it.Selected = true
	}
	return engine.Execute(context.Background(), env, plan, nil)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestOwnerItemsPlanExactArgv(t *testing.T) {
	env := testEnv(t, true)
	useTestbin(t, env)
	mkdirs(t, filepath.Join(env.XDGCache, "node", "corepack"))
	byID := discover(t, env)

	cases := []struct {
		id   string
		argv []string
	}{
		{"dev.npm", []string{"npm", "cache", "clean", "--force"}},
		{"dev.pnpm", []string{"pnpm", "store", "prune"}},
		{"dev.bun", []string{"bun", "pm", "cache", "rm"}},
		{"dev.go-mod", []string{"go", "clean", "-modcache"}},
		{"dev.go-build", []string{"go", "clean", "-cache"}},
		{"dev.pip", []string{"pip3", "cache", "purge"}},
		{"dev.uv", []string{"uv", "cache", "prune"}},
		{"dev.mise", []string{"mise", "cache", "clear"}},
		{"dev.conda", []string{"conda", "clean", "--yes", "--index-cache", "--tarballs", "--logfiles"}},
		{"dev.nix", []string{"nix-collect-garbage", "--delete-older-than", "30d"}},
		{"dev.corepack", []string{"corepack", "cache", "clean"}},
		{"dev.gh-cli", []string{"gh", "config", "clear-cache"}},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			tg, ok := byID[tc.id]
			if !ok {
				t.Fatalf("missing %s; got %v", tc.id, keys(byID))
			}
			if tg.Recovery != engine.RecoveryOwner {
				t.Fatalf("%s recovery = %v, want owner", tc.id, tg.Recovery)
			}
			ap, ok := tg.Action.(engine.ArgvPlanner)
			if !ok {
				t.Fatalf("%s action does not expose planned argv", tc.id)
			}
			if got := strings.Join(ap.PlannedArgv(), " "); got != strings.Join(tc.argv, " ") {
				t.Fatalf("%s argv = %q, want %q", tc.id, got, strings.Join(tc.argv, " "))
			}
		})
	}
}

func TestOwnerPathResolutionAndExecution(t *testing.T) {
	env := testEnv(t, false)
	logPath := useTestbin(t, env)
	byID := discover(t, env)
	npm, ok := byID["dev.npm"]
	if !ok {
		t.Fatal("missing dev.npm")
	}
	if want := filepath.Join(env.Home, ".npm"); npm.Path != want {
		t.Fatalf("npm path = %q, want %q", npm.Path, want)
	}
	sum := runOnly(t, env, "dev.npm")
	if sum.OK != 1 {
		t.Fatalf("expected one ok item, got %+v", sum)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "argv: npm cache clean --force") {
		t.Fatalf("expected npm cache clean, log:\n%s", log)
	}
}

func TestOwnerPathChangeGuardStops(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{dev.New()},
		Only:       []string{"dev.npm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// Dev caches are not pre-selected by default; the guard is being tested.
	for _, it := range plan.Items {
		it.Selected = true
	}
	// Point the owner at a different cache between planning and execution.
	other := t.TempDir()
	t.Setenv("BURROW_TEST_HOME", other)
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.GuardStops != 1 || sum.OK != 0 {
		t.Fatalf("expected a guard stop, got %+v", sum)
	}
}

func TestFallbackTrashesDocumentedRoots(t *testing.T) {
	env := testEnv(t, true)
	// No owner tools on PATH: discovery must fall back to the cache roots.
	t.Setenv("PATH", t.TempDir())
	mkdirs(t,
		filepath.Join(env.Home, ".npm", "_cacache"),
		filepath.Join(env.Home, ".local", "share", "pnpm", "store"),
		filepath.Join(env.Home, ".bun", "install", "cache"),
		filepath.Join(env.Home, "go", "pkg", "mod"),
		filepath.Join(env.XDGCache, "go-build"),
		filepath.Join(env.XDGCache, "pip"),
		filepath.Join(env.XDGCache, "uv"),
		filepath.Join(env.XDGCache, "mise"),
	)
	byID := discover(t, env)
	cases := map[string]string{
		"dev.npm":      filepath.Join(env.Home, ".npm", "_cacache"),
		"dev.pnpm":     filepath.Join(env.Home, ".local", "share", "pnpm", "store"),
		"dev.bun":      filepath.Join(env.Home, ".bun", "install", "cache"),
		"dev.go-mod":   filepath.Join(env.Home, "go", "pkg", "mod"),
		"dev.go-build": filepath.Join(env.XDGCache, "go-build"),
		"dev.pip":      filepath.Join(env.XDGCache, "pip"),
		"dev.uv":       filepath.Join(env.XDGCache, "uv"),
		"dev.mise":     filepath.Join(env.XDGCache, "mise"),
	}
	for id, want := range cases {
		tg, ok := byID[id]
		if !ok {
			t.Fatalf("missing fallback %s", id)
		}
		if tg.Recovery != engine.RecoveryTrash || tg.Path != want {
			t.Fatalf("%s = {recovery:%v path:%q}, want trash %q", id, tg.Recovery, tg.Path, want)
		}
	}
}

func TestCargoKeepSubtrees(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	cargo := filepath.Join(env.Home, ".cargo")
	mkdirs(t,
		filepath.Join(cargo, "registry", "cache"),
		filepath.Join(cargo, "registry", "src"),
		filepath.Join(cargo, "registry", "index"),
		filepath.Join(cargo, "git"),
		filepath.Join(cargo, "bin"),
		filepath.Join(cargo, "toolchains"),
	)
	// Plant a file so the cache dir is non-empty and measurable.
	if err := os.WriteFile(filepath.Join(cargo, "registry", "cache", "crate.crate"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := runOnly(t, env, "dev.rust-registry-cache")
	if sum.OK != 1 {
		t.Fatalf("expected the cache to be trashed, got %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(cargo, "registry", "cache")); !os.IsNotExist(err) {
		t.Fatalf("registry/cache should be gone: %v", err)
	}
	for _, keep := range []string{"registry/src", "registry/index", "git", "bin", "toolchains"} {
		if _, err := os.Stat(filepath.Join(cargo, keep)); err != nil {
			t.Fatalf("kept subtree %s missing: %v", keep, err)
		}
	}
}

func TestCargoProcessGuardSkips(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	cache := filepath.Join(env.Home, ".cargo", "registry", "cache")
	mkdirs(t, cache)
	if err := os.WriteFile(filepath.Join(cache, "crate.crate"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BURROW_TESTBIN_PGREP", "cargo")
	sum := runOnly(t, env, "dev.rust-registry-cache")
	if sum.GuardStops != 1 || sum.OK != 0 {
		t.Fatalf("expected a cargo guard stop, got %+v", sum)
	}
	if _, err := os.Stat(cache); err != nil {
		t.Fatalf("cache must be kept while cargo runs: %v", err)
	}
}

func TestPoetryKeepsVirtualenvs(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	base := filepath.Join(env.XDGCache, "pypoetry")
	mkdirs(t, filepath.Join(base, "artifacts"), filepath.Join(base, "cache"), filepath.Join(base, "virtualenvs"))
	byID := discover(t, env)
	if _, ok := byID["dev.poetry.artifacts"]; !ok {
		t.Fatal("missing dev.poetry.artifacts")
	}
	if _, ok := byID["dev.poetry.cache"]; !ok {
		t.Fatal("missing dev.poetry.cache")
	}
	sum := runOnly(t, env, "dev.poetry")
	if sum.OK != 2 {
		t.Fatalf("expected two poetry items trashed, got %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(base, "virtualenvs")); err != nil {
		t.Fatalf("virtualenvs must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "artifacts")); !os.IsNotExist(err) {
		t.Fatalf("artifacts should be gone: %v", err)
	}
}

func TestCPANKeepsSources(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	base := filepath.Join(env.Home, ".cpan")
	mkdirs(t, filepath.Join(base, "build"), filepath.Join(base, "sources"))
	sum := runOnly(t, env, "dev.cpan-build")
	if sum.OK != 1 {
		t.Fatalf("expected build trashed, got %+v", sum)
	}
	if _, err := os.Stat(filepath.Join(base, "sources")); err != nil {
		t.Fatalf("~/.cpan/sources must survive: %v", err)
	}
	if _, err := os.Stat(filepath.Join(base, "build")); !os.IsNotExist(err) {
		t.Fatalf("~/.cpan/build should be gone: %v", err)
	}
}

func TestFrontendAndCloudCachesAreTrash(t *testing.T) {
	env := testEnv(t, true)
	useTestbin(t, env)
	mkdirs(t,
		filepath.Join(env.XDGCache, "typescript"),
		filepath.Join(env.XDGCache, "vite"),
		filepath.Join(env.Home, ".kube", "cache"),
		filepath.Join(env.Home, ".docker", "buildx", "cache"),
	)
	byID := discover(t, env)
	for _, id := range []string{"dev.frontend-caches.typescript", "dev.frontend-caches.vite", "dev.cloud-cli.kube", "dev.docker-buildx"} {
		tg, ok := byID[id]
		if !ok {
			t.Fatalf("missing %s", id)
		}
		if tg.Recovery != engine.RecoveryTrash {
			t.Fatalf("%s recovery = %v, want trash", id, tg.Recovery)
		}
	}
}

func TestMiscTrashItems(t *testing.T) {
	env := testEnv(t, true)
	useTestbin(t, env)
	paths := map[string]string{
		"dev.yarn":             filepath.Join(env.Home, ".yarn", "cache"),
		"dev.yarn-cache":       filepath.Join(env.XDGCache, "yarn"),
		"dev.rustup-downloads": filepath.Join(env.Home, ".rustup", "downloads"),
		"dev.gem.3.2.0":        filepath.Join(env.Home, ".gem", "ruby", "3.2.0", "cache"),
		"dev.gem-bundle":       filepath.Join(env.Home, ".bundle", "cache"),
		"dev.rbenv":            filepath.Join(env.Home, ".rbenv", "cache"),
	}
	for _, p := range paths {
		mkdirs(t, p)
	}
	byID := discover(t, env)
	for id, want := range paths {
		tg, ok := byID[id]
		if !ok {
			t.Fatalf("missing %s: %v", id, keys(byID))
		}
		if tg.Recovery != engine.RecoveryTrash || tg.Path != want {
			t.Fatalf("%s = {recovery:%v path:%q}, want trash %q", id, tg.Recovery, tg.Path, want)
		}
	}
}

func keys(m map[string]engine.Target) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestFixtureKeepDiscard runs the cache items against the checked-in
// testdata/dev tree and asserts the keep/discard reasoning: cache portions are
// trashed while registry/src, virtualenvs, and cpan/sources survive.
func TestFixtureKeepDiscard(t *testing.T) {
	env := testEnv(t, false)
	useTestbin(t, env)
	base, err := filepath.Abs(filepath.Join("..", "..", "..", "testdata", "dev"))
	if err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join(base, "cargo"), filepath.Join(env.Home, ".cargo"))
	copyTree(t, filepath.Join(base, "pypoetry"), filepath.Join(env.XDGCache, "pypoetry"))
	copyTree(t, filepath.Join(base, "cpan"), filepath.Join(env.Home, ".cpan"))

	if sum := runOnly(t, env, "dev.rust-registry-cache"); sum.OK != 1 {
		t.Fatalf("cargo cache not trashed: %+v", sum)
	}
	if sum := runOnly(t, env, "dev.poetry"); sum.OK != 2 {
		t.Fatalf("poetry caches not trashed: %+v", sum)
	}
	if sum := runOnly(t, env, "dev.cpan-build"); sum.OK != 1 {
		t.Fatalf("cpan build not trashed: %+v", sum)
	}

	mustExist := []string{
		filepath.Join(env.Home, ".cargo", "registry", "src"),
		filepath.Join(env.Home, ".cargo", "registry", "index"),
		filepath.Join(env.Home, ".cargo", "git"),
		filepath.Join(env.Home, ".cargo", "bin"),
		filepath.Join(env.Home, ".cargo", "toolchains"),
		filepath.Join(env.XDGCache, "pypoetry", "virtualenvs"),
		filepath.Join(env.Home, ".cpan", "sources"),
	}
	for _, p := range mustExist {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("kept path %s missing: %v", p, err)
		}
	}
	mustBeGone := []string{
		filepath.Join(env.Home, ".cargo", "registry", "cache"),
		filepath.Join(env.XDGCache, "pypoetry", "artifacts"),
		filepath.Join(env.XDGCache, "pypoetry", "cache"),
		filepath.Join(env.Home, ".cpan", "build"),
	}
	for _, p := range mustBeGone {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("discarded path %s still present: %v", p, err)
		}
	}
}

func TestDevIdleGateAndRegen(t *testing.T) {
	env := testEnv(t, true)
	// No owner tools: discovery uses the documented cache roots.
	t.Setenv("PATH", t.TempDir())
	fresh := filepath.Join(env.Home, ".npm", "_cacache")
	old := filepath.Join(env.XDGCache, "pip")
	mkdirs(t, fresh, old)
	oldTime := time.Now().Add(-90 * 24 * time.Hour)
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	byID := discover(t, env)

	npm := byID["dev.npm"]
	if !npm.Gated {
		t.Fatalf("fresh npm cache should be gated: %+v", npm)
	}
	if npm.AutoSelectable() {
		t.Fatal("gated npm cache must not be auto-selectable")
	}
	if npm.Regen != engine.RegenCheap {
		t.Fatalf("npm regen = %v, want cheap", npm.Regen)
	}

	pip := byID["dev.pip"]
	if pip.Gated {
		t.Fatalf("old pip cache should not be gated: %+v", pip)
	}
	if !pip.AutoSelectable() {
		t.Fatal("old pip cache should be auto-selectable")
	}
}

func TestDevExpensiveNotAutoSelectable(t *testing.T) {
	env := testEnv(t, true)
	t.Setenv("PATH", t.TempDir())
	mkdirs(t, filepath.Join(env.Home, "go", "pkg", "mod"))
	byID := discover(t, env)
	tg, ok := byID["dev.go-mod"]
	if !ok {
		t.Fatalf("missing dev.go-mod: %v", keys(byID))
	}
	if tg.Regen != engine.RegenExpensive {
		t.Fatalf("go-mod regen = %v, want expensive", tg.Regen)
	}
	if tg.AutoSelectable() {
		t.Fatal("expensive go-mod cache must not be auto-selectable")
	}
}

// TestDevNotPreselectedByDefault pins the v2 default: every dev row is shown
// but off, with the reason as its first evidence, and the built plan selects
// nothing unless the user opts in per run.
func TestDevNotPreselectedByDefault(t *testing.T) {
	env := testEnv(t, true)
	// No owner tools: discovery uses the documented cache roots.
	t.Setenv("PATH", t.TempDir())
	mkdirs(t,
		filepath.Join(env.Home, ".npm", "_cacache"),
		filepath.Join(env.XDGCache, "pip"),
		filepath.Join(env.XDGCache, "uv"),
	)
	targets, err := dev.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) == 0 {
		t.Fatal("expected dev targets")
	}
	for _, tg := range targets {
		if tg.DefaultOn {
			t.Fatalf("%s must not be pre-selected by default", tg.ID)
		}
		if tg.Evidence[0] != "dev caches are not pre-selected by default" {
			t.Fatalf("%s evidence[0] = %q, want the preselect reason", tg.ID, tg.Evidence[0])
		}
	}

	plan, err := engine.Build(context.Background(), env, engine.Options{Categories: []engine.Category{dev.New()}})
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Selected(); len(got) != 0 {
		t.Fatalf("nothing in the dev category may be pre-selected, got %d items", len(got))
	}
}

// TestDevPreselectOptIn: [dev] preselect = true restores regen-cost-driven
// pre-selection (free/cheap on, expensive off) and the idle gate still
// applies; the opt-out note is gone.
func TestDevPreselectOptIn(t *testing.T) {
	env := testEnv(t, true)
	t.Setenv("PATH", t.TempDir())
	pip := filepath.Join(env.XDGCache, "pip")
	mod := filepath.Join(env.Home, "go", "pkg", "mod")
	mkdirs(t, pip, mod)
	stale := time.Now().Add(-90 * 24 * time.Hour)
	for _, p := range []string{pip, mod} {
		if err := filepath.WalkDir(p, func(path string, _ os.DirEntry, _ error) error {
			return os.Chtimes(path, stale, stale)
		}); err != nil {
			t.Fatal(err)
		}
	}
	env.Cfg.Dev.Preselect = true
	byID := discover(t, env)
	if !byID["dev.pip"].DefaultOn {
		t.Fatalf("cheap caches must be pre-selected when opted in: %+v", byID["dev.pip"])
	}
	if byID["dev.go-mod"].AutoSelectable() {
		t.Fatalf("expensive caches stay out of bulk selection even when opted in: %+v", byID["dev.go-mod"])
	}
	for _, tg := range byID {
		for _, e := range tg.Evidence {
			if e == "dev caches are not pre-selected by default" {
				t.Fatalf("%s must not carry the opt-out note when preselect is on", tg.ID)
			}
		}
	}
}

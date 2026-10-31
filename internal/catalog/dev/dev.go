// Package dev implements the `dev` cleanup category: language and
// toolchain caches. It is the category that differentiates burrow from a
// generic cleaner.
//
// Each item prefers its owner command and falls back to trashing a documented
// cache root only when the owner command is unavailable. Owner caches are
// per-user: they are resolved by asking the owner immediately before acting and
// are never elevated.
//
// Provenance: this is a clean-room behavioral port of Mole's lib/clean/dev.sh
// keep/discard reasoning (Cargo registry/src, Poetry virtualenvs, CPAN sources,
// HF/torch/W&B model roots). No Mole source was copied.
package dev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// ID is the category identifier.
const ID = "dev"

// Category enumerates developer/toolchain cache targets.
type Category struct{}

// New returns the dev category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "Developer caches" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Language and toolchain caches. Dev caches are never pre-selected by default — press a to override. Owner commands where available; otherwise the documented cache root is trashed."
}

// Process guards. A running owner means skip, fail closed.
var (
	npmGuard   = catalog.ProcessGuard{Names: []string{"npm"}}
	pnpmGuard  = catalog.ProcessGuard{Names: []string{"pnpm"}}
	goGuard    = catalog.ProcessGuard{Names: []string{"go", "gopls"}}
	cargoGuard = catalog.ProcessGuard{Names: []string{"cargo", "rustc"}}
)

// cacheBase names the directory a spec's relative path is resolved against.
type cacheBase int

const (
	homeBase cacheBase = iota
	xdgCacheBase
	xdgConfigBase
	cargoHomeBase
	rustupHomeBase
)

// trashSpec is the documented cache-root fallback for an item.
type trashSpec struct {
	base     cacheBase
	paths    []string // candidate paths; the first that exists wins
	glob     string   // when set, every match is its own target
	evidence string
}

// ownerSpec is the owner command an item prefers. A nil probe means the owner
// reports no path to resolve; path, when set, is a known location used only for
// evidence and sizing.
type ownerSpec struct {
	binary   string
	command  func() owner.OwnerCommand
	probe    func() owner.OwnerCommand
	base     cacheBase
	path     string
	evidence string
}

// itemSpec is one row of the dev catalog. An item with both an owner and a
// trash fallback uses the owner when it is installed and the fallback otherwise,
// so the two never produce duplicate rows.
type itemSpec struct {
	id        string
	display   string
	risk      engine.Risk
	defaultOn bool
	guard     engine.Guard
	owner     *ownerSpec
	trash     *trashSpec
}

var itemSpecs = []itemSpec{
	{
		id: "dev.npm", display: "npm cache", risk: engine.RiskLow, defaultOn: true, guard: npmGuard,
		owner: &ownerSpec{binary: "npm", command: owner.NpmCacheClean, probe: owner.NpmCacheProbe, evidence: "npm owns its cache"},
		// Prefer the content-addressable cache so _logs and _update-notifier
		// are left alone; fall back to the whole ~/.npm only on older layouts.
		trash: &trashSpec{base: homeBase, paths: []string{".npm/_cacache", ".npm"}, evidence: "documented npm cache root"},
	},
	{
		id: "dev.pnpm", display: "pnpm store", risk: engine.RiskLow, defaultOn: true, guard: pnpmGuard,
		owner: &ownerSpec{binary: "pnpm", command: owner.PnpmStorePrune, probe: owner.PnpmStoreProbe, evidence: "pnpm owns its content-addressable store"},
		trash: &trashSpec{base: homeBase, paths: []string{".local/share/pnpm/store", ".pnpm-store"}, evidence: "documented pnpm store root"},
	},
	{
		id: "dev.yarn", display: "yarn cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".yarn/cache"}, evidence: "yarn cache is regenerable"},
	},
	{
		id: "dev.yarn-cache", display: "yarn cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"yarn"}, evidence: "yarn cache is regenerable"},
	},
	{
		id: "dev.bun", display: "bun cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "bun", command: owner.BunCacheRm, probe: owner.BunCacheProbe, evidence: "bun owns its cache"},
		trash: &trashSpec{base: homeBase, paths: []string{".bun/install/cache"}, evidence: "documented bun cache root"},
	},
	{
		id: "dev.go-mod", display: "Go module cache", risk: engine.RiskLow, defaultOn: true, guard: goGuard,
		owner: &ownerSpec{binary: "go", command: owner.GoCleanModCache, probe: owner.GoModCacheProbe, evidence: "go owns GOMODCACHE"},
		trash: &trashSpec{base: homeBase, paths: []string{"go/pkg/mod"}, evidence: "documented GOMODCACHE root"},
	},
	{
		id: "dev.go-build", display: "Go build cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "go", command: owner.GoCleanCache, probe: owner.GoBuildCacheProbe, evidence: "go owns GOCACHE; cleaning is concurrent-safe"},
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"go-build"}, evidence: "documented GOCACHE root"},
	},
	{
		id: "dev.rust-registry-cache", display: "Cargo registry cache", risk: engine.RiskLow, defaultOn: true, guard: cargoGuard,
		// Keep registry/src, registry/index, git, bin, and toolchains: only the
		// downloaded crate archives under registry/cache are disposable.
		trash: &trashSpec{base: cargoHomeBase, paths: []string{"registry/cache"}, evidence: "keep registry/src, registry/index, git, bin, toolchains"},
	},
	{
		id: "dev.rustup-downloads", display: "rustup downloads", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: rustupHomeBase, paths: []string{"downloads"}, evidence: "downloaded toolchain archives are re-fetchable"},
	},
	{
		id: "dev.pip", display: "pip cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "pip3", command: owner.PipCachePurge, probe: owner.PipCacheDirProbe, evidence: "pip owns its wheel cache"},
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"pip"}, evidence: "documented pip cache root"},
	},
	{
		id: "dev.uv", display: "uv cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "uv", command: owner.UvCachePrune, probe: owner.UvCacheDirProbe, evidence: "uv owns its cache"},
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"uv"}, evidence: "documented uv cache root"},
	},
	{
		id: "dev.poetry.artifacts", display: "Poetry artifacts", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"pypoetry/artifacts"}, evidence: "keep pypoetry/virtualenvs"},
	},
	{
		id: "dev.poetry.cache", display: "Poetry cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"pypoetry/cache"}, evidence: "keep pypoetry/virtualenvs"},
	},
	{
		id: "dev.conda", display: "conda cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "conda", command: owner.CondaClean, evidence: "conda owns its package caches"},
	},
	{
		id: "dev.gem", display: "RubyGems cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, glob: ".gem/ruby/*/cache", evidence: "gem cache is regenerable"},
	},
	{
		id: "dev.gem-bundle", display: "Bundler cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".bundle/cache"}, evidence: "bundler cache is regenerable"},
	},
	{
		id: "dev.rbenv", display: "rbenv cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".rbenv/cache"}, evidence: "rbenv cache is regenerable"},
	},
	{
		id: "dev.cpan-build", display: "CPAN build dir", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".cpan/build"}, evidence: "keep ~/.cpan/sources"},
	},
	{
		id: "dev.nix", display: "Nix store garbage", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "nix-collect-garbage", command: owner.NixCollectGarbage, evidence: "nix-collect-garbage owns the store"},
	},
	{
		id: "dev.corepack", display: "corepack cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "corepack", command: owner.CorepackCacheClean, base: xdgCacheBase, path: "node/corepack", evidence: "corepack owns its cache"},
	},
	{
		id: "dev.mise", display: "mise cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "mise", command: owner.MiseCacheClear, probe: owner.MiseCachePathProbe, evidence: "mise owns its cache"},
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"mise"}, evidence: "documented mise cache root"},
	},
	{
		id: "dev.frontend-caches.typescript", display: "typescript cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"typescript"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.electron", display: "electron cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"electron"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.node-gyp", display: "node-gyp cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"node-gyp"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.vite", display: "vite cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"vite"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.webpack", display: "webpack cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"webpack"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.eslint", display: "eslint cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"eslint"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.prettier", display: "prettier cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"prettier"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.frontend-caches.parcel-cache", display: "parcel-cache cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgCacheBase, paths: []string{"parcel-cache"}, evidence: "frontend tool caches are regenerable"},
	},
	{
		id: "dev.docker-buildx", display: "Docker buildx cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".docker/buildx/cache"}, evidence: "buildx cache is regenerable"},
	},
	{
		id: "dev.cloud-cli.kube", display: "kubectl cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".kube/cache"}, evidence: "kubectl discovery cache"},
	},
	{
		id: "dev.cloud-cli.aws", display: "AWS CLI cache", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".aws/cli/cache"}, evidence: "AWS CLI cache"},
	},
	{
		id: "dev.cloud-cli.gcloud", display: "gcloud logs", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: xdgConfigBase, paths: []string{"gcloud/logs"}, evidence: "gcloud logs"},
	},
	{
		id: "dev.cloud-cli.azure", display: "Azure logs", risk: engine.RiskLow, defaultOn: true,
		trash: &trashSpec{base: homeBase, paths: []string{".azure/logs"}, evidence: "Azure CLI logs"},
	},
	{
		id: "dev.gh-cli", display: "GitHub CLI cache", risk: engine.RiskLow, defaultOn: true,
		owner: &ownerSpec{binary: "gh", command: owner.GhClearCache, base: xdgCacheBase, path: "gh", evidence: "gh owns its cache"},
	},
}

// Discover enumerates dev targets. Per-item probe failures are skipped rather
// than returned, so one missing tool never blanks the category.
func (*Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	if env == nil {
		return nil, nil
	}
	var out []engine.Target
	for _, s := range itemSpecs {
		out = append(out, s.discover(ctx, env)...)
	}
	out = applyRegen(out)
	out = applyIdleGate(env, out)
	out = applyPreselect(env, out)
	return out, nil
}

// discover resolves one spec: the owner command when installed, otherwise the
// documented trash fallback.
func (s itemSpec) discover(ctx context.Context, env *engine.Env) []engine.Target {
	if s.owner != nil && owner.Available(s.owner.binary) {
		return s.owner.discover(ctx, env, s)
	}
	if s.trash != nil {
		return s.trash.discover(env, s)
	}
	return nil
}

// regenFor classifies how expensive a dev cache is to rebuild. It gates
// pre-selection only; it never changes how an item executes.
func regenFor(id string) engine.Regen {
	switch id {
	case "dev.go-mod", "dev.rust-registry-cache", "dev.rustup-downloads", "dev.conda", "dev.nix":
		return engine.RegenExpensive
	case "dev.npm", "dev.pnpm", "dev.bun", "dev.pip", "dev.uv",
		"dev.poetry.artifacts", "dev.poetry.cache", "dev.mise", "dev.corepack", "dev.gh-cli":
		return engine.RegenCheap
	}
	if strings.HasPrefix(id, "dev.gem") {
		return engine.RegenCheap
	}
	return engine.RegenFree
}

func applyRegen(targets []engine.Target) []engine.Target {
	for i := range targets {
		targets[i].Regen = regenFor(targets[i].ID)
	}
	return targets
}

// applyIdleGate marks a dev cache touched within [dev] max_age as gated: it is
// shown with an evidence line but not pre-selected, so a toolchain a person is
// actively using is not churned. A cache whose age cannot be established is
// left selectable.
func applyIdleGate(env *engine.Env, targets []engine.Target) []engine.Target {
	if env == nil || env.Cfg == nil {
		return targets
	}
	within, err := config.ParseDuration(env.Cfg.Dev.MaxAge)
	if err != nil || within <= 0 {
		return targets
	}
	for i := range targets {
		t := &targets[i]
		if t.Path == "" {
			continue
		}
		recent, known := catalog.IsRecent(t.Path, within)
		if known && recent {
			t.Gated = true
			t.Evidence = append(t.Evidence, fmt.Sprintf("active within %s; shown but not pre-selected", within))
		}
	}
	return targets
}

// applyPreselect implements the v2 default: the owner of the machine actively
// uses their toolchain caches, so nothing in this category is pre-selected
// unless [dev] preselect opts in. Rows stay visible and individually
// toggleable; the idle gate and regen cost still apply to bulk selection when
// preselect is enabled.
func applyPreselect(env *engine.Env, targets []engine.Target) []engine.Target {
	if env == nil || env.Cfg == nil || env.Cfg.Dev.Preselect {
		return targets
	}
	for i := range targets {
		t := &targets[i]
		t.DefaultOn = false
		t.Evidence = append([]string{"dev caches are not pre-selected by default"}, t.Evidence...)
	}
	return targets
}

// CacheRoots returns the top-level XDG cache directories the dev category
// manages. user-cache.app-caches uses it so a dev cache is never double-counted
// or trashed twice (dev items win). It is derived from itemSpecs, so adding a
// cache cannot leave the sweep out of date.
func CacheRoots(env *engine.Env) []string {
	if env == nil || env.XDGCache == "" {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range itemSpecs {
		name := s.cacheRoot()
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, filepath.Join(env.XDGCache, name))
	}
	return out
}

// cacheRoot is the top-level XDG cache entry an item owns, or "" when it does
// not live under the XDG cache. It is taken from the same spec that builds the
// item, so the sweep allow-list cannot drift from discovery.
func (s itemSpec) cacheRoot() string {
	var rel string
	switch {
	case s.trash != nil && s.trash.base == xdgCacheBase && len(s.trash.paths) > 0:
		rel = s.trash.paths[0]
	case s.owner != nil && s.owner.base == xdgCacheBase && s.owner.path != "":
		rel = s.owner.path
	default:
		return ""
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
}

// --- constructors ---

func ownerTarget(env *engine.Env, id, display, path string, cmd owner.OwnerCommand, resolve action.PathResolver, guard engine.Guard, risk engine.Risk, on bool, evidence string) engine.Target {
	t := engine.Target{
		ID:        id,
		Category:  ID,
		Display:   display,
		Path:      path,
		Action:    action.NewOwnerCommand(cmd, env.Run),
		Recovery:  engine.RecoveryOwner,
		NeedsRoot: cmd.NeedsRoot,
		Guard:     guard,
		Risk:      risk,
		DefaultOn: on,
		Evidence:  []string{evidence},
	}
	if resolve != nil {
		t.Action = &action.OwnerPathAction{
			Command:      cmd,
			Runner:       env.Run,
			Resolve:      resolve,
			Expected:     path,
			AllowedRoots: catalog.AllowedRoots(env),
		}
	}
	return t
}

func pathResolver(argv []string) action.PathResolver {
	return func(ctx context.Context, r *owner.Runner) (string, error) {
		return catalog.ResolveOwnerPath(ctx, r, argv)
	}
}

// trashIfExists builds a trash target for a documented cache root, or nothing
// when the root is absent or fails validation.
func trashIfExists(env *engine.Env, id, display, path string, risk engine.Risk, on bool, evidence string, guard engine.Guard) []engine.Target {
	if path == "" || !catalog.FileExists(path) {
		return nil
	}
	t, err := catalog.NewTrashTarget(env, catalog.TrashTargetOptions{
		ID: id, Category: ID, Display: display, Path: path,
		Risk: risk, DefaultOn: on, Evidence: []string{evidence},
	})
	if err != nil {
		return nil
	}
	if guard != nil {
		t.Guard = guard
	}
	return []engine.Target{t}
}

// discover builds an owner target for the spec, re-resolving its path at
// execution time when the owner reports one.
func (o ownerSpec) discover(ctx context.Context, env *engine.Env, s itemSpec) []engine.Target {
	cmd := o.command()
	if o.probe != nil {
		return resolvedOwner(ctx, env, s.id, s.display, cmd, o.probe(), s.guard, s.risk, s.defaultOn, o.evidence)
	}
	path := ""
	if o.path != "" {
		if p := filepath.Join(baseDir(env, o.base), o.path); catalog.DirExists(p) {
			path = p
		}
	}
	return []engine.Target{ownerTarget(env, s.id, s.display, path, cmd, nil, s.guard, s.risk, s.defaultOn, o.evidence)}
}

// discover builds the trash fallback for the spec, using the first candidate
// path that exists.
func (t trashSpec) discover(env *engine.Env, s itemSpec) []engine.Target {
	base := baseDir(env, t.base)
	if t.glob != "" {
		matches, _ := filepath.Glob(filepath.Join(base, t.glob))
		var out []engine.Target
		for _, m := range matches {
			id := s.id + "." + filepath.Base(filepath.Dir(m))
			out = append(out, trashIfExists(env, id, s.display, m, s.risk, s.defaultOn, t.evidence, s.guard)...)
		}
		return out
	}
	for _, rel := range t.paths {
		if ts := trashIfExists(env, s.id, s.display, filepath.Join(base, rel), s.risk, s.defaultOn, t.evidence, s.guard); len(ts) > 0 {
			return ts
		}
	}
	return nil
}

// resolvedOwner probes the owner for its cache path and, when the path is
// inside the user's trees, returns an owner target that re-resolves at
// execution time. Any uncertainty yields no target (fail closed).
func resolvedOwner(ctx context.Context, env *engine.Env, id, display string, cmd owner.OwnerCommand, probe owner.OwnerCommand, guard engine.Guard, risk engine.Risk, on bool, evidence string) []engine.Target {
	path, err := catalog.ResolveOwnerPath(ctx, env.Run, probe.Argv)
	if err != nil || !safe.WithinAnyRoot(path, catalog.AllowedRoots(env)) {
		return nil
	}
	return []engine.Target{ownerTarget(env, id, display, path, cmd, pathResolver(probe.Argv), guard, risk, on, evidence)}
}

// --- helpers ---

func baseDir(env *engine.Env, b cacheBase) string {
	switch b {
	case xdgCacheBase:
		return env.XDGCache
	case xdgConfigBase:
		return env.XDGConfig
	case cargoHomeBase:
		return envVar("CARGO_HOME", filepath.Join(env.Home, ".cargo"))
	case rustupHomeBase:
		return envVar("RUSTUP_HOME", filepath.Join(env.Home, ".rustup"))
	default:
		return env.Home
	}
}

func envVar(name, fallback string) string {
	if v := os.Getenv(name); filepath.IsAbs(v) {
		return v
	}
	return fallback
}

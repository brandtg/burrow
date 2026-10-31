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
	return "Language and toolchain caches, shown but not pre-selected by default. Owner commands where available; otherwise the documented cache root is trashed."
}

// Process guards. A running owner means skip, fail closed.
var (
	npmGuard   = catalog.ProcessGuard{Names: []string{"npm"}}
	pnpmGuard  = catalog.ProcessGuard{Names: []string{"pnpm"}}
	goGuard    = catalog.ProcessGuard{Names: []string{"go", "gopls"}}
	cargoGuard = catalog.ProcessGuard{Names: []string{"cargo", "rustc"}}
)

// Discover enumerates dev targets. Per-item probe failures are skipped rather
// than returned, so one missing tool never blanks the category.
func (*Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	if env == nil {
		return nil, nil
	}
	var out []engine.Target
	out = append(out, npmItem(ctx, env)...)
	out = append(out, pnpmItem(ctx, env)...)
	out = append(out, yarnItems(env)...)
	out = append(out, bunItem(ctx, env)...)
	out = append(out, goModItem(ctx, env)...)
	out = append(out, goBuildItem(ctx, env)...)
	out = append(out, rustRegistryItem(env)...)
	out = append(out, rustupDownloadsItem(env)...)
	out = append(out, pipItem(ctx, env)...)
	out = append(out, uvItem(ctx, env)...)
	out = append(out, poetryItems(env)...)
	out = append(out, condaItem(env)...)
	out = append(out, gemItems(env)...)
	out = append(out, rbenvItem(env)...)
	out = append(out, cpanBuildItem(env)...)
	out = append(out, nixItem(env)...)
	out = append(out, corepackItem(env)...)
	out = append(out, miseItem(ctx, env)...)
	out = append(out, frontendCacheItems(env)...)
	out = append(out, dockerBuildxItem(env)...)
	out = append(out, cloudCLIItems(env)...)
	out = append(out, ghItem(env)...)
	out = applyRegen(out)
	out = applyIdleGate(env, out)
	out = applyPreselect(env, out)
	return out, nil
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
// or trashed twice (dev items win).
func CacheRoots(env *engine.Env) []string {
	if env == nil || env.XDGCache == "" {
		return nil
	}
	names := []string{
		"yarn", "typescript", "electron", "node-gyp", "vite", "webpack",
		"eslint", "prettier", "parcel-cache", "pypoetry", "pip", "uv",
		"go-build", "mise", "gh", "node",
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(env.XDGCache, n))
	}
	return out
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
func trashIfExists(env *engine.Env, id, display, path string, risk engine.Risk, on bool, evidence string) []engine.Target {
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
	return []engine.Target{t}
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

// --- items ---

func npmItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("npm") {
		return resolvedOwner(ctx, env, "dev.npm", "npm cache", owner.NpmCacheClean(), owner.NpmCacheProbe(), npmGuard, engine.RiskLow, true, "npm owns its cache")
	}
	// Prefer the content-addressable cache so _logs and _update-notifier are
	// left alone; fall back to the whole ~/.npm only on older npm layouts.
	if ts := trashIfExists(env, "dev.npm", "npm cache", filepath.Join(env.Home, ".npm", "_cacache"), engine.RiskLow, true, "documented npm cache root"); len(ts) > 0 {
		return ts
	}
	return trashIfExists(env, "dev.npm", "npm cache", filepath.Join(env.Home, ".npm"), engine.RiskLow, true, "documented npm cache root")
}

func pnpmItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("pnpm") {
		return resolvedOwner(ctx, env, "dev.pnpm", "pnpm store", owner.PnpmStorePrune(), owner.PnpmStoreProbe(), pnpmGuard, engine.RiskLow, true, "pnpm owns its content-addressable store")
	}
	for _, p := range []string{filepath.Join(env.Home, ".local", "share", "pnpm", "store"), filepath.Join(env.Home, ".pnpm-store")} {
		if ts := trashIfExists(env, "dev.pnpm", "pnpm store", p, engine.RiskLow, true, "documented pnpm store root"); len(ts) > 0 {
			return ts
		}
	}
	return nil
}

func yarnItems(env *engine.Env) []engine.Target {
	var out []engine.Target
	out = append(out, trashIfExists(env, "dev.yarn", "yarn cache", filepath.Join(env.Home, ".yarn", "cache"), engine.RiskLow, true, "yarn cache is regenerable")...)
	out = append(out, trashIfExists(env, "dev.yarn-cache", "yarn cache", filepath.Join(env.XDGCache, "yarn"), engine.RiskLow, true, "yarn cache is regenerable")...)
	return out
}

func bunItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("bun") {
		return resolvedOwner(ctx, env, "dev.bun", "bun cache", owner.BunCacheRm(), owner.BunCacheProbe(), nil, engine.RiskLow, true, "bun owns its cache")
	}
	return trashIfExists(env, "dev.bun", "bun cache", filepath.Join(env.Home, ".bun", "install", "cache"), engine.RiskLow, true, "documented bun cache root")
}

func goModItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("go") {
		return resolvedOwner(ctx, env, "dev.go-mod", "Go module cache", owner.GoCleanModCache(), owner.GoModCacheProbe(), goGuard, engine.RiskLow, true, "go owns GOMODCACHE")
	}
	return trashIfExists(env, "dev.go-mod", "Go module cache", filepath.Join(env.Home, "go", "pkg", "mod"), engine.RiskLow, true, "documented GOMODCACHE root")
}

func goBuildItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("go") {
		// Go documents the build cache as safe to clean concurrently; no guard.
		return resolvedOwner(ctx, env, "dev.go-build", "Go build cache", owner.GoCleanCache(), owner.GoBuildCacheProbe(), nil, engine.RiskLow, true, "go owns GOCACHE; cleaning is concurrent-safe")
	}
	return trashIfExists(env, "dev.go-build", "Go build cache", filepath.Join(env.XDGCache, "go-build"), engine.RiskLow, true, "documented GOCACHE root")
}

func rustRegistryItem(env *engine.Env) []engine.Target {
	// Keep registry/src, registry/index, git, bin, and toolchains: only the
	// downloaded crate archives under registry/cache are disposable (Mole's
	// cargo reasoning). There is no cargo command for this, so the subtree is
	// trashed under the trash contract.
	cargoHome := envVar("CARGO_HOME", filepath.Join(env.Home, ".cargo"))
	targets := trashIfExists(env, "dev.rust-registry-cache", "Cargo registry cache", filepath.Join(cargoHome, "registry", "cache"), engine.RiskLow, true, "keep registry/src, registry/index, git, bin, toolchains")
	for i := range targets {
		targets[i].Guard = cargoGuard
	}
	return targets
}

func rustupDownloadsItem(env *engine.Env) []engine.Target {
	rustupHome := envVar("RUSTUP_HOME", filepath.Join(env.Home, ".rustup"))
	return trashIfExists(env, "dev.rustup-downloads", "rustup downloads", filepath.Join(rustupHome, "downloads"), engine.RiskLow, true, "downloaded toolchain archives are re-fetchable")
}

func pipItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("pip3") {
		return resolvedOwner(ctx, env, "dev.pip", "pip cache", owner.PipCachePurge(), owner.PipCacheDirProbe(), nil, engine.RiskLow, true, "pip owns its wheel cache")
	}
	return trashIfExists(env, "dev.pip", "pip cache", filepath.Join(env.XDGCache, "pip"), engine.RiskLow, true, "documented pip cache root")
}

func uvItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("uv") {
		return resolvedOwner(ctx, env, "dev.uv", "uv cache", owner.UvCachePrune(), owner.UvCacheDirProbe(), nil, engine.RiskLow, true, "uv owns its cache")
	}
	return trashIfExists(env, "dev.uv", "uv cache", filepath.Join(env.XDGCache, "uv"), engine.RiskLow, true, "documented uv cache root")
}

func poetryItems(env *engine.Env) []engine.Target {
	// Keep virtualenvs; only artifacts/ and cache/ are regenerable.
	var out []engine.Target
	out = append(out, trashIfExists(env, "dev.poetry.artifacts", "Poetry artifacts", filepath.Join(env.XDGCache, "pypoetry", "artifacts"), engine.RiskLow, true, "keep pypoetry/virtualenvs")...)
	out = append(out, trashIfExists(env, "dev.poetry.cache", "Poetry cache", filepath.Join(env.XDGCache, "pypoetry", "cache"), engine.RiskLow, true, "keep pypoetry/virtualenvs")...)
	return out
}

func condaItem(env *engine.Env) []engine.Target {
	if !owner.Available("conda") {
		return nil
	}
	return []engine.Target{ownerTarget(env, "dev.conda", "conda cache", "", owner.CondaClean(), nil, nil, engine.RiskLow, true, "conda owns its package caches")}
}

func gemItems(env *engine.Env) []engine.Target {
	var out []engine.Target
	if matches, _ := filepath.Glob(filepath.Join(env.Home, ".gem", "ruby", "*", "cache")); matches != nil {
		for _, m := range matches {
			out = append(out, trashIfExists(env, "dev.gem."+filepath.Base(filepath.Dir(m)), "RubyGems cache", m, engine.RiskLow, true, "gem cache is regenerable")...)
		}
	}
	out = append(out, trashIfExists(env, "dev.gem-bundle", "Bundler cache", filepath.Join(env.Home, ".bundle", "cache"), engine.RiskLow, true, "bundler cache is regenerable")...)
	return out
}

func rbenvItem(env *engine.Env) []engine.Target {
	return trashIfExists(env, "dev.rbenv", "rbenv cache", filepath.Join(env.Home, ".rbenv", "cache"), engine.RiskLow, true, "rbenv cache is regenerable")
}

func cpanBuildItem(env *engine.Env) []engine.Target {
	// Keep ~/.cpan/sources (Mole's CPAN reasoning).
	return trashIfExists(env, "dev.cpan-build", "CPAN build dir", filepath.Join(env.Home, ".cpan", "build"), engine.RiskLow, true, "keep ~/.cpan/sources")
}

func nixItem(env *engine.Env) []engine.Target {
	if !owner.Available("nix-collect-garbage") {
		return nil
	}
	return []engine.Target{ownerTarget(env, "dev.nix", "Nix store garbage", "", owner.NixCollectGarbage(), nil, nil, engine.RiskLow, true, "nix-collect-garbage owns the store")}
}

func corepackItem(env *engine.Env) []engine.Target {
	if !owner.Available("corepack") {
		return nil
	}
	home := envVar("COREPACK_HOME", filepath.Join(env.XDGCache, "node", "corepack"))
	path := ""
	if catalog.DirExists(home) {
		path = home
	}
	return []engine.Target{ownerTarget(env, "dev.corepack", "corepack cache", path, owner.CorepackCacheClean(), nil, nil, engine.RiskLow, true, "corepack owns its cache")}
}

func miseItem(ctx context.Context, env *engine.Env) []engine.Target {
	if owner.Available("mise") {
		return resolvedOwner(ctx, env, "dev.mise", "mise cache", owner.MiseCacheClear(), owner.MiseCachePathProbe(), nil, engine.RiskLow, true, "mise owns its cache")
	}
	return trashIfExists(env, "dev.mise", "mise cache", filepath.Join(env.XDGCache, "mise"), engine.RiskLow, true, "documented mise cache root")
}

func frontendCacheItems(env *engine.Env) []engine.Target {
	var out []engine.Target
	for _, name := range []string{"typescript", "electron", "node-gyp", "vite", "webpack", "eslint", "prettier", "parcel-cache"} {
		out = append(out, trashIfExists(env, "dev.frontend-caches."+name, name+" cache", filepath.Join(env.XDGCache, name), engine.RiskLow, true, "frontend tool caches are regenerable")...)
	}
	return out
}

func dockerBuildxItem(env *engine.Env) []engine.Target {
	return trashIfExists(env, "dev.docker-buildx", "Docker buildx cache", filepath.Join(env.Home, ".docker", "buildx", "cache"), engine.RiskLow, true, "buildx cache is regenerable")
}

func cloudCLIItems(env *engine.Env) []engine.Target {
	var out []engine.Target
	out = append(out, trashIfExists(env, "dev.cloud-cli.kube", "kubectl cache", filepath.Join(env.Home, ".kube", "cache"), engine.RiskLow, true, "kubectl discovery cache")...)
	out = append(out, trashIfExists(env, "dev.cloud-cli.aws", "AWS CLI cache", filepath.Join(env.Home, ".aws", "cli", "cache"), engine.RiskLow, true, "AWS CLI cache")...)
	out = append(out, trashIfExists(env, "dev.cloud-cli.gcloud", "gcloud logs", filepath.Join(env.XDGConfig, "gcloud", "logs"), engine.RiskLow, true, "gcloud logs")...)
	out = append(out, trashIfExists(env, "dev.cloud-cli.azure", "Azure logs", filepath.Join(env.Home, ".azure", "logs"), engine.RiskLow, true, "Azure CLI logs")...)
	return out
}

func ghItem(env *engine.Env) []engine.Target {
	if !owner.Available("gh") {
		return nil
	}
	path := ""
	if dir := filepath.Join(env.XDGCache, "gh"); catalog.DirExists(dir) {
		path = dir
	}
	return []engine.Target{ownerTarget(env, "dev.gh-cli", "GitHub CLI cache", path, owner.GhClearCache(), nil, nil, engine.RiskLow, true, "gh owns its cache")}
}

// --- helpers ---

func envVar(name, fallback string) string {
	if v := os.Getenv(name); filepath.IsAbs(v) {
		return v
	}
	return fallback
}

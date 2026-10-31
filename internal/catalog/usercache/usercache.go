// Package usercache implements the `user-cache` cleanup category:
// the XDG cache sweep. Every top-level entry under $XDG_CACHE_HOME that is not
// owned by a dev item is its own row, so a user can keep specific caches; the
// whitelist and the protected set also apply.
package usercache

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/catalog/dev"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// ID is the category identifier.
const ID = "user-cache"

// Category enumerates XDG cache targets.
type Category struct{}

// New returns the user-cache category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "User cache" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "XDG cache entries, one row per top-level cache. Only the curated cheap allowlist is pre-selected; expensive caches stay off. Trashed, so recoverable; whitelisted paths are skipped."
}

// protectedNames are top-level cache entries the generic sweep never touches.
// thumbnails has its own row; burrow must not trash its own cache; and the
// remaining entries hold settings, secrets, or runtime state rather than
// regenerable cache data, so deleting them is not recoverable in practice.
var protectedNames = map[string]bool{
	"thumbnails":    true,
	"burrow":        true,
	"dconf":         true,
	"keyring":       true,
	"keyrings":      true,
	"gvfs":          true,
	"gvfs-metadata": true,
	"pulse":         true,
	"systemd":       true,
}

// cheapCacheNames is the curated allowlist of top-level cache entries that are
// cheap and safe to regenerate, so they are pre-selected. Everything else is
// shown but off unless [user-cache] sweep_unknown is set.
var cheapCacheNames = map[string]bool{
	"fontconfig":        true,
	"mesa_shader_cache": true,
	"gstreamer-1.0":     true,
	"tracker3":          true,
}

// expensiveCacheNames are large or slow to rebuild (model weights, browser
// downloads, IDE indexes, build graphs). They are never pre-selected: deleting
// them means a costly re-download or rebuild.
var expensiveCacheNames = map[string]bool{
	"huggingface":           true,
	"torch":                 true,
	"wandb":                 true,
	"whisper":               true,
	"sentence-transformers": true,
	"ms-playwright":         true,
	"puppeteer":             true,
	"chromium":              true,
	"google-chrome":         true,
	"cypress":               true,
	"bazel":                 true,
	"bazelisk":              true,
	"JetBrains":             true,
	"pre-commit":            true,
}

// Discover enumerates the user-cache targets.
func (*Category) Discover(_ context.Context, env *engine.Env) ([]engine.Target, error) {
	if env == nil || env.XDGCache == "" {
		return nil, nil
	}
	var out []engine.Target
	out = append(out, thumbnailItem(env)...)
	out = append(out, appCacheItems(env)...)
	out = append(out, trashItem(env)...)
	return out, nil
}

func thumbnailItem(env *engine.Env) []engine.Target {
	return trashEntry(env, "user-cache.thumbnails", "thumbnails", filepath.Join(env.XDGCache, "thumbnails"), engine.RiskLow, engine.RegenFree, true, false)
}

func appCacheItems(env *engine.Env) []engine.Target {
	entries, err := os.ReadDir(env.XDGCache)
	if err != nil {
		return nil
	}
	devRoots := dev.CacheRoots(env)
	sweepUnknown := env.Cfg != nil && env.Cfg.UserCache.SweepUnknown
	within := userCacheMaxAge(env)
	var out []engine.Target
	for _, de := range entries {
		name := de.Name()
		if protectedNames[name] {
			continue
		}
		path := filepath.Join(env.XDGCache, name)
		if safe.WithinAnyRoot(path, devRoots) {
			continue
		}
		if env.Whitelist != nil && env.Whitelist.Contains(path) {
			continue
		}

		risk := engine.RiskMed
		regen := engine.RegenCheap
		on := sweepUnknown
		gated := false
		switch {
		case cheapCacheNames[name]:
			risk = engine.RiskLow
			on = true
		case expensiveCacheNames[name]:
			regen = engine.RegenExpensive
			on = false
			if recent, known := catalog.IsRecent(path, within); known && recent {
				gated = true
			}
		default:
			if recent, known := catalog.IsRecent(path, within); known && recent {
				gated = true
			}
		}
		out = append(out, trashEntry(env, "user-cache.app-caches."+name, name, path, risk, regen, on, gated)...)
	}
	return out
}

func userCacheMaxAge(env *engine.Env) time.Duration {
	if env == nil || env.Cfg == nil {
		return 0
	}
	d, err := config.ParseDuration(env.Cfg.UserCache.MaxAge)
	if err != nil {
		return 0
	}
	return d
}

func trashEntry(env *engine.Env, id, display, path string, risk engine.Risk, regen engine.Regen, on, gated bool) []engine.Target {
	if !catalog.FileExists(path) {
		return nil
	}
	t, err := catalog.NewTrashTarget(env, catalog.TrashTargetOptions{
		ID: id, Category: ID, Display: display, Path: path,
		Risk: risk, Regen: regen, Gated: gated, DefaultOn: on,
		Evidence: []string{"top-level XDG cache entry"},
	})
	if err != nil {
		return nil
	}
	return []engine.Target{t}
}

// trashItem empties the freedesktop Trash. It is irreversible, so it declares
// the owner contract, is high risk, and is default OFF and explicit.
func trashItem(env *engine.Env) []engine.Target {
	dir := env.Paths.TrashDir
	if dir == "" {
		dir = filepath.Join(env.XDGData, "Trash")
	}
	if !catalog.DirExists(dir) || !trashHasItems(dir) {
		return nil
	}
	tr := trash.New(dir, env.UID())
	t := engine.Target{
		ID:        "user-cache.trash",
		Category:  ID,
		Display:   "empty Trash",
		Path:      dir,
		Action:    &action.EmptyTrashAction{Trash: tr, Path: dir},
		Recovery:  engine.RecoveryOwner,
		Risk:      engine.RiskHigh,
		DefaultOn: false,
		Evidence:  []string{"emptying the Trash is irreversible"},
	}
	return []engine.Target{t}
}

func trashHasItems(dir string) bool {
	for _, sub := range []string{"files", "info"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err != nil {
			continue
		}
		for _, de := range entries {
			if strings.HasSuffix(de.Name(), ".trashinfo") || sub == "files" {
				return true
			}
		}
	}
	return false
}

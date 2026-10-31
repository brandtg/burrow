// Package roots enumerates the directories burrow's clean flow can reclaim.
//
// The analyze explorer uses it to mark rows inside a reclaimable root ([R])
// and to name the `burrow clean --only <selector>` that handles it. This is a
// read-only bridge: analyze never offers a delete key. It is a separate
// package because the category packages import internal/catalog, so package
// catalog cannot import them back.
package roots

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/catalog/dev"
	"github.com/brandtg/burrow/internal/catalog/projects"
	"github.com/brandtg/burrow/internal/engine"
)

// Root is one directory the clean flow can act on, paired with the --only
// selector that scopes a clean run to it.
type Root struct {
	Path     string
	Selector string
}

// All returns the absolute, cleaned, existing directories the clean flow can
// reclaim, assembled from the categories' own root exports. Only user-level
// trash-contract roots appear here: system and package-manager targets run
// owner commands and have no directory to navigate into.
func All(env *engine.Env) []Root {
	if env == nil {
		return nil
	}
	var out []Root
	add := func(selector string, paths ...string) {
		for _, p := range paths {
			if p == "" {
				continue
			}
			out = append(out, Root{Path: p, Selector: selector})
		}
	}
	add("projects", projects.Roots(env)...)
	add("dev", dev.CacheRoots(env)...)
	if env.XDGCache != "" {
		add("user-cache", env.XDGCache)
	}
	if env.Home != "" {
		add("downloads", filepath.Join(env.Home, "Downloads"))
	}
	if env.XDGData != "" {
		add("containers",
			filepath.Join(env.XDGData, "containers"),
			filepath.Join(env.XDGData, "docker"),
		)
	}

	seen := map[string]bool{}
	kept := out[:0]
	for _, r := range out {
		p := filepath.Clean(r.Path)
		if !filepath.IsAbs(p) || seen[p] {
			continue
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		seen[p] = true
		r.Path = p
		kept = append(kept, r)
	}
	return kept
}

// SelectorFor returns the --only selector for the most specific reclaimable
// root that contains path, or "" when none applies. Roots must be absolute
// and cleaned, as returned by Reclaimable.
func SelectorFor(path string, rs []Root) string {
	bestLen := -1
	best := ""
	for _, r := range rs {
		if !within(path, r.Path) {
			continue
		}
		if len(r.Path) > bestLen {
			bestLen = len(r.Path)
			best = r.Selector
		}
	}
	return best
}

func within(path, root string) bool {
	if path == root {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}

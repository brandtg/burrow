// Package projects implements the `projects` cleanup category: rebuildable
// build artifacts (node_modules, target, virtualenvs, __pycache__, ...) under
// the configured project roots. Discovery lives in this package: the
// git-tracked check, the active-project guard, and the inactivity window.
// Every target carries the trash recovery contract, so a wrong guess costs
// disk space, not data.
//
// Provenance: behavioral port of Mole's purge rule (git-tracked-file
// protection, nested-repo protection, age filter), reimplemented clean-room.
// No Mole source was copied (Mole is GPL-3.0).
package projects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/catalog"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
)

// ID is the category identifier.
const ID = "projects"

// Category enumerates rebuildable project artifacts. The only state is the
// warning sink and a git hook for tests; all protection rules live in this
// package's discovery.
type Category struct {
	warns []string
	// git overrides the git implementation for tests. Nil uses ExecGit.
	git Git
}

// New returns the projects category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "Projects" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Rebuildable artifacts under the configured project roots, one row per project. Only bytecode caches are pre-selected; node_modules and build outputs stay off. Trashed, so recoverable."
}

// Warnings returns the non-fatal discovery notes (a configured root that does
// not exist, a candidate that could not be validated) collected by the last
// Discover call. Discovery never aborts on a bad root; the warning is the only
// signal.
func (c *Category) Warnings() []string { return c.warns }

func (c *Category) warn(msg string) { c.warns = append(c.warns, ID+": "+msg) }

// Discover enumerates rebuildable artifacts under the configured project roots.
// Artifacts protected by the discovery keep rules (git-tracked, unknown git state,
// nested repository, unverifiable age, recent activity) produce no target.
// Discovery is read-only; each candidate is validated and snapshotted before it
// becomes a target, so an invalid candidate is skipped (fail closed).
func (c *Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	// Warnings accumulate on the category value, which is registered as a
	// singleton. Reset them so repeated discovery is idempotent.
	c.warns = nil
	if env == nil || env.Cfg == nil {
		return nil, nil
	}
	roots := Roots(env)
	if len(roots) == 0 {
		return nil, nil
	}
	maxAge, err := config.ParseDuration(env.Cfg.Purge.MaxAge)
	if err != nil {
		return nil, fmt.Errorf("invalid [projects] max_age: %w", err)
	}
	activeWithin, err := config.ParseDuration(env.Cfg.Purge.ActiveWithin)
	if err != nil {
		return nil, fmt.Errorf("invalid [projects] active_within: %w", err)
	}
	arts, err := Discover(ctx, Options{
		Roots:        roots,
		MaxAge:       maxAge,
		ActiveWithin: activeWithin,
		Git:          c.git,
		Warn:         c.warn,
	})
	if err != nil {
		return nil, err
	}
	var out []engine.Target
	seen := map[string]bool{}
	for _, a := range arts {
		if a.Kept() {
			continue
		}
		t, err := c.target(env, roots, a, seen)
		if err != nil {
			c.warn(fmt.Sprintf("skipping %s: %v", a.Path, err))
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

// target builds one trash-contract target for a discovered artifact. Roots is
// the resolved project-root allow-list: a candidate can only ever be trashed
// inside a configured root.
func (c *Category) target(env *engine.Env, roots []string, a Artifact, seen map[string]bool) (engine.Target, error) {
	regen := regenFor(a.Type)
	return catalog.NewTrashTarget(env, catalog.TrashTargetOptions{
		ID:         uniqueID(a, seen),
		Category:   ID,
		Display:    a.Type,
		Path:       a.Path,
		Roots:      roots,
		Risk:       engine.RiskLow,
		Regen:      regen,
		DefaultOn:  regen <= engine.RegenCheap,
		Evidence:   evidence(a),
		Group:      a.Project,
		GroupTitle: groupTitle(env, roots, a.Project),
		Size:       a.Size,
		SizeKnown:  a.SizeKnown,
	})
}

// groupTitle names the artifact's owning project in the tree. When the owning
// project is a scan root itself — an artifact directly in the root, or the
// root is the owning repository — filepath.Base would repeat the category
// title ("Projects > Projects"), so roots render as home-relative paths.
func groupTitle(env *engine.Env, roots []string, project string) string {
	if !slices.Contains(roots, project) {
		return filepath.Base(project)
	}
	home := ""
	if env != nil {
		home = env.Home
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
	}
	if home == "" {
		return project
	}
	if project == home {
		return "~"
	}
	if strings.HasPrefix(project, home+string(filepath.Separator)) {
		return "~" + project[len(home):]
	}
	return project
}

// regenFor classifies how expensive an artifact is to rebuild. It gates
// pre-selection only; it never changes how an item executes. Bytecode and
// content-addressed caches regenerate for free or a small download; anything
// else (node_modules, virtualenvs, Rust target, build outputs) means
// re-downloading dependencies or re-running a build.
func regenFor(pattern string) engine.Regen {
	switch pattern {
	case "__pycache__":
		return engine.RegenFree
	case "_cacache":
		return engine.RegenCheap
	default:
		return engine.RegenExpensive
	}
}

// evidence states why the artifact is a candidate: its last known activity.
func evidence(a Artifact) []string {
	if !a.AgeKnown {
		return []string{"activity age unknown"}
	}
	return []string{"last activity " + humanAge(time.Since(a.Mtime)) + " ago"}
}

// uniqueID builds a stable per-artifact id: projects.<rel-to-project>, with a
// short path hash appended when two candidates would otherwise collide.
func uniqueID(a Artifact, seen map[string]bool) string {
	base := relID(a.Project, a.Path)
	id := ID + "." + base
	if seen[id] {
		id = ID + "." + base + "-" + shortHash(a.Path)
	}
	seen[id] = true
	return id
}

// relID renders the artifact path relative to its project as an id segment.
func relID(project, path string) string {
	rel, err := filepath.Rel(project, path)
	if err != nil || rel == "" || rel == "." {
		return sanitize(filepath.Base(path))
	}
	return sanitize(rel)
}

func sanitize(s string) string {
	return strings.NewReplacer("/", "-", "\\", "-").Replace(s)
}

func shortHash(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:4])
}

// Roots expands the configured project roots ([projects] paths) against the
// invoking user's home, resolves symlinks, and de-duplicates them. Discovery
// reports artifacts at their physical path, so the deletion allow-list must
// use the same resolved roots or a symlinked project root (for example
// ~/code -> /data/code) would be rejected as outside the allow-list. A root
// that cannot be resolved (for example, one that does not exist yet) keeps its
// lexical form; discovery warns about it and skips it.
func Roots(env *engine.Env) []string {
	if env == nil || env.Cfg == nil {
		return nil
	}
	var roots []string
	seen := map[string]bool{}
	for _, p := range env.Cfg.Purge.Paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		abs := config.ExpandHome(p, env.Home)
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(env.Home, abs)
		}
		abs = filepath.Clean(abs)
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = filepath.Clean(resolved)
		}
		if seen[abs] {
			continue
		}
		seen[abs] = true
		roots = append(roots, abs)
	}
	return roots
}

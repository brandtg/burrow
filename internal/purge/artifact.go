// Package purge discovers rebuildable project artifacts (node_modules, Rust
// target, virtualenvs, __pycache__, ...) under configured project roots and
// moves them to the Trash after proving they are safe to remove.
//
// Safety: discovery is read-only (invariant I1). An artifact is kept (never
// purged) when it is git-tracked, when git state cannot be determined, when it
// is or contains a git repository, when its activity age cannot be verified, or
// when it has seen recent activity. Deletion flows through internal/trash with
// a proper .trashinfo entry (recoverable by default; no permanent-delete
// primitive) and is journaled with action "trash" and recovery "trash".
//
// Provenance: behavioral port of Mole's purge rule (git-tracked-file
// protection, nested-repo protection, age filter). Reimplemented clean-room; no
// Mole source was copied (Mole is GPL-3.0).
package purge

import (
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/safe"
)

// DefaultArtifactDirs is the pinned v0.1 artifact list (spec §11). It is
// extensible via [purge] artifact_dirs in config.
var DefaultArtifactDirs = []string{
	"node_modules",
	"target",
	".venv",
	"venv",
	"__pycache__",
	".next/cache",
	"build",
	"dist",
	".gradle",
	"_cacache",
}

// DefaultMaxDepth bounds how far discovery descends below each project root.
const DefaultMaxDepth = 6

// DefaultActiveWithin is the fallback window for the active-project guard: an
// artifact in a repository committed within this window is kept regardless of
// the artifact's own age.
const DefaultActiveWithin = 30 * 24 * time.Hour

// markerRequired names the generic artifact directories whose name alone is not
// proof of a project build output. A candidate matches only when its parent
// holds an ecosystem marker (see markerFiles). Unambiguous names such as
// node_modules or __pycache__ are not listed.
var markerRequired = map[string]bool{
	"build":   true,
	"dist":    true,
	"target":  true,
	".gradle": true,
}

// markerFiles are the project files that justify a generic artifact directory.
var markerFiles = []string{
	"package.json",
	"Cargo.toml",
	"pyproject.toml",
	"setup.py",
	"setup.cfg",
	"go.mod",
	"pom.xml",
	"build.gradle",
	"build.gradle.kts",
	"settings.gradle",
	"settings.gradle.kts",
	"CMakeLists.txt",
	"meson.build",
	"Makefile",
}

// requiresMarker reports whether pattern must be justified by a project marker.
func requiresMarker(pattern string) bool { return markerRequired[pattern] }

// hasMarker reports whether dir (the artifact's parent) holds an ecosystem
// marker file.
func hasMarker(dir string) bool {
	for _, name := range markerFiles {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
			return true
		}
	}
	return false
}

// Artifact is one discovered rebuildable artifact directory.
type Artifact struct {
	// Path is the absolute artifact directory.
	Path string
	// Type is the matched artifact pattern (for example "node_modules").
	Type string
	// Project groups artifacts in the UI: the owning git repository root when
	// the artifact is inside a repo, otherwise the configured scan root.
	Project string
	// RepoRoot is the owning git repository root, or "" when not in one.
	RepoRoot string
	// Size is on-disk usage in bytes.
	Size int64
	// SizeKnown is false when the tree could not be measured.
	SizeKnown bool
	// Mtime is the newest modification time found anywhere in the tree.
	Mtime time.Time
	// AgeKnown is false when mtime could not be established.
	AgeKnown bool
	// Tracked reports whether the artifact is git-tracked.
	Tracked bool
	// KeepReason, when non-empty, means the artifact must not be purged.
	KeepReason string
	// Snapshot is the identity captured during discovery for TOCTOU defense.
	Snapshot *safe.Snapshot
}

// Kept reports whether the artifact is protected from purging.
func (a Artifact) Kept() bool { return a.KeepReason != "" }

// Age returns the time since the newest activity in the tree. ok is false when
// the age could not be established.
func (a Artifact) Age(now time.Time) (time.Duration, bool) {
	if !a.AgeKnown {
		return 0, false
	}
	return now.Sub(a.Mtime), true
}

// matchArtifact returns the matching pattern for a directory whose path
// relative to the scan root is rel. A single-segment pattern matches the
// directory base name at any depth; a multi-segment pattern (for example
// ".next/cache") matches a path suffix. Regular files never match because
// callers only pass directories.
func matchArtifact(rel string, patterns []string) (string, bool) {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	base := path.Base(rel)
	for _, p := range patterns {
		p = strings.Trim(strings.TrimSpace(p), "/")
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			if base == p {
				return p, true
			}
			continue
		}
		if rel == p || strings.HasSuffix(rel, "/"+p) {
			return p, true
		}
	}
	return "", false
}

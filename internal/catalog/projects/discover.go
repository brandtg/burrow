package projects

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/brandtg/burrow/internal/owner"
)

// Options controls discovery.
type Options struct {
	// Roots are absolute project roots to scan. Missing roots are skipped.
	Roots []string
	// ArtifactDirs are the directory patterns to match. Empty uses
	// DefaultArtifactDirs.
	ArtifactDirs []string
	// MaxDepth bounds descent below each root. Zero uses DefaultMaxDepth.
	MaxDepth int
	// MaxAge is the inactivity window. Artifacts with activity newer than this
	// are kept. Zero disables the age filter.
	MaxAge time.Duration
	// ActiveWithin keeps an artifact whose owning repository saw a commit within
	// this window, regardless of artifact age. Zero disables the guard.
	ActiveWithin time.Duration
	// Now is the reference time for the age filter. Zero uses time.Now.
	Now time.Time
	// Git is the git query implementation. Nil uses ExecGit.
	Git Git
	// GitTimeout bounds one git invocation. Zero uses DefaultGitTimeout.
	GitTimeout time.Duration
	// ScanTimeout bounds measuring one artifact tree. Zero uses
	// owner.TimeoutScanPerRoot.
	ScanTimeout time.Duration
	// Warn, when set, receives a message for every root that is skipped
	// (missing, unreadable, or not a directory). Discovery never aborts on a
	// bad root; the warning is the only signal.
	Warn func(string)
}

func (o Options) withDefaults() Options {
	if len(o.ArtifactDirs) == 0 {
		o.ArtifactDirs = DefaultArtifactDirs
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxDepth
	}
	if o.Git == nil {
		o.Git = ExecGit{}
	}
	if o.GitTimeout <= 0 {
		o.GitTimeout = DefaultGitTimeout
	}
	if o.ScanTimeout <= 0 {
		o.ScanTimeout = owner.TimeoutScanPerRoot
	}
	return o
}

// Discover scans every configured root and returns the candidate artifacts,
// both purgeable and kept. It is read-only (invariant I1).
func Discover(ctx context.Context, opts Options) ([]Artifact, error) {
	opts = opts.withDefaults()
	git := newGitCache(opts.Git, opts.GitTimeout)
	var out []Artifact
	for _, root := range opts.Roots {
		arts, err := scanRoot(ctx, root, opts, git)
		if err != nil {
			// A root that does not exist can never contribute; the stock
			// config ships several optional roots, so their absence is not
			// a condition worth alarming the user about. A root that exists
			// but cannot be read still warns.
			if !errors.Is(err, fs.ErrNotExist) && opts.Warn != nil {
				opts.Warn(fmt.Sprintf("skipping root %s: %v", root, err))
			}
			continue
		}
		out = append(out, arts...)
	}
	sortArtifacts(out)
	return out, nil
}

// scanRoot walks one project root, pruning matched artifact directories and
// .git so discovery stays bounded.
func scanRoot(ctx context.Context, root string, opts Options, git *gitCache) ([]Artifact, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	resolved = filepath.Clean(resolved)
	fi, err := os.Lstat(resolved)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("purge: root is not a directory")
	}

	var out []Artifact
	walkErr := filepath.WalkDir(resolved, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == resolved {
				// The root itself is unreadable — surface it so Discover
				// can warn; child errors stay silent.
				return err
			}
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p == resolved {
			return nil
		}
		if d.Name() == ".git" {
			return filepath.SkipDir
		}
		rel, rerr := filepath.Rel(resolved, p)
		if rerr != nil {
			return filepath.SkipDir
		}
		depth := strings.Count(filepath.ToSlash(rel), "/") + 1
		if depth > opts.MaxDepth {
			return filepath.SkipDir
		}
		pattern, ok := matchArtifact(rel, opts.ArtifactDirs)
		if !ok {
			return nil
		}
		out = append(out, classify(ctx, p, pattern, resolved, opts, git))
		return filepath.SkipDir
	})
	if walkErr != nil {
		return out, walkErr
	}
	return out, nil
}

// classify measures one artifact and applies every protection rule. Any
// uncertainty sets KeepReason (fail closed).
func classify(ctx context.Context, p, pattern, root string, opts Options, git *gitCache) Artifact {
	a := Artifact{Path: p, Type: pattern, Project: root}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}

	size, newest, hasGit, ok := measure(ctx, p, opts.ScanTimeout)
	if !ok {
		a.KeepReason = firstReason(a.KeepReason, "cannot measure artifact tree")
	} else {
		a.Size = size
		a.SizeKnown = true
		if !newest.IsZero() {
			a.Mtime = newest
			a.AgeKnown = true
		}
	}
	if hasGit {
		a.KeepReason = firstReason(a.KeepReason, "contains a git repository")
	}

	if err := protectGit(ctx, &a, p, git); err != nil {
		a.KeepReason = firstReason(a.KeepReason, "git state unknown: "+err.Error())
	}

	// Active-project guard: a repository committed recently is under active work
	// even if this artifact has not been touched. The guard is advisory; a git
	// error contributes no keep reason (tracked-set remains the hard check).
	if opts.ActiveWithin > 0 && a.RepoRoot != "" {
		if when, err := git.lastCommit(ctx, a.RepoRoot); err == nil && !when.IsZero() {
			if age := now.Sub(when); age >= 0 && age < opts.ActiveWithin {
				a.KeepReason = firstReason(a.KeepReason, "active project (last commit "+humanAge(age)+" ago)")
			}
		}
	}

	if opts.MaxAge > 0 {
		if !a.AgeKnown {
			a.KeepReason = firstReason(a.KeepReason, "cannot verify activity age")
		} else if age, _ := a.Age(now); age < opts.MaxAge {
			a.KeepReason = firstReason(a.KeepReason, "recent activity")
		}
	}
	return a
}

// humanAge renders a duration as whole days when a day or more, else hours.
func humanAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if days := int(d.Hours() / 24); days > 0 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

// protectGit keeps a candidate that is git-tracked, and sets the project
// grouping to the owning repository. A git failure other than "not a repo" is
// returned so the caller keeps the artifact (unknown state).
func protectGit(ctx context.Context, a *Artifact, p string, git *gitCache) error {
	repo, err := git.repoRoot(ctx, filepath.Dir(p))
	if err != nil {
		if errors.Is(err, ErrNoRepo) {
			return nil
		}
		return err
	}
	a.RepoRoot = repo
	a.Project = repo
	set, err := git.trackedSet(ctx, repo)
	if err != nil {
		return err
	}
	rel, rerr := filepath.Rel(repo, p)
	if rerr != nil {
		return rerr
	}
	if set.trackedDir(rel) {
		a.Tracked = true
		a.KeepReason = firstReason(a.KeepReason, "git-tracked")
	}
	return nil
}

// measure walks one artifact tree once, returning on-disk size, the newest
// mtime, and whether the tree contains a .git entry. Symlinks are not followed.
// Any unreadable entry or timeout makes the result unreliable (ok=false) so the
// caller keeps the artifact.
func measure(ctx context.Context, root string, timeout time.Duration) (size int64, newest time.Time, hasGit, ok bool) {
	if timeout <= 0 {
		timeout = owner.TimeoutScanPerRoot
	}
	mctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ok = true
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if mctx.Err() != nil {
			return mctx.Err()
		}
		if walkErr != nil {
			return walkErr
		}
		if d.Name() == ".git" {
			hasGit = true
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		if st, sok := info.Sys().(*syscall.Stat_t); sok {
			size += st.Blocks * 512
		}
		if mt := info.ModTime(); mt.After(newest) {
			newest = mt
		}
		return nil
	})
	if err != nil {
		return size, newest, hasGit, false
	}
	return size, newest, hasGit, true
}

func firstReason(existing, reason string) string {
	if existing != "" {
		return existing
	}
	return reason
}

func sortArtifacts(a []Artifact) {
	sort.SliceStable(a, func(i, j int) bool {
		if a[i].Project != a[j].Project {
			return a[i].Project < a[j].Project
		}
		return a[i].Path < a[j].Path
	})
}

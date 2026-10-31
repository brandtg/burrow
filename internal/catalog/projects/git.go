package projects

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/owner"
)

// ErrNoRepo reports that a directory is not inside a git repository.
var ErrNoRepo = errors.New("purge: not a git repository")

// DefaultGitTimeout bounds one git invocation, taken from the central timeout
// table's quick-detect budget.
const DefaultGitTimeout = owner.TimeoutQuickDetect

// Git abstracts the git queries purge needs so tests can simulate tracked,
// untracked, and unknown states without invoking the git binary.
type Git interface {
	// TopLevel returns the repository root that owns dir. It returns ErrNoRepo
	// when dir is not inside a repository; any other error means the state is
	// unknown and callers must fail closed.
	TopLevel(ctx context.Context, dir string) (string, error)
	// LsFiles returns tracked file paths relative to repoRoot, using forward
	// slashes.
	LsFiles(ctx context.Context, repoRoot string) ([]string, error)
	// LastCommit returns the committer time of repoRoot's most recent commit.
	// An error (including a repository with no commits) means the active-project
	// guard has no signal; callers must not treat it as a keep.
	LastCommit(ctx context.Context, repoRoot string) (time.Time, error)
}

// ExecGit is the default Git implementation, backed by the git binary. Each
// call is bounded by Timeout; a timeout is reported as an error so callers keep
// the artifact (fail closed).
type ExecGit struct {
	// Timeout bounds each git invocation. Zero uses DefaultGitTimeout.
	Timeout time.Duration
}

func (g ExecGit) timeout() time.Duration {
	if g.Timeout <= 0 {
		return DefaultGitTimeout
	}
	return g.Timeout
}

// TopLevel runs `git -C dir rev-parse --show-toplevel`.
func (g ExecGit) TopLevel(ctx context.Context, dir string) (string, error) {
	out, err := runGit(ctx, g.timeout(), dir, "rev-parse", "--show-toplevel")
	if err != nil {
		if isNotARepo(err) {
			return "", ErrNoRepo
		}
		return "", err
	}
	root := strings.TrimSpace(string(out))
	if root == "" {
		return "", ErrNoRepo
	}
	return filepath.Clean(root), nil
}

// LsFiles runs `git -C repoRoot ls-files -z`.
func (g ExecGit) LsFiles(ctx context.Context, repoRoot string) ([]string, error) {
	out, err := runGit(ctx, g.timeout(), repoRoot, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(string(out), "\x00")
	files := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			files = append(files, p)
		}
	}
	return files, nil
}

// LastCommit runs `git -C repoRoot log -1 --format=%ct`.
func (g ExecGit) LastCommit(ctx context.Context, repoRoot string) (time.Time, error) {
	out, err := runGit(ctx, g.timeout(), repoRoot, "log", "-1", "--format=%ct")
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(string(out))
	if s == "" {
		return time.Time{}, errors.New("purge: git log returned no commit time")
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(secs, 0), nil
}

// gitError carries stderr so a "not a repository" message can be told apart
// from a real failure (missing binary, timeout, corrupt repo).
type gitError struct {
	err    error
	stderr string
}

func (e *gitError) Error() string {
	msg := strings.TrimSpace(e.stderr)
	if msg == "" {
		return e.err.Error()
	}
	return msg
}

func (e *gitError) Unwrap() error { return e.err }

func isNotARepo(err error) bool {
	var ge *gitError
	if errors.As(err, &ge) {
		return strings.Contains(ge.stderr, "not a git repository")
	}
	return false
}

// runGit executes git with a timeout and no shell. A missing git binary or a
// timeout is returned as a plain error (unknown state, fail closed).
func runGit(ctx context.Context, timeout time.Duration, dir string, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{"-C", dir}, args...)
	cmd := exec.CommandContext(cctx, bin, full...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if cctx.Err() == context.DeadlineExceeded {
		return stdout.Bytes(), context.DeadlineExceeded
	}
	if err != nil {
		return stdout.Bytes(), &gitError{err: err, stderr: stderr.String()}
	}
	return stdout.Bytes(), nil
}

// findGitMarker walks up from dir and returns the first directory containing a
// .git entry (a directory for a normal repo, a file for a worktree or
// submodule). It is a cheap pre-check that avoids invoking git for directories
// that are plainly not repositories.
func findGitMarker(dir string) (string, bool) {
	dir = filepath.Clean(dir)
	for {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// trackedSet indexes git-tracked files and every directory that contains one,
// so a candidate directory can be classified in constant time.
type trackedSet struct {
	files map[string]bool
	dirs  map[string]bool
}

func buildTrackedSet(files []string) trackedSet {
	s := trackedSet{
		files: make(map[string]bool, len(files)),
		dirs:  make(map[string]bool),
	}
	for _, f := range files {
		f = strings.TrimPrefix(filepath.ToSlash(f), "./")
		f = strings.Trim(f, "/")
		if f == "" {
			continue
		}
		s.files[f] = true
		for dir := path.Dir(f); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
			s.dirs[dir] = true
			if path.Dir(dir) == dir {
				break
			}
		}
	}
	return s
}

// trackedDir reports whether any tracked file lives at or under rel.
func (s trackedSet) trackedDir(rel string) bool {
	rel = strings.Trim(filepath.ToSlash(rel), "/")
	if rel == "" || rel == "." {
		return false
	}
	return s.dirs[rel] || s.files[rel]
}

// gitCache bounds git work to one top-level lookup and one tracked-set listing
// per repository per run.
type gitCache struct {
	git     Git
	timeout time.Duration

	topLevel map[string]topResult
	tracked  map[string]trackedResult
	commit   map[string]commitResult
}

type topResult struct {
	root string
	err  error
}

type trackedResult struct {
	set trackedSet
	err error
}

type commitResult struct {
	when time.Time
	err  error
}

func newGitCache(g Git, timeout time.Duration) *gitCache {
	if timeout <= 0 {
		timeout = DefaultGitTimeout
	}
	return &gitCache{
		git:      g,
		timeout:  timeout,
		topLevel: map[string]topResult{},
		tracked:  map[string]trackedResult{},
		commit:   map[string]commitResult{},
	}
}

// repoRoot returns the owning repository root for dir, or ErrNoRepo when dir is
// not inside one.
func (c *gitCache) repoRoot(ctx context.Context, dir string) (string, error) {
	marker, ok := findGitMarker(dir)
	if !ok {
		return "", ErrNoRepo
	}
	if r, cached := c.topLevel[marker]; cached {
		return r.root, r.err
	}
	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	root, err := c.git.TopLevel(cctx, marker)
	if err == nil {
		root = filepath.Clean(root)
	}
	c.topLevel[marker] = topResult{root: root, err: err}
	return root, err
}

// tracked returns the cached tracked set for repoRoot.
func (c *gitCache) trackedSet(ctx context.Context, repoRoot string) (trackedSet, error) {
	if r, cached := c.tracked[repoRoot]; cached {
		return r.set, r.err
	}
	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	files, err := c.git.LsFiles(cctx, repoRoot)
	if err != nil {
		c.tracked[repoRoot] = trackedResult{err: err}
		return trackedSet{}, err
	}
	set := buildTrackedSet(files)
	c.tracked[repoRoot] = trackedResult{set: set}
	return set, nil
}

// lastCommit returns the cached most-recent commit time for repoRoot.
func (c *gitCache) lastCommit(ctx context.Context, repoRoot string) (time.Time, error) {
	if r, cached := c.commit[repoRoot]; cached {
		return r.when, r.err
	}
	cctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	when, err := c.git.LastCommit(cctx, repoRoot)
	c.commit[repoRoot] = commitResult{when: when, err: err}
	return when, err
}

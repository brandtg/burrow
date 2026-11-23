package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/whitelist"
)

// fakeGit simulates git for tracked/unknown tests.
type fakeGit struct {
	root      string
	tracked   []string
	err       error
	commit    time.Time
	commitErr error
}

func (f fakeGit) TopLevel(context.Context, string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.root, nil
}

func (f fakeGit) LsFiles(context.Context, string) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.tracked, nil
}

func (f fakeGit) LastCommit(context.Context, string) (time.Time, error) {
	if f.commitErr != nil {
		return time.Time{}, f.commitErr
	}
	return f.commit, nil
}

func testEnv(t *testing.T, roots ...string) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	for _, d := range []string{home, filepath.Join(dir, "data")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	cfg.Purge.Paths = roots
	return &engine.Env{
		Home:      home,
		XDGCache:  filepath.Join(dir, "cache"),
		XDGConfig: filepath.Join(dir, "config"),
		XDGData:   filepath.Join(dir, "data"),
		XDGState:  filepath.Join(dir, "state"),
		Cfg:       cfg,
		Whitelist: &whitelist.Set{},
		Paths: config.Paths{
			Journal:  filepath.Join(dir, "operations.jsonl"),
			TrashDir: filepath.Join(dir, "data", "Trash"),
		},
	}
}

// makeArtifact creates root/<name>/file with content and an old mtime, so it
// passes every age window.
func makeArtifact(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "file.txt"), []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-30 * 24 * time.Hour)
	err := filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(path, when, when)
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func discover(t *testing.T, cat *Category, env *engine.Env) []engine.Target {
	t.Helper()
	targets, err := cat.Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	return targets
}

func TestDiscoverGroupsPerProjectAndTrashes(t *testing.T) {
	root := t.TempDir()
	art := makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	cat := New()

	targets := discover(t, cat, env)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d: %+v", len(targets), targets)
	}
	got := targets[0]
	if got.Category != ID {
		t.Fatalf("category = %q, want %q", got.Category, ID)
	}
	if got.Recovery != engine.RecoveryTrash {
		t.Fatalf("recovery = %v, want trash", got.Recovery)
	}
	if got.Action == nil || got.Action.Recovery() != engine.RecoveryTrash {
		t.Fatalf("action must carry the trash contract: %+v", got.Action)
	}
	if got.Group != root || got.GroupTitle != root {
		t.Fatalf("group = %q/%q, want %q/%q", got.Group, got.GroupTitle, root, root)
	}
	if got.Display != "node_modules" {
		t.Fatalf("display = %q, want node_modules", got.Display)
	}
	if !got.SizeKnown || got.SizeBytes <= 0 {
		t.Fatalf("artifact size must be measured: %+v", got)
	}
	if err := got.Validate(); err != nil {
		t.Fatalf("target must validate: %v", err)
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("discovery must not mutate: %v", err)
	}
}

func TestDiscoverRootProjectTitleCollapsesHome(t *testing.T) {
	// An artifact directly in a scan root groups under the root itself; the
	// title must render home-relative, not repeat the category name.
	env := testEnv(t)
	root := filepath.Join(env.Home, "Projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules")
	env.Cfg.Purge.Paths = []string{root}

	targets := discover(t, New(), env)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d: %+v", len(targets), targets)
	}
	if got := targets[0].GroupTitle; got != "~/Projects" {
		t.Fatalf("group title = %q, want ~/Projects", got)
	}
}

func TestDiscoverOmitsKeptArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	cat := &Category{git: fakeGit{root: root, tracked: []string{"node_modules/file.txt"}}}

	targets := discover(t, cat, env)
	if len(targets) != 0 {
		t.Fatalf("a git-tracked artifact must produce no target, got %+v", targets)
	}
	// The keep decision is not silent: the category note carries the tally so
	// the bulk of a machine's footprint is visible even though these
	// artifacts never become rows.
	note := cat.PreviewNote()
	if !strings.Contains(note, "Kept by policy:") || !strings.Contains(note, "1 artifact") {
		t.Fatalf("kept-by-policy tally missing from note: %q", note)
	}
}

// TestDiscoverKeptTallyAbsentWhenNothingKept pins that the tally is added
// only when something was actually kept, so an empty workspace note stays
// clean.
func TestDiscoverKeptTallyAbsentWhenNothingKept(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	cat := New()
	discover(t, cat, env)
	if strings.Contains(cat.PreviewNote(), "Kept by policy") {
		t.Fatalf("nothing was kept; tally must be absent: %q", cat.PreviewNote())
	}
}

// mapGit routes TopLevel through a dir -> repo map so one Discover call can
// cover several repositories.
type mapGit struct {
	roots  map[string]string
	commit time.Time
}

func (m mapGit) TopLevel(_ context.Context, dir string) (string, error) {
	if root, ok := m.roots[dir]; ok {
		return root, nil
	}
	return "", ErrNoRepo
}

func (m mapGit) LsFiles(context.Context, string) ([]string, error) { return nil, nil }

func (m mapGit) LastCommit(context.Context, string) (time.Time, error) { return m.commit, nil }

// TestDiscoverDisambiguatesCollidingGroupTitles pins the collision fix: two
// repositories both named "logiops" must not render as two identical group
// headers in the preview; each gains a distinguishing parent path, and every
// target of one project shares the same replacement title.
func TestDiscoverDisambiguatesCollidingGroupTitles(t *testing.T) {
	env := testEnv(t)
	root := filepath.Join(env.Home, "Projects")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	env.Cfg.Purge.Paths = []string{root}

	a := makeArtifact(t, filepath.Join(root, "a", "logiops"), "node_modules")
	b := makeArtifact(t, filepath.Join(root, "b", "logiops"), "node_modules")
	// findGitMarker requires a real .git entry before it consults the Git
	// implementation, so each fake repo needs the marker directory.
	for _, repo := range []string{filepath.Dir(a), filepath.Dir(b)} {
		if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	git := mapGit{roots: map[string]string{
		filepath.Dir(a): filepath.Dir(a),
		filepath.Dir(b): filepath.Dir(b),
	}}
	cat := &Category{git: git}

	targets := discover(t, cat, env)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %+v", targets)
	}
	titles := map[string]string{}
	for _, tg := range targets {
		if tg.GroupTitle == "logiops" {
			t.Fatalf("colliding title left undisambiguated: %+v", tg)
		}
		titles[tg.Group] = tg.GroupTitle
	}
	if len(titles) != 2 {
		t.Fatalf("expected two distinct titles, got %v", titles)
	}
	for _, tg := range targets {
		want := titles[tg.Group]
		if tg.GroupTitle != want {
			t.Fatalf("targets of one project must share a title: %q vs %q", tg.GroupTitle, want)
		}
	}
}

func TestDiscoverUnknownGitStateKeepsArtifacts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	cat := &Category{git: fakeGit{err: errors.New("git unavailable")}}

	if targets := discover(t, cat, env); len(targets) != 0 {
		t.Fatalf("unknown git state must keep the artifact (fail closed), got %+v", targets)
	}
}

func TestDiscoverActiveProjectKept(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	cat := &Category{git: fakeGit{root: root, commit: time.Now().Add(-24 * time.Hour)}}

	if targets := discover(t, cat, env); len(targets) != 0 {
		t.Fatalf("artifact in a recently-committed repo must be kept, got %+v", targets)
	}
}

func TestDiscoverNoRootsConfigured(t *testing.T) {
	env := testEnv(t)
	targets, err := New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 0 {
		t.Fatalf("no roots configured must yield no targets, got %+v", targets)
	}
}

func TestDiscoverWarnsOnMissingRoot(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules")
	missing := filepath.Join(t.TempDir(), "nope")
	env := testEnv(t, missing, root)
	cat := New()

	targets := discover(t, cat, env)
	if len(targets) != 1 {
		t.Fatalf("the good root must still be scanned, got %+v", targets)
	}
	// A missing root is silent; it can never contribute anything.
	for _, w := range cat.Warnings() {
		if strings.Contains(w, missing) {
			t.Fatalf("missing root must not warn, got %v", cat.Warnings())
		}
	}
}

func TestDiscoverIDsUniqueAcrossProjects(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	makeArtifact(t, a, "node_modules")
	makeArtifact(t, b, "node_modules")
	env := testEnv(t, a, b)

	targets := discover(t, New(), env)
	if len(targets) != 2 {
		t.Fatalf("expected 2 targets, got %d", len(targets))
	}
	if targets[0].ID == targets[1].ID {
		t.Fatalf("ids must be unique: %q", targets[0].ID)
	}
	groups := map[string]bool{targets[0].Group: true, targets[1].Group: true}
	if len(groups) != 2 {
		t.Fatalf("each project is its own group, got %v", groups)
	}
}

func TestDiscoverRegenDefaults(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules")
	makeArtifact(t, root, "__pycache__")
	env := testEnv(t, root)

	byType := map[string]engine.Target{}
	for _, tg := range discover(t, New(), env) {
		byType[tg.Display] = tg
	}
	if len(byType) != 2 {
		t.Fatalf("expected node_modules and __pycache__, got %v", byType)
	}
	if byType["node_modules"].Regen != engine.RegenExpensive || byType["node_modules"].DefaultOn {
		t.Fatalf("node_modules must be shown but not pre-selected: %+v", byType["node_modules"])
	}
	if byType["__pycache__"].Regen != engine.RegenFree || !byType["__pycache__"].DefaultOn {
		t.Fatalf("__pycache__ must be free and pre-selected: %+v", byType["__pycache__"])
	}
}

// TestDiscoverGenericNameNeedsNoMarker: with Trash recovery a wrong guess
// costs disk space, not data, so a generic build/dist dir is a candidate like
// any other; the ecosystem-marker gating is gone.
func TestDiscoverGenericNameNeedsNoMarker(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "build")
	env := testEnv(t, root)

	targets := discover(t, New(), env)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target for the unmarked build dir, got %+v", targets)
	}
	if targets[0].Display != "build" {
		t.Fatalf("display = %q, want build", targets[0].Display)
	}
}

func TestDiscoverInvalidWindowFailsClosed(t *testing.T) {
	root := t.TempDir()
	makeArtifact(t, root, "node_modules")
	env := testEnv(t, root)
	env.Cfg.Purge.MaxAge = "bogus"

	targets, err := New().Discover(context.Background(), env)
	if err == nil {
		t.Fatal("an unparseable window must fail the category, not disable it")
	}
	if len(targets) != 0 {
		t.Fatalf("no targets may be produced when the policy is unprovable, got %+v", targets)
	}
}

func TestRootsResolvesSymlinksAndDuplicates(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	env := testEnv(t, link, real, link)

	roots := Roots(env)
	if len(roots) != 1 || roots[0] != real {
		t.Fatalf("roots = %v, want [%s]", roots, real)
	}
}

func TestRootsExpandsHome(t *testing.T) {
	env := testEnv(t, "~/code")
	roots := Roots(env)
	if len(roots) != 1 || roots[0] != filepath.Join(env.Home, "code") {
		t.Fatalf("roots = %v, want under home %s", roots, env.Home)
	}
}

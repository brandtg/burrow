package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/trash"
)

func purgeEnv(t *testing.T, root string) map[string]string {
	t.Helper()
	return map[string]string{
		"XDG_CONFIG_HOME":    t.TempDir(),
		"XDG_DATA_HOME":      t.TempDir(),
		"XDG_STATE_HOME":     t.TempDir(),
		"XDG_CACHE_HOME":     t.TempDir(),
		"BURROW_PURGE_PATHS": root,
	}
}

func makeOldArtifact(t *testing.T, root, name string) string {
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

func TestPurgePathsPrintsRoots(t *testing.T) {
	root := t.TempDir()
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--paths"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), root) {
		t.Fatalf("--paths output missing root %q: %q", root, te.out.String())
	}
}

// stubOwnerTools keeps the alias flow hermetic: discovery of the other
// categories probes owner tools, which must hit the testbin stubs, not real
// ones on the machine.
func stubOwnerTools(t *testing.T) {
	t.Helper()
	useTestbin(t)
}

// TestPurgeAliasNoticeAndFlow pins the fold: `purge` is a thin alias for
// `clean --only projects`. It announces itself on stderr and previews the
// grouped projects plan; a dry run mutates nothing.
func TestPurgeAliasNoticeAndFlow(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.errOut.String(), "deprecated") {
		t.Fatalf("expected a deprecation notice on stderr: %q", te.errOut.String())
	}
	out := te.out.String()
	if !strings.Contains(out, "Projects") || !strings.Contains(out, "node_modules") {
		t.Fatalf("expected the grouped projects preview:\n%s", out)
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("dry-run must not trash: %v", err)
	}
}

// TestPurgeAliasDryRunJSONGrouped checks the stable JSON schema carries the
// per-project grouping (item 1): every projects item names its group.
func TestPurgeAliasDryRunJSONGrouped(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{
		`"command": "clean"`,
		`"dry_run": true`,
		`"category": "projects"`,
		`"recovery": "trash"`,
		`"path": "` + art + `"`,
		`"group": "` + root + `"`,
		`"group_title": "` + filepath.Base(root) + `"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

func TestPurgeAliasRequiresYesWhenNotTTY(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge"); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitUsage, te.errOut.String())
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("refused run must not trash: %v", err)
	}
}

// TestPurgeAliasYesPreviewsBeforeTrashing: --yes still shows the plan before
// executing, and pre-selected items (free/cheap artifacts) are trashed.
func TestPurgeAliasYesPreviewsBeforeTrashing(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "__pycache__")
	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	iPath := strings.Index(out, "__pycache__")
	iFreed := strings.Index(out, "Freed ")
	if iPath < 0 || iFreed < 0 || iPath > iFreed {
		t.Fatalf("preview must precede the summary:\n%s", out)
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact should be trashed away, stat err = %v", err)
	}
}

// TestPurgeAliasYesTrashesAndJournals: the artifact lands in the Trash with a
// trash-contract journal record, so it is restorable (item 3: trashed, never
// "purged").
func TestPurgeAliasYesTrashesAndJournals(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "__pycache__")
	env := purgeEnv(t, root)
	stubOwnerTools(t)
	te := newTestApp(t, env)
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact should be moved away, stat err = %v", err)
	}
	entries, err := trash.New(filepath.Join(env["XDG_DATA_HOME"], "Trash"), os.Getuid()).List()
	if err != nil {
		t.Fatal(err)
	}
	var inTrash bool
	for _, e := range entries {
		if e.Path == art {
			inTrash = true
		}
	}
	if !inTrash {
		t.Fatalf("artifact must be restorable from the Trash: %+v", entries)
	}
	records, err := journal.Read(filepath.Join(env["XDG_STATE_HOME"], "burrow", "operations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range records {
		if r.Action == journal.ActionTrash && r.Path == art {
			found = true
			if r.Recovery != journal.RecoveryTrash {
				t.Fatalf("recovery = %q, want trash", r.Recovery)
			}
		}
	}
	if !found {
		t.Fatalf("no trash journal record for %s: %+v", art, records)
	}
}

func TestPurgeAliasSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	art := makeOldArtifact(t, real, "__pycache__")
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, link))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact under a symlinked root should be trashed, stat err = %v", err)
	}
}

func TestPurgeAliasWarnsOnMissingRoot(t *testing.T) {
	root := t.TempDir()
	makeOldArtifact(t, root, "node_modules")
	missing := filepath.Join(t.TempDir(), "nope")
	env := purgeEnv(t, missing+","+root)
	stubOwnerTools(t)
	te := newTestApp(t, env)
	if code := te.run("purge", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.errOut.String(), "skipping root") {
		t.Fatalf("expected a skipped-root warning, stderr = %q", te.errOut.String())
	}
}

// TestPurgeAliasAgeFlag keeps --age working: it overrides the whole policy, so
// a recently-active artifact becomes a candidate.
func TestPurgeAliasAgeFlag(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	if err := os.Chtimes(art, time.Now(), time.Now()); err != nil {
		t.Fatal(err)
	}
	env := purgeEnv(t, root)
	stubOwnerTools(t)

	te := newTestApp(t, env)
	if code := te.run("purge", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if strings.Contains(te.out.String(), `"path": "`+art+`"`) {
		t.Fatalf("a recently-active artifact must be kept by default:\n%s", te.out.String())
	}

	te = newTestApp(t, env)
	if code := te.run("purge", "--dry-run", "--json", "--age", "0s"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), `"path": "`+art+`"`) {
		t.Fatalf("--age must override the age policy:\n%s", te.out.String())
	}
}

// TestPurgeAliasCuratedDefault: expensive-to-rebuild artifacts (node_modules)
// are shown but not pre-selected, so --yes alone does not trash them — the
// preview TUI is where they get chosen. Free/cheap artifacts go by default.
func TestPurgeAliasCuratedDefault(t *testing.T) {
	root := t.TempDir()
	modules := makeOldArtifact(t, root, "node_modules")
	cache := makeOldArtifact(t, root, "__pycache__")
	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(modules); err != nil {
		t.Fatalf("node_modules must not be pre-selected: %v", err)
	}
	if _, err := os.Lstat(cache); !os.IsNotExist(err) {
		t.Fatalf("__pycache__ should be trashed by default, stat err = %v", err)
	}
}

func TestPurgeAliasJSON(t *testing.T) {
	root := t.TempDir()
	makeOldArtifact(t, root, "node_modules")
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--json", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{`"items"`, `"node_modules"`, `"dry_run": true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json output missing %q: %s", want, out)
		}
	}
}

func TestPurgeAliasKeepsTrackedArtifact(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runGit("init", "-q")
	tracked := filepath.Join(root, "build")
	if err := os.MkdirAll(tracked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tracked, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "build/keep.txt")
	makeOldArtifact(t, root, "build")

	stubOwnerTools(t)
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(tracked); err != nil {
		t.Fatalf("git-tracked artifact must be kept: %v", err)
	}
}

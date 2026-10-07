package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/journal"
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

func TestPurgeDryRunDeletesNothing(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), "dry-run") {
		t.Fatalf("expected a dry-run notice: %q", te.out.String())
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("dry-run must not delete: %v", err)
	}
}

func TestPurgeRequiresYesWhenNotTTY(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge"); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d: %s", code, ExitUsage, te.errOut.String())
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("refused purge must not delete: %v", err)
	}
}

// TestPurgeYesPreviewsBeforeDeleting pins the fix for F5: --yes is a permanent
// delete and must show the candidate listing before executing.
func TestPurgeYesPreviewsBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	iPath := strings.Index(out, art)
	iPurged := strings.Index(out, "purged ")
	if iPath < 0 || iPurged < 0 || iPath > iPurged {
		t.Fatalf("candidate listing must precede the summary:\n%s", out)
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact should be deleted, stat err = %v", err)
	}
}

func TestPurgeYesDeletesAndJournals(t *testing.T) {
	root := t.TempDir()
	art := makeOldArtifact(t, root, "node_modules")
	env := purgeEnv(t, root)
	te := newTestApp(t, env)
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact should be deleted, stat err = %v", err)
	}
	records, err := journal.Read(filepath.Join(env["XDG_STATE_HOME"], "burrow", "operations.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range records {
		if r.Action == journal.ActionPurge && r.Path == art {
			found = true
			if r.Recovery != journal.RecoveryOwner {
				t.Fatalf("recovery = %q, want owner", r.Recovery)
			}
		}
	}
	if !found {
		t.Fatalf("no purge journal record for %s: %+v", art, records)
	}
}

func TestPurgeSymlinkedRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	art := makeOldArtifact(t, real, "node_modules")
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}

	te := newTestApp(t, purgeEnv(t, link))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(art); !os.IsNotExist(err) {
		t.Fatalf("artifact under a symlinked root should be purged, stat err = %v", err)
	}
}

func TestPurgeWarnsOnMissingRoot(t *testing.T) {
	root := t.TempDir()
	makeOldArtifact(t, root, "node_modules")
	missing := filepath.Join(t.TempDir(), "nope")
	env := purgeEnv(t, missing+","+root)
	te := newTestApp(t, env)
	if code := te.run("purge", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.errOut.String(), "skipping root") {
		t.Fatalf("expected a skipped-root warning, stderr = %q", te.errOut.String())
	}
}

func TestPurgeJSON(t *testing.T) {
	root := t.TempDir()
	makeOldArtifact(t, root, "node_modules")
	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--json", "--dry-run"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{`"candidates"`, `"node_modules"`, `"dry_run": true`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json output missing %q: %s", want, out)
		}
	}
}

func TestPurgeKeepsTrackedArtifact(t *testing.T) {
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

	te := newTestApp(t, purgeEnv(t, root))
	if code := te.run("purge", "--yes"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Lstat(tracked); err != nil {
		t.Fatalf("git-tracked artifact must be kept: %v", err)
	}
}

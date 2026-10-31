package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
)

type lockAdapter struct {
	distro.Adapter
	probe distro.LockProbe
}

func (l lockAdapter) LockGuard() distro.LockProbe { return l.probe }

func cleanTestEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"XDG_CONFIG_HOME": t.TempDir(),
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CACHE_HOME":  t.TempDir(),
	}
}

func useTestbin(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testbin"))
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("BURROW_TESTBIN_LOG", logPath)
	t.Setenv("PATH", abs+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func aptAdapter() distro.Adapter {
	return lockAdapter{Adapter: distro.For(distro.Info{ID: "ubuntu"})}
}

func TestCleanDryRunJSON(t *testing.T) {
	logPath := useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "system.pkg-cache", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{`"dry_run": true`, `"system.pkg-cache"`, `"command": "clean"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
	if log := readMaybe(logPath); strings.Contains(log, "clean") {
		t.Fatalf("dry-run mutated: %s", log)
	}
}

func TestCleanYesExecutesExactArgv(t *testing.T) {
	logPath := useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "system.pkg-cache", "--yes", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), `"status": "ok"`) {
		t.Fatalf("expected ok status:\n%s", te.out.String())
	}
	log := readMaybe(logPath)
	if !strings.Contains(log, "apt-get clean") || !strings.Contains(log, "sudo -- ") {
		t.Fatalf("expected elevated apt-get clean, log:\n%s", log)
	}
}

func TestCleanGuardOnlyBlockerExits75(t *testing.T) {
	useTestbin(t)
	lock := filepath.Join(t.TempDir(), "dpkg.lock")
	f, err := os.OpenFile(lock, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = lockAdapter{
		Adapter: distro.For(distro.Info{ID: "ubuntu"}),
		probe:   distro.LockProbe{LockFiles: []string{lock}},
	}
	if code := te.run("clean", "--only", "system.pkg-cache", "--yes", "--json"); code != ExitGuard {
		t.Fatalf("exit = %d, want %d: %s", code, ExitGuard, te.out.String())
	}
	if !strings.Contains(te.out.String(), `"guard_stops": 1`) {
		t.Fatalf("expected a guard stop:\n%s", te.out.String())
	}
}

func TestCleanRequiresConfirmation(t *testing.T) {
	useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "system.pkg-cache"); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(te.out.String(), "re-run with --yes to execute") {
		t.Fatalf("expected the re-run hint, got %q", te.out.String())
	}
}

func TestCleanWhitelistFlag(t *testing.T) {
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--whitelist"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), "whitelist is empty") {
		t.Fatalf("output = %q", te.out.String())
	}
}

func TestCleanWhitelistPrintsEntries(t *testing.T) {
	cfg := t.TempDir()
	wlPath := filepath.Join(cfg, "burrow", "whitelist")
	if err := os.MkdirAll(filepath.Dir(wlPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wlPath, []byte("# keep\n/home/u/.cache/keep\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": cfg,
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CACHE_HOME":  t.TempDir(),
	})
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--whitelist"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), "/home/u/.cache/keep") {
		t.Fatalf("output = %q", te.out.String())
	}
}

func TestCleanExitCodes(t *testing.T) {
	cases := []struct {
		name string
		sum  engine.Summary
		want int
	}{
		{"nothing", engine.Summary{}, ExitOK},
		{"ok", engine.Summary{OK: 1}, ExitOK},
		{"partial", engine.Summary{OK: 1, Failed: 1}, ExitPartial},
		{"guard-only", engine.Summary{Skipped: 1, GuardStops: 1}, ExitGuard},
		{"timeout-only", engine.Summary{Skipped: 1, Timeouts: 1}, ExitTimeout},
		{"interrupted", engine.Summary{Interrupted: true}, ExitInterrupt},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCode(cleanExit(&tc.sum)); got != tc.want {
				t.Fatalf("exit = %d, want %d", got, tc.want)
			}
		})
	}
}

func readMaybe(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func TestCleanContainersDryRunJSON(t *testing.T) {
	logPath := useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "containers", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{
		`"dry_run": true`,
		`"command": "clean"`,
		`"containers.docker-build-cache"`,
		`"containers.docker-all"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
	if log := readMaybe(logPath); strings.Contains(log, "prune") {
		t.Fatalf("dry-run mutated: %s", log)
	}
}

func TestCleanDesktopPkgsDryRunJSON(t *testing.T) {
	useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "desktop-pkgs", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{
		`"flatpak.unused"`,
		`"snap.old-revisions.core20_2015"`,
		`"2015"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

func TestCleanPlainDryRunPrintsPreview(t *testing.T) {
	useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "system.pkg-cache", "--dry-run", "--plain"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	if !strings.Contains(out, "package cache") || !strings.Contains(out, "nothing was changed (dry run)") {
		t.Fatalf("plain dry-run should print the preview and stop:\n%s", out)
	}
	if strings.Contains(out, "will run") {
		t.Fatalf("plan-only output must not claim commands will run:\n%s", out)
	}
}

// TestBareBurrowIsTheCleanFlow pins the product shape: no subcommand runs the
// full clean flow. On a non-TTY it prints the preview and stops with a re-run
// hint, so a bare invocation can never mutate anything and exits 0.
func TestBareBurrowIsTheCleanFlow(t *testing.T) {
	useTestbin(t)
	env := cleanTestEnv(t)
	// Keep discovery hermetic: the configured project roots must be a scratch
	// dir, not the developer's real home.
	env["BURROW_PURGE_PATHS"] = t.TempDir()
	te := newTestApp(t, env)
	te.app.Adapter = aptAdapter()
	if code := te.run(); code != ExitOK {
		t.Fatalf("exit = %d, want %d: %s", code, ExitOK, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), "re-run with --yes to execute") {
		t.Fatalf("expected the re-run hint, got %q", te.out.String())
	}
}

// TestBareBurrowDryRunPlainGroupsProjects: the bare flow previews the Projects
// category grouped one row per project and mutates nothing.
func TestBareBurrowDryRunPlainGroupsProjects(t *testing.T) {
	useTestbin(t)
	root := t.TempDir()
	art := makeOldArtifact(t, root, "__pycache__")
	env := cleanTestEnv(t)
	env["BURROW_PURGE_PATHS"] = root
	te := newTestApp(t, env)
	te.app.Adapter = aptAdapter()
	if code := te.run("--dry-run", "--plain"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{"Projects", filepath.Base(root), "__pycache__"} {
		if !strings.Contains(out, want) {
			t.Fatalf("bare dry-run preview missing %q:\n%s", want, out)
		}
	}
	if _, err := os.Lstat(art); err != nil {
		t.Fatalf("dry-run must not mutate: %v", err)
	}
}

// TestBareBurrowDryRunJSONSystem: the bare flow with --json is the stable
// machine document, filtered by --only like `clean`.
func TestBareBurrowDryRunJSONSystem(t *testing.T) {
	useTestbin(t)
	env := cleanTestEnv(t)
	env["BURROW_PURGE_PATHS"] = t.TempDir()
	te := newTestApp(t, env)
	te.app.Adapter = aptAdapter()
	if code := te.run("--dry-run", "--json", "--only", "system"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	out := te.out.String()
	for _, want := range []string{`"command": "clean"`, `"dry_run": true`, `"system.pkg-cache"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("json missing %q:\n%s", want, out)
		}
	}
}

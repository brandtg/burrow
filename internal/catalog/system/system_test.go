package system_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/catalog/system"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

// noLockAdapter delegates to the real apt adapter but reports no lock files so
// tests do not touch the host's /var/lib/dpkg locks.
type noLockAdapter struct{ distro.Adapter }

func (noLockAdapter) LockGuard() distro.LockProbe { return distro.LockProbe{} }

func stubEnv(t *testing.T, dryRun bool) *engine.Env {
	return stubEnvFor(t, dryRun, distro.For(distro.Info{ID: "ubuntu"}))
}

func stubEnvFor(t *testing.T, dryRun bool, adapter distro.Adapter) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	return &engine.Env{
		Home:      filepath.Join(dir, "home"),
		XDGCache:  filepath.Join(dir, "cache"),
		XDGData:   filepath.Join(dir, "data"),
		XDGState:  filepath.Join(dir, "state"),
		Run:       owner.New(dryRun),
		Cfg:       config.Default(),
		Adapter:   noLockAdapter{adapter},
		Whitelist: &whitelist.Set{},
		Paths: config.Paths{
			Journal:  filepath.Join(dir, "operations.jsonl"),
			TrashDir: filepath.Join(dir, "Trash"),
		},
		DryRun: dryRun,
	}
}

func repoTestbin(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "..", "testbin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("testbin not found: %v", err)
	}
	return abs
}

func useTestbin(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("BURROW_TESTBIN_LOG", logPath)
	t.Setenv("PATH", repoTestbin(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func TestDnfOldKernelsHiddenWithoutPackageCleanup(t *testing.T) {
	// package-cleanup ships in dnf-utils, not base dnf. With a PATH that has
	// dnf/rpm/uname but no package-cleanup, the old-kernels item must be
	// skipped rather than offered.
	bin := t.TempDir()
	for _, name := range []string{"dnf", "rpm", "uname"} {
		if err := os.Symlink(filepath.Join(repoTestbin(t), name), filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	t.Setenv("BURROW_TESTBIN_LOG", filepath.Join(t.TempDir(), "argv.log"))

	env := stubEnvFor(t, true, distro.For(distro.Info{ID: "fedora"}))
	targets, err := system.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	for _, tg := range targets {
		if tg.ID == "system.old-kernels" {
			t.Fatal("system.old-kernels must be hidden when package-cleanup is absent")
		}
	}
}

func TestDiscoverFindsDnfItems(t *testing.T) {
	useTestbin(t)
	env := stubEnvFor(t, true, distro.For(distro.Info{ID: "fedora"}))
	targets, err := system.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]engine.Target{}
	for _, tg := range targets {
		byID[tg.ID] = tg
	}
	cache, ok := byID["system.pkg-cache"]
	if !ok {
		t.Fatalf("missing system.pkg-cache; got %v", keys(byID))
	}
	if got := cache.Action.(engine.ArgvPlanner).PlannedArgv(); !strings.HasSuffix(strings.Join(got, " "), "dnf clean all") {
		t.Fatalf("dnf pkg-cache argv = %v", got)
	}
	if _, ok := byID["system.orphans"]; !ok {
		t.Fatalf("missing system.orphans; got %v", keys(byID))
	}
	if _, ok := byID["system.old-kernels"]; !ok {
		t.Fatalf("missing system.old-kernels; got %v", keys(byID))
	}
}

func TestDiscoverFindsAptItems(t *testing.T) {
	useTestbin(t)
	env := stubEnv(t, true)
	targets, err := system.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]engine.Target{}
	for _, tg := range targets {
		byID[tg.ID] = tg
	}
	cache, ok := byID["system.pkg-cache"]
	if !ok {
		t.Fatalf("missing system.pkg-cache; got %v", keys(byID))
	}
	if cache.Recovery != engine.RecoveryOwner || !cache.NeedsRoot || cache.Risk != engine.RiskLow || !cache.DefaultOn {
		t.Fatalf("pkg-cache contract wrong: %+v", cache)
	}
	jd, ok := byID["system.journald"]
	if !ok {
		t.Fatalf("missing system.journald; got %v", keys(byID))
	}
	if !jd.SizeKnown || jd.SizeBytes != 512*1024*1024 {
		t.Fatalf("journald size = %d known=%v", jd.SizeBytes, jd.SizeKnown)
	}
}

func TestExecuteRunsExactDnfArgv(t *testing.T) {
	logPath := useTestbin(t)
	env := stubEnvFor(t, false, distro.For(distro.Info{ID: "fedora"}))
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{system.New()},
		Only:       []string{"system.pkg-cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || !plan.Items[0].Selected {
		t.Fatalf("expected one selected pkg-cache item, got %d", len(plan.Items))
	}
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.OK != 1 {
		t.Fatalf("expected one ok item, got %+v", sum)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "sudo -- ") || !strings.Contains(log, "dnf clean all") {
		t.Fatalf("expected elevated dnf clean all, log:\n%s", log)
	}
}

func TestExecuteRunsExactAptArgv(t *testing.T) {
	logPath := useTestbin(t)
	env := stubEnv(t, false)
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{system.New()},
		Only:       []string{"system.pkg-cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 1 || !plan.Items[0].Selected {
		t.Fatalf("expected one selected pkg-cache item, got %d", len(plan.Items))
	}
	jw, err := journal.NewWriter(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, jw)
	if sum.OK != 1 {
		t.Fatalf("expected one ok item, got %+v", sum)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "sudo -- ") || !strings.Contains(log, "apt-get clean") {
		t.Fatalf("expected elevated apt-get clean, log:\n%s", log)
	}
	records, err := journal.Read(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 3 {
		t.Fatalf("expected start + item + end records, got %d", len(records))
	}
}

func TestDryRunProducesPlanWithZeroMutations(t *testing.T) {
	logPath := useTestbin(t)
	env := stubEnv(t, true)
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{system.New()},
		Only:       []string{"system.pkg-cache"},
	})
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.OK != 0 {
		t.Fatalf("dry-run must not report ok, got %+v", sum)
	}
	// Read-only probes may run; no mutating command may.
	if log := readFile(t, logPath); strings.Contains(log, "clean") || strings.Contains(log, "sudo --") {
		t.Fatalf("dry-run executed a mutating command:\n%s", log)
	}
}

func keys(m map[string]engine.Target) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

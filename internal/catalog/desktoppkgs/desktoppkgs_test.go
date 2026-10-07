package desktoppkgs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

// snapListStub reports two disabled revisions and one active snap.
const snapListStub = `#!/bin/sh
case "$1" in
list)
  echo "Name    Version   Rev   Tracking   Publisher   Notes"
  echo "core20  20240111  2015  latest/stable  canonical  disabled"
  echo "core22  20240201  1234  latest/stable  canonical  disabled"
  echo "firefox 120       5678  latest/stable  mozilla    -"
  ;;
esac
`

func testEnv(t *testing.T, dryRun bool) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	return &engine.Env{
		Home:      home,
		XDGCache:  filepath.Join(dir, "cache"),
		XDGData:   filepath.Join(dir, "data"),
		XDGState:  filepath.Join(dir, "state"),
		Run:       owner.New(dryRun),
		Cfg:       config.Default(),
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

func useTestbin(t *testing.T) {
	t.Helper()
	t.Setenv("BURROW_TESTBIN_LOG", filepath.Join(t.TempDir(), "argv.log"))
	t.Setenv("PATH", repoTestbin(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func useStubDir(t *testing.T, scripts map[string]string) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range scripts {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BURROW_TESTBIN_LOG", filepath.Join(t.TempDir(), "argv.log"))
	t.Setenv("PATH", dir)
}

// isolateSnap points the snap revision store at a fixture so host snap state
// cannot influence a test.
func isolateSnap(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "snaps")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	old := snapSnapsDir
	snapSnapsDir = dir
	t.Cleanup(func() { snapSnapsDir = old })
	return dir
}

func build(t *testing.T, env *engine.Env, only ...string) *engine.Plan {
	t.Helper()
	plan, err := engine.Build(context.Background(), env, engine.Options{
		Categories: []engine.Category{New()},
		Only:       only,
	})
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func byID(plan *engine.Plan) map[string]*engine.PlannedItem {
	m := map[string]*engine.PlannedItem{}
	for _, it := range plan.Items {
		m[it.Target.ID] = it
	}
	return m
}

func TestFlatpakOwnerItems(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, true)
	plan := build(t, env, "flatpak.unused", "flatpak.repair")
	items := byID(plan)

	unused, ok := items["flatpak.unused"]
	if !ok {
		t.Fatalf("missing flatpak.unused")
	}
	if unused.Target.Risk != engine.RiskLow || !unused.Target.DefaultOn || unused.Target.NeedsRoot {
		t.Fatalf("flatpak.unused contract wrong: %+v", unused.Target)
	}
	if got := strings.Join(unused.Argv, " "); got != "flatpak uninstall --unused -y" {
		t.Fatalf("flatpak.unused argv = %q", got)
	}

	repair, ok := items["flatpak.repair"]
	if !ok {
		t.Fatalf("missing flatpak.repair")
	}
	if repair.Target.Risk != engine.RiskLow || repair.Target.DefaultOn || !repair.Target.NeedsRoot {
		t.Fatalf("flatpak.repair contract wrong: %+v", repair.Target)
	}
	if got := strings.Join(repair.Argv, " "); got != "sudo -- flatpak repair" {
		t.Fatalf("flatpak.repair argv = %q", got)
	}
}

func TestSnapOldRevisionsOneRowPerDisabled(t *testing.T) {
	useStubDir(t, map[string]string{"snap": snapListStub})
	isolateSnap(t)
	env := testEnv(t, true)
	plan := build(t, env, "snap.old-revisions")
	if len(plan.Items) != 2 {
		t.Fatalf("expected one row per disabled revision, got %d", len(plan.Items))
	}
	wantIDs := map[string]bool{
		"snap.old-revisions.core20_2015": true,
		"snap.old-revisions.core22_1234": true,
	}
	seen := map[string]bool{}
	var argvs []string
	for _, it := range plan.Items {
		if !wantIDs[it.Target.ID] {
			t.Fatalf("unexpected revision id %q", it.Target.ID)
		}
		if seen[it.Target.ID] {
			t.Fatalf("duplicate revision id %q", it.Target.ID)
		}
		seen[it.Target.ID] = true
		if it.Target.Risk != engine.RiskMed || !it.Target.DefaultOn || !it.Target.NeedsRoot {
			t.Fatalf("revision contract wrong: %+v", it.Target)
		}
		argvs = append(argvs, strings.Join(it.Argv, " "))
	}
	joined := strings.Join(argvs, "\n")
	for _, want := range []string{
		"sudo -- snap remove --purge core20 --revision 2015",
		"sudo -- snap remove --purge core22 --revision 1234",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestSnapRevisionSizeMeasured(t *testing.T) {
	snaps := isolateSnap(t)
	useStubDir(t, map[string]string{"snap": snapListStub})
	env := testEnv(t, true)
	// snap stores a disabled revision as <name>_<rev>.snap.
	if err := os.WriteFile(filepath.Join(snaps, "core20_2015.snap"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env, "snap.old-revisions")
	it := byID(plan)["snap.old-revisions.core20_2015"]
	if it == nil {
		t.Fatal("core20 revision target not found")
	}
	if !it.Target.SizeKnown || it.Target.SizeBytes <= 0 {
		t.Fatalf("expected a measured revision size, got size=%d known=%v", it.Target.SizeBytes, it.Target.SizeKnown)
	}
}

func TestFlatpakUserdataTrashesOrphan(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, false)
	orphan := filepath.Join(env.Home, ".var", "app", "org.example.Orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(orphan, "data"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env, "flatpak.userdata")
	if len(plan.Items) != 1 {
		t.Fatalf("expected one userdata item, got %d", len(plan.Items))
	}
	if plan.Items[0].Target.Recovery != engine.RecoveryTrash {
		t.Fatalf("expected trash contract, got %v", plan.Items[0].Target.Recovery)
	}
	plan.Items[0].Selected = true
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.OK != 1 {
		t.Fatalf("expected one ok, got %+v", sum)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan dir still present: %v", err)
	}
}

func TestUserdataAbsentWhenAppStateUnknown(t *testing.T) {
	useStubDir(t, map[string]string{"flatpak": "#!/bin/sh\nexit 1\n"})
	env := testEnv(t, true)
	orphan := filepath.Join(env.Home, ".var", "app", "org.example.Orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env)
	for _, it := range plan.Items {
		if it.Target.ID == "flatpak.userdata" {
			t.Fatal("userdata must not be offered when the app inventory is unknown")
		}
	}
}

func TestUserdataGuardSkipsWhenStateChanges(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, false)
	orphan := filepath.Join(env.Home, ".var", "app", "org.example.Orphan")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env, "flatpak.userdata")
	if len(plan.Items) != 1 {
		t.Fatalf("expected one item, got %d", len(plan.Items))
	}
	// The app inventory becomes unknown before execution.
	useStubDir(t, map[string]string{"flatpak": "#!/bin/sh\nexit 1\n"})
	plan.Items[0].Selected = true
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.Failed != 0 || sum.Skipped != 1 || sum.GuardStops != 1 {
		t.Fatalf("expected one guard skip, got %+v", sum)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("guard must not have trashed the dir: %v", err)
	}
}

func TestSnapUserdataTrashesOrphan(t *testing.T) {
	isolateSnap(t)
	useTestbin(t)
	env := testEnv(t, false)
	orphan := filepath.Join(env.Home, "snap", "oldapp")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env, "snap.userdata")
	if len(plan.Items) != 1 {
		t.Fatalf("expected one snap.userdata item, got %d", len(plan.Items))
	}
	u := plan.Items[0]
	if u.Target.Recovery != engine.RecoveryTrash || u.Target.Risk != engine.RiskMed || u.Target.DefaultOn || u.Target.NeedsRoot {
		t.Fatalf("snap.userdata contract wrong: %+v", u.Target)
	}
	u.Selected = true
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.OK != 1 {
		t.Fatalf("expected one ok, got %+v", sum)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("snap data dir still present: %v", err)
	}
}

func TestSnapUserdataAbsentWhenAppStateUnknown(t *testing.T) {
	isolateSnap(t)
	useStubDir(t, map[string]string{"snap": "#!/bin/sh\nexit 1\n"})
	env := testEnv(t, true)
	orphan := filepath.Join(env.Home, "snap", "oldapp")
	if err := os.MkdirAll(orphan, 0o755); err != nil {
		t.Fatal(err)
	}
	plan := build(t, env)
	for _, it := range plan.Items {
		if it.Target.ID == "snap.userdata" {
			t.Fatal("snap.userdata must not be offered when the snap inventory is unknown")
		}
	}
}

func TestParsers(t *testing.T) {
	apps := parseFlatpakApps("org.gnome.Calculator\nNothing unused to uninstall\n\n")
	if len(apps) != 1 || !apps["org.gnome.Calculator"] {
		t.Fatalf("parseFlatpakApps = %v", apps)
	}
	snaps := parseSnapNames("Name Version Rev Tracking Publisher Notes\ncore20 1 2015 latest/stable canonical disabled\n")
	if len(snaps) != 1 || !snaps["core20"] {
		t.Fatalf("parseSnapNames = %v", snaps)
	}
	revs := parseDisabledRevisions("Name Version Rev Tracking Publisher Notes\ncore20 1 2015 latest/stable canonical disabled\nfirefox 2 99 latest/stable mozilla -\n")
	if len(revs) != 1 || revs[0].name != "core20" || revs[0].rev != "2015" {
		t.Fatalf("parseDisabledRevisions = %v", revs)
	}
}

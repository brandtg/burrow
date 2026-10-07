package containers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

func testEnv(t *testing.T, dryRun bool) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	return &engine.Env{
		Home:      filepath.Join(dir, "home"),
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

// useTestbin prepends the repo testbin to PATH.
func useTestbin(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("BURROW_TESTBIN_LOG", logPath)
	t.Setenv("PATH", repoTestbin(t)+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

// useOnlyTestbin sets PATH to the repo testbin alone, so host-installed tools
// (for example podman) cannot change a golden.
func useOnlyTestbin(t *testing.T) string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "argv.log")
	t.Setenv("BURROW_TESTBIN_LOG", logPath)
	t.Setenv("PATH", repoTestbin(t))
	return logPath
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

func TestDiscoverDockerItems(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, true)
	plan := build(t, env)
	items := byID(plan)

	cases := []struct {
		id        string
		risk      engine.Risk
		def       bool
		needsRoot bool
		argv      []string
	}{
		{"containers.docker-build-cache", engine.RiskLow, true, false, []string{"docker", "builder", "prune", "-f"}},
		{"containers.docker-dangling", engine.RiskLow, true, false, []string{"docker", "image", "prune", "-f"}},
		{"containers.docker-unused", engine.RiskMed, false, false, []string{"docker", "image", "prune", "-a", "-f"}},
		{"containers.docker-volumes", engine.RiskHigh, false, false, []string{"docker", "volume", "prune", "-f"}},
		{"containers.docker-all", engine.RiskHigh, false, false, []string{"docker", "system", "prune", "-a", "--volumes", "-f"}},
	}
	for _, tc := range cases {
		it, ok := items[tc.id]
		if !ok {
			t.Fatalf("missing %s; got %v", tc.id, keys(items))
		}
		if it.Target.Risk != tc.risk || it.Target.DefaultOn != tc.def || it.Target.NeedsRoot != tc.needsRoot {
			t.Fatalf("%s contract wrong: risk=%v default=%v root=%v", tc.id, it.Target.Risk, it.Target.DefaultOn, it.Target.NeedsRoot)
		}
		if got := strings.Join(it.Argv, " "); got != strings.Join(tc.argv, " ") {
			t.Fatalf("%s argv = %q, want %q", tc.id, got, strings.Join(tc.argv, " "))
		}
		if it.Target.Recovery != engine.RecoveryOwner {
			t.Fatalf("%s recovery = %v, want owner", tc.id, it.Target.Recovery)
		}
	}

	// High-risk items are never auto-selected.
	if items["containers.docker-volumes"].Selected || items["containers.docker-all"].Selected {
		t.Fatal("high-risk docker items must never be auto-selected")
	}
	if !items["containers.docker-build-cache"].Selected || !items["containers.docker-dangling"].Selected {
		t.Fatal("low-risk docker items should be selected by default")
	}

	// docker system df sizing is rendered per category.
	if got := items["containers.docker-build-cache"].Target.SizeBytes; got != 800*1000*1000 {
		t.Fatalf("build cache size = %d", got)
	}
	if got := items["containers.docker-unused"].Target.SizeBytes; got != 1200*1000*1000 {
		t.Fatalf("unused images size = %d", got)
	}
	if items["containers.docker-dangling"].Target.SizeKnown {
		t.Fatal("dangling image size must stay unknown: docker system df cannot isolate it")
	}
}

func TestDockerArgvExecutedExactly(t *testing.T) {
	logPath := useTestbin(t)
	env := testEnv(t, false)
	plan := build(t, env, "containers.docker-build-cache")
	if len(plan.Items) != 1 || !plan.Items[0].Selected {
		t.Fatalf("expected one selected item, got %d", len(plan.Items))
	}
	jw, err := journal.NewWriter(env.Paths.Journal)
	if err != nil {
		t.Fatal(err)
	}
	sum := engine.Execute(context.Background(), env, plan, jw)
	if sum.OK != 1 {
		t.Fatalf("expected one ok, got %+v", sum)
	}
	log := readFile(t, logPath)
	if !strings.Contains(log, "docker builder prune -f") {
		t.Fatalf("expected docker builder prune, log:\n%s", log)
	}
	if strings.Contains(log, "sudo") {
		t.Fatalf("docker must run without elevation, log:\n%s", log)
	}
}

func TestDockerDaemonUnreachableSkipsWithReason(t *testing.T) {
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)

	env := testEnv(t, false)
	plan := build(t, env)
	if len(plan.Items) == 0 {
		t.Fatal("docker items should be planned even when the daemon is unreachable")
	}
	sum := engine.Execute(context.Background(), env, plan, nil)
	if sum.Failed != 0 {
		t.Fatalf("unreachable daemon must not fail items: %+v", sum)
	}
	for _, it := range plan.Selected() {
		if it.Status != engine.StatusSkipped || it.Reason == "" || !it.GuardStop {
			t.Fatalf("item %s not skipped with reason: %+v", it.Target.ID, it)
		}
	}
}

func TestReconcileDropsSupersededDockerPrunes(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, true)
	plan := build(t, env)
	items := byID(plan)
	items["containers.docker-all"].Selected = true
	// Everything else selected too.
	for _, it := range plan.Items {
		it.Selected = true
	}
	New().Reconcile(plan)
	if !items["containers.docker-all"].Selected {
		t.Fatal("docker-all must stay selected")
	}
	for _, id := range []string{"containers.docker-build-cache", "containers.docker-dangling", "containers.docker-unused", "containers.docker-volumes"} {
		if items[id].Selected {
			t.Fatalf("%s should be deselected when docker-all is selected", id)
		}
	}
}

func TestReconcileImagePruneAllSupersedesDangling(t *testing.T) {
	useTestbin(t)
	env := testEnv(t, true)
	plan := build(t, env)
	items := byID(plan)
	items["containers.docker-unused"].Selected = true
	items["containers.docker-dangling"].Selected = true
	New().Reconcile(plan)
	if !items["containers.docker-unused"].Selected {
		t.Fatal("unused images must stay selected")
	}
	if items["containers.docker-dangling"].Selected {
		t.Fatal("dangling must be deselected when image prune -a is selected")
	}
}

func TestParseDockerDF(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "testdata", "containers", "docker-system-df.txt"))
	if err != nil {
		t.Fatal(err)
	}
	d := parseDockerDF(string(data))
	if !d.imagesKnown || d.images != 1200*1000*1000 {
		t.Fatalf("images = %d known=%v", d.images, d.imagesKnown)
	}
	if !d.buildCacheKnown || d.buildCache != 800*1000*1000 {
		t.Fatalf("build cache = %d known=%v", d.buildCache, d.buildCacheKnown)
	}
	if !d.volumesKnown || d.volumes != 0 {
		t.Fatalf("volumes = %d known=%v", d.volumes, d.volumesKnown)
	}
	if !d.containersKnown || d.containers != 50*1000*1000 {
		t.Fatalf("containers = %d known=%v", d.containers, d.containersKnown)
	}
	if total, known := d.reclaimableTotal(); !known || total != 2050*1000*1000 {
		t.Fatalf("total = %d known=%v", total, known)
	}
}

func TestDockerPlanGolden(t *testing.T) {
	useOnlyTestbin(t)
	env := testEnv(t, true)
	plan := build(t, env)
	got := renderPlan(plan)
	golden := filepath.Join("..", "..", "..", "testdata", "containers", "docker-plan.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v (run with UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("docker plan golden mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func renderPlan(plan *engine.Plan) string {
	var b strings.Builder
	for _, it := range plan.Items {
		fmt.Fprintf(&b, "%s|%s|%s|default=%v|selected=%v|%s\n",
			it.Target.ID, it.Target.Risk, it.Target.Recovery,
			it.Target.DefaultOn, it.Selected, strings.Join(it.Argv, " "))
	}
	return b.String()
}

func keys(m map[string]*engine.PlannedItem) []string {
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

package downloads_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/catalog/downloads"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/whitelist"
)

func testEnv(t *testing.T) *engine.Env {
	t.Helper()
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(filepath.Join(home, "Downloads"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Downloads.OldMinSize = "1M"
	return &engine.Env{
		Home:      home,
		XDGCache:  filepath.Join(dir, "cache"),
		XDGConfig: filepath.Join(dir, "config"),
		XDGData:   filepath.Join(dir, "data"),
		XDGState:  filepath.Join(dir, "state"),
		Run:       owner.New(true),
		Cfg:       cfg,
		Whitelist: &whitelist.Set{},
		Paths:     config.Paths{TrashDir: filepath.Join(dir, "Trash")},
		DryRun:    true,
	}
}

func writeFile(t *testing.T, path string, size int, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func discover(t *testing.T, env *engine.Env) map[string]engine.Target {
	t.Helper()
	targets, err := downloads.New().Discover(context.Background(), env)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]engine.Target{}
	for _, tg := range targets {
		out[tg.ID] = tg
	}
	return out
}

func TestInstallerDetection(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Home, "Downloads", "foo.deb"), 16, 40*24*time.Hour)
	writeFile(t, filepath.Join(env.Home, "Downloads", "notes.txt"), 16, 40*24*time.Hour)
	writeFile(t, filepath.Join(env.Home, "setup.AppImage"), 16, 40*24*time.Hour)
	writeFile(t, filepath.Join(env.Home, "Downloads", "fresh.deb"), 16, time.Hour)

	got := discover(t, env)
	for _, id := range []string{"downloads.installers.foo.deb", "downloads.installers.setup.AppImage"} {
		tg, ok := got[id]
		if !ok {
			t.Fatalf("missing %s: %v", id, keys(got))
		}
		if tg.Recovery != engine.RecoveryTrash || !tg.DefaultOn || tg.Risk != engine.RiskLow {
			t.Fatalf("%s contract wrong: %+v", id, tg)
		}
	}
	if _, ok := got["downloads.installers.fresh.deb"]; ok {
		t.Fatal("fresh installer must not be swept")
	}
	if _, ok := got["downloads.installers.notes.txt"]; ok {
		t.Fatal("non-installer must not be swept")
	}
}

func TestOldDownloads(t *testing.T) {
	env := testEnv(t)
	writeFile(t, filepath.Join(env.Home, "Downloads", "big.bin"), 2<<20, 100*24*time.Hour)
	writeFile(t, filepath.Join(env.Home, "Downloads", "small.bin"), 16, 100*24*time.Hour)
	writeFile(t, filepath.Join(env.Home, "Downloads", "recent.bin"), 2<<20, time.Hour)

	got := discover(t, env)
	tg, ok := got["downloads.old.big.bin"]
	if !ok {
		t.Fatalf("missing downloads.old.big.bin: %v", keys(got))
	}
	if tg.DefaultOn || tg.Risk != engine.RiskMed {
		t.Fatalf("old download must be opt-in med risk: %+v", tg)
	}
	if _, ok := got["downloads.old.small.bin"]; ok {
		t.Fatal("small old download must not be listed")
	}
	if _, ok := got["downloads.old.recent.bin"]; ok {
		t.Fatal("recent download must not be listed")
	}
}

func keys(m map[string]engine.Target) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

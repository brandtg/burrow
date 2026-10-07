package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func xdgTestEnv(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"XDG_CONFIG_HOME": t.TempDir(),
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CACHE_HOME":  t.TempDir(),
	}
}

func fixtureRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "..", "testdata", "analyze", "tree"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestAnalyzeJSONGolden(t *testing.T) {
	te := newTestApp(t, xdgTestEnv(t))
	root := fixtureRoot(t)
	if code := te.run("analyze", "--json", "--apparent", "-t", "3", root); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	got := []byte(strings.ReplaceAll(te.out.String(), root, "<ROOT>"))
	goldenCompare(t, filepath.Join("..", "..", "testdata", "analyze", "golden.json"), got)
}

func TestAnalyzeNonTTYDefaultsToJSON(t *testing.T) {
	te := newTestApp(t, xdgTestEnv(t))
	root := fixtureRoot(t)
	if code := te.run("analyze", "--apparent", root); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), `"scan_status"`) {
		t.Fatalf("non-TTY analyze did not emit JSON: %q", te.out.String())
	}
}

func TestAnalyzeJSONRejectsMultiplePaths(t *testing.T) {
	te := newTestApp(t, xdgTestEnv(t))
	if code := te.run("analyze", "--json", t.TempDir(), t.TempDir()); code != ExitUsage {
		t.Fatalf("exit code = %d, want %d", code, ExitUsage)
	}
}

func TestAnalyzeMissingPathErrors(t *testing.T) {
	te := newTestApp(t, xdgTestEnv(t))
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if code := te.run("analyze", "--json", missing); code == ExitOK {
		t.Fatalf("expected a non-zero exit for a missing path, stderr=%q", te.errOut.String())
	}
}

func TestAnalyzeJSONReportsPartialScan(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read an unreadable directory")
	}
	te := newTestApp(t, xdgTestEnv(t))
	root := t.TempDir()
	locked := filepath.Join(root, "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(locked, "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	if code := te.run("analyze", "--json", root); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if !strings.Contains(te.out.String(), `"scan_status": "partial"`) {
		t.Fatalf("partial scan not reported: %s", te.out.String())
	}
}

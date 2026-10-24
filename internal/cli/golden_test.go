package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// goldenCompare compares got to the file at path. Run the test with
// BURROW_UPDATE_GOLDEN=1 to regenerate a golden after an intentional change;
// diffs are meant to be reviewed, never auto-accepted in CI.
func goldenCompare(t *testing.T, path string, got []byte) {
	t.Helper()
	if os.Getenv("BURROW_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("golden mismatch for %s:\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

var runIDPattern = regexp.MustCompile(`"run_id": "[^"]*"`)

// maskRunID replaces the random run id so the rest of the document can be
// snapshotted.
func maskRunID(in []byte) []byte {
	return runIDPattern.ReplaceAll(in, []byte(`"run_id": "<RUN_ID>"`))
}

func TestCleanDryRunJSONGolden(t *testing.T) {
	useTestbin(t)
	te := newTestApp(t, cleanTestEnv(t))
	te.app.Adapter = aptAdapter()
	if code := te.run("clean", "--only", "system.pkg-cache", "--dry-run", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	got := maskRunID(te.out.Bytes())
	goldenCompare(t, filepath.Join("..", "..", "testdata", "clean", "json.golden"), got)
}

func TestHistoryJSONGolden(t *testing.T) {
	state := t.TempDir()
	journalPath := filepath.Join(state, "burrow", "operations.jsonl")
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"ts":"2026-10-05T12:00:00Z","run_id":"01JTEST","command":"clean","item":"dev.npm","action":"owner","argv":["npm","cache","clean","--force"],"status":"ok","bytes":1024,"host":"h","burrow_version":"0.1.0"}` + "\n"
	if err := os.WriteFile(journalPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": t.TempDir(),
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  state,
		"XDG_CACHE_HOME":  t.TempDir(),
	})
	if code := te.run("history", "--json"); code != ExitOK {
		t.Fatalf("exit = %d: %s", code, te.errOut.String())
	}
	goldenCompare(t, filepath.Join("..", "..", "testdata", "history", "json.golden"), te.out.Bytes())
}

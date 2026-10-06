package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/trash"
)

// TestHistoryRestoreAnalyzeItem exercises the full analyze -> trash -> journal
// -> history --restore path for an item that landed in the home trash.
func TestHistoryRestoreAnalyzeItem(t *testing.T) {
	cfg := t.TempDir()
	data := t.TempDir()
	state := t.TempDir()

	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := trash.New(filepath.Join(data, "Trash"), os.Getuid())
	if _, err := tr.Put(victim); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(victim); !os.IsNotExist(err) {
		t.Fatalf("victim should be in the trash, stat err = %v", err)
	}

	journalPath := filepath.Join(state, "burrow", "operations.jsonl")
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	line := fmt.Sprintf(
		`{"ts":%q,"run_id":"RUN1","command":"analyze","item":%q,"action":"trash","path":%q,"status":"ok","bytes":1,"host":"h","burrow_version":"0.1.0"}`+"\n",
		time.Now().UTC().Format(time.RFC3339), victim, victim)
	if err := os.WriteFile(journalPath, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": cfg,
		"XDG_DATA_HOME":   data,
		"XDG_STATE_HOME":  state,
		"XDG_CACHE_HOME":  t.TempDir(),
	})
	if code := te.run("history", "--restore", "--run", "RUN1"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	if _, err := os.Stat(victim); err != nil {
		t.Fatalf("history --restore did not restore the item: %v", err)
	}
}

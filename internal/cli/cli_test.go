package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testEnv struct {
	app    *App
	out    *bytes.Buffer
	errOut *bytes.Buffer
}

func newTestApp(t *testing.T, env map[string]string) *testEnv {
	t.Helper()
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	app := New(out, errOut)
	app.Geteuid = func() int { return 1000 }
	app.Getenv = func(k string) string { return env[k] }
	return &testEnv{app: app, out: out, errOut: errOut}
}

func (te *testEnv) run(args ...string) int {
	return te.app.Run(args)
}

func TestVersionCommand(t *testing.T) {
	te := newTestApp(t, nil)
	if code := te.run("version"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(te.out.String(), "burrow") {
		t.Fatalf("output = %q", te.out.String())
	}
}

func TestVersionJSON(t *testing.T) {
	te := newTestApp(t, nil)
	if code := te.run("version", "--json"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(te.out.String(), `"version"`) {
		t.Fatalf("output = %q", te.out.String())
	}
}

func TestConfigCommand(t *testing.T) {
	cfg := t.TempDir()
	data := t.TempDir()
	state := t.TempDir()
	cache := t.TempDir()
	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": cfg,
		"XDG_DATA_HOME":   data,
		"XDG_STATE_HOME":  state,
		"XDG_CACHE_HOME":  cache,
	})
	if code := te.run("config"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	got := te.out.String()
	for _, want := range []string{"resolved paths", filepath.Join(cfg, "burrow", "config.toml"), filepath.Join(state, "burrow", "operations.jsonl")} {
		if !strings.Contains(got, want) {
			t.Fatalf("config output missing %q:\n%s", want, got)
		}
	}
}

func TestHistoryReadsHandWrittenJournal(t *testing.T) {
	cfg := t.TempDir()
	state := t.TempDir()
	journalPath := filepath.Join(state, "burrow", "operations.jsonl")
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"ts":"2026-10-05T12:00:00Z","run_id":"01JTEST","command":"clean","item":"","action":"run_start","status":"start","bytes":0,"host":"h","burrow_version":"0.1.0"}
{"ts":"2026-10-05T12:00:01Z","run_id":"01JTEST","command":"clean","item":"dev.npm","action":"owner","argv":["npm","cache","clean","--force"],"status":"ok","bytes":1024,"host":"h","burrow_version":"0.1.0"}
{"ts":"2026-10-05T12:00:02Z","run_id":"01JTEST","command":"clean","item":"","action":"run_end","status":"end","bytes":1024,"host":"h","burrow_version":"0.1.0"}
`
	if err := os.WriteFile(journalPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": cfg,
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  state,
		"XDG_CACHE_HOME":  t.TempDir(),
	})
	if code := te.run("history"); code != ExitOK {
		t.Fatalf("exit code = %d: %s", code, te.errOut.String())
	}
	got := te.out.String()
	if !strings.Contains(got, "COMMAND") || !strings.Contains(got, "clean") || !strings.Contains(got, "01JTEST") {
		t.Fatalf("history output = %q", got)
	}
}

func TestHistoryJSON(t *testing.T) {
	cfg := t.TempDir()
	state := t.TempDir()
	journalPath := filepath.Join(state, "burrow", "operations.jsonl")
	if err := os.MkdirAll(filepath.Dir(journalPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{"ts":"2026-10-05T12:00:00Z","run_id":"01JTEST","command":"clean","item":"dev.npm","action":"owner","status":"ok","bytes":5,"host":"h","burrow_version":"0.1.0"}` + "\n"
	if err := os.WriteFile(journalPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	te := newTestApp(t, map[string]string{
		"XDG_CONFIG_HOME": cfg,
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  state,
		"XDG_CACHE_HOME":  t.TempDir(),
	})
	if code := te.run("history", "--json"); code != ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(te.out.String(), `"item":"dev.npm"`) {
		t.Fatalf("json output = %q", te.out.String())
	}
}

func TestStubCommandsExitUsage(t *testing.T) {
	for _, name := range []string{"analyze", "clean", "purge", "status"} {
		te := newTestApp(t, nil)
		if code := te.run(name); code != ExitUsage {
			t.Fatalf("%s: exit code = %d, want %d", name, code, ExitUsage)
		}
		if !strings.Contains(te.errOut.String(), "not implemented") {
			t.Fatalf("%s: stderr = %q", name, te.errOut.String())
		}
	}
}

func TestRootWithoutSudoRefuses(t *testing.T) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	app := New(out, errOut)
	app.Geteuid = func() int { return 0 }
	app.Getenv = func(string) string { return "" }
	if code := app.Run([]string{"history"}); code != ExitGeneric {
		t.Fatalf("exit code = %d, want %d", code, ExitGeneric)
	}
	if !strings.Contains(errOut.String(), "root") {
		t.Fatalf("expected root refusal message, got %q", errOut.String())
	}
}

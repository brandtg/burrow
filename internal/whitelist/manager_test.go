package whitelist

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagerAddRemoveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist")
	m := NewManager(path)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	changed, err := m.Add("/home/u/.cache/keep")
	if err != nil || !changed {
		t.Fatalf("add: changed=%v err=%v", changed, err)
	}
	if changed, err = m.Add("/home/u/.cache/keep"); err != nil || changed {
		t.Fatalf("duplicate add should be a no-op: changed=%v err=%v", changed, err)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}

	reloaded := NewManager(path)
	if err := reloaded.Load(); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Entries(); len(got) != 1 || got[0] != "/home/u/.cache/keep" {
		t.Fatalf("entries = %v", got)
	}

	if changed, err := reloaded.Remove("/home/u/.cache/keep"); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if err := reloaded.Save(); err != nil {
		t.Fatal(err)
	}
	final := NewManager(path)
	if err := final.Load(); err != nil {
		t.Fatal(err)
	}
	if len(final.Entries()) != 0 {
		t.Fatalf("expected empty after remove, got %v", final.Entries())
	}
}

func TestManagerPreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "whitelist")
	content := "# keep this comment\n\n/home/u/keep\n# trailing comment\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(path)
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add("/home/u/added"); err != nil {
		t.Fatal(err)
	}
	if err := m.Save(); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# keep this comment", "# trailing comment", "/home/u/keep", "/home/u/added"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("saved whitelist missing %q:\n%s", want, got)
		}
	}
}

func TestManagerRejectsRelative(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "whitelist"))
	if _, err := m.Add("relative/path"); err == nil {
		t.Fatal("expected a relative-path error")
	}
}

func TestManagerMissingFileIsEmpty(t *testing.T) {
	m := NewManager(filepath.Join(t.TempDir(), "does-not-exist"))
	if err := m.Load(); err != nil {
		t.Fatal(err)
	}
	if len(m.Entries()) != 0 {
		t.Fatalf("expected empty entries, got %v", m.Entries())
	}
}

package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func newTestTrash(t *testing.T) (*Trash, string) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "Trash")
	return New(home, os.Getuid()), home
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPutListRestoreRoundTrip(t *testing.T) {
	tr, _ := newTestTrash(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "cache-entry")
	writeFile(t, src, "payload")

	entry, err := tr.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Path != src {
		t.Fatalf("entry path %q != %q", entry.Path, src)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present after Put")
	}

	entries, err := tr.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Path != src {
		t.Fatalf("listed path %q != %q", entries[0].Path, src)
	}

	if err := tr.Restore(entries[0]); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Fatalf("restored content = %q", data)
	}
	entries, _ = tr.List()
	if len(entries) != 0 {
		t.Fatalf("expected empty trash after restore, got %d", len(entries))
	}
}

func TestPutCollisionNaming(t *testing.T) {
	tr, home := newTestTrash(t)
	a := t.TempDir()
	b := t.TempDir()
	writeFile(t, filepath.Join(a, "same"), "a")
	writeFile(t, filepath.Join(b, "same"), "b")

	e1, err := tr.Put(filepath.Join(a, "same"))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := tr.Put(filepath.Join(b, "same"))
	if err != nil {
		t.Fatal(err)
	}
	if e1.Name != "same" {
		t.Fatalf("first name = %q", e1.Name)
	}
	if e2.Name != "same.2" {
		t.Fatalf("second name = %q", e2.Name)
	}
	if _, err := os.Stat(filepath.Join(home, "files", "same")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "files", "same.2")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, "info", "same.2.trashinfo")); err != nil {
		t.Fatalf("info name must mirror files name: %v", err)
	}
}

func TestPutWritesGoldenInfo(t *testing.T) {
	tr, home := newTestTrash(t)
	src := filepath.Join(t.TempDir(), "My File")
	writeFile(t, src, "x")
	entry, err := tr.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(entry.InfoPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Path != src {
		t.Fatalf("info path %q != %q", info.Path, src)
	}
	if entry.InfoPath != filepath.Join(home, "info", "My File.trashinfo") {
		t.Fatalf("unexpected info path %q", entry.InfoPath)
	}
}

func TestPutMissingSourceFailsClosed(t *testing.T) {
	tr, _ := newTestTrash(t)
	if _, err := tr.Put(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected error for missing source")
	}
}

// TestPutUnavailableTrashNeverUnlinks is the I5 regression: if the trash cannot
// be created, Put must fail and leave the source untouched. It must never fall
// back to unlinking.
func TestPutUnavailableTrashNeverUnlinks(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "Trash")
	writeFile(t, blocker, "i am a file, not a directory")
	tr := New(blocker, os.Getuid())

	src := filepath.Join(t.TempDir(), "valuable")
	writeFile(t, src, "keep me")

	if _, err := tr.Put(src); err == nil {
		t.Fatal("expected Put to fail when trash is unavailable")
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("source was removed despite fail-closed contract: %v", err)
	}
	if string(data) != "keep me" {
		t.Fatalf("source content changed: %q", data)
	}
}

func TestRestoreRefusesExistingTarget(t *testing.T) {
	tr, _ := newTestTrash(t)
	src := filepath.Join(t.TempDir(), "thing")
	writeFile(t, src, "original")
	entry, err := tr.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "new occupant")
	if err := tr.Restore(entry); err == nil {
		t.Fatal("expected restore to refuse an existing target")
	}
}

// TestCrossDevice exercises the per-mount trash path. It requires a directory
// on a different filesystem, supplied by scripts/integration.sh via
// BURROW_CROSS_DEVICE_DIR (a loopback mount). It is skipped otherwise.
func TestCrossDevice(t *testing.T) {
	other := os.Getenv("BURROW_CROSS_DEVICE_DIR")
	if other == "" {
		t.Skip("set BURROW_CROSS_DEVICE_DIR to a directory on another filesystem")
	}
	home := filepath.Join(t.TempDir(), "Trash")
	tr := New(home, os.Getuid())

	src := filepath.Join(other, "cross-device-entry")
	writeFile(t, src, "payload")
	entry, err := tr.Put(src)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Fatalf("source still present after cross-device Put")
	}
	if _, err := os.Lstat(entry.InfoPath); err != nil {
		t.Fatalf("info file missing: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(entry.InfoPath), "..", "files", entry.Name)); err != nil {
		t.Fatalf("files entry missing: %v", err)
	}
}

func TestEmpty(t *testing.T) {
	tr, _ := newTestTrash(t)
	for _, name := range []string{"a", "b", "c"} {
		writeFile(t, filepath.Join(t.TempDir(), name), "x")
	}
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		writeFile(t, filepath.Join(dir, name), "x")
		if _, err := tr.Put(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.Empty(); err != nil {
		t.Fatal(err)
	}
	entries, _ := tr.List()
	if len(entries) != 0 {
		t.Fatalf("expected empty, got %d", len(entries))
	}
}

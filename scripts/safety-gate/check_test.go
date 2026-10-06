package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGo(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGateFlagsPlantedSink(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import "os"

func Bad(p string) error { return os.Remove(p) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(violations), violations)
	}
	if violations[0].Sink != "os.Remove" {
		t.Fatalf("unexpected sink %q", violations[0].Sink)
	}
}

func TestGateAllowsSafePackages(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "trash", "ok.go"), `package trash

import (
	"os"
	"syscall"
)

func Ok(p string) error {
	_ = syscall.Unlink(p)
	return os.RemoveAll(p)
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected no violations, got %v", violations)
	}
}

func TestGateSkipsTestFiles(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad_test.go"), `package catalog

import "os"

func bad(p string) { _ = os.Remove(p) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected test files skipped, got %v", violations)
	}
}

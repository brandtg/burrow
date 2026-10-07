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

func TestGateFlagsUnixSink(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import "golang.org/x/sys/unix"

func Bad(p string) error { return unix.Unlinkat(0, p, 0) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "golang.org/x/sys/unix.Unlinkat" {
		t.Fatalf("expected unix.Unlinkat violation, got %v", violations)
	}
}

func TestGateFlagsMethodTruncate(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import "os"

func Bad(f *os.File) error { return f.Truncate(0) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "(method).Truncate" {
		t.Fatalf("expected method Truncate violation, got %v", violations)
	}
}

func TestGateFlagsDotImport(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import . "os"

func Bad(p string) error { return Remove(p) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "os.Remove" {
		t.Fatalf("expected dot-import os.Remove violation, got %v", violations)
	}
}

func TestGateFlagsAliasedImport(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import o "os"

func Bad(p string) error { return o.Remove(p) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "os.Remove" {
		t.Fatalf("expected aliased os.Remove violation, got %v", violations)
	}
}

func TestGateFlagsRemoveTreeOutsidePurge(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import "github.com/brandtg/burrow/internal/safe"

func Bad(p string, v *safe.Validator, s *safe.Snapshot) error {
	return safe.RemoveTree(p, v, s)
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "safe.RemoveTree" {
		t.Fatalf("expected a safe.RemoveTree violation, got %v", violations)
	}
}

func TestGateFlagsRemoveTreeInAllowedSinkPackage(t *testing.T) {
	root := t.TempDir()
	// internal/trash is allowed to call os.RemoveAll, but must never reach for
	// the permanent-delete primitive.
	writeGo(t, filepath.Join(root, "internal", "trash", "bad.go"), `package trash

import "github.com/brandtg/burrow/internal/safe"

func Bad(p string, v *safe.Validator, s *safe.Snapshot) error {
	return safe.RemoveTree(p, v, s)
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "safe.RemoveTree" {
		t.Fatalf("expected a safe.RemoveTree violation in an allowed sink package, got %v", violations)
	}
}

func TestGateAllowsRemoveTreeInPurge(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "purge", "ok.go"), `package purge

import "github.com/brandtg/burrow/internal/safe"

func Ok(p string, v *safe.Validator, s *safe.Snapshot) error {
	return safe.RemoveTree(p, v, s)
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

func TestGateFlagsAliasedRemoveTree(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "catalog", "bad.go"), `package catalog

import s "github.com/brandtg/burrow/internal/safe"

func Bad(p string) error { return s.RemoveTree(p, nil, nil) }
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "safe.RemoveTree" {
		t.Fatalf("expected an aliased safe.RemoveTree violation, got %v", violations)
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

func TestGateFlagsDeleteCapableOwnerArgv(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "owner", "bad.go"), `package owner

type OwnerCommand struct{ Argv []string }

func Bad() OwnerCommand {
	return OwnerCommand{Argv: []string{"rm", "-rf", "/var/lib/snapd/cache"}}
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "owner argv" {
		t.Fatalf("expected one owner argv violation, got %v", violations)
	}
}

func TestGateFlagsFindDeleteOwnerArgv(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "owner", "bad.go"), `package owner

type OwnerCommand struct{ Argv []string }

func Bad() OwnerCommand {
	return OwnerCommand{Argv: []string{"find", "/var/lib/snapd/cache", "-type", "f", "-delete"}}
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "owner argv" {
		t.Fatalf("expected one owner argv violation, got %v", violations)
	}
}

func TestGateFlagsShellOwnerArgv(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "owner", "bad.go"), `package owner

type OwnerCommand struct{ Argv []string }

func Bad() OwnerCommand {
	return OwnerCommand{Argv: []string{"sh", "-c", "rm -rf /tmp/x"}}
}
`)
	violations, err := Check(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || violations[0].Sink != "owner argv" {
		t.Fatalf("expected one owner argv violation, got %v", violations)
	}
}

func TestGateAllowsSafeOwnerArgv(t *testing.T) {
	root := t.TempDir()
	writeGo(t, filepath.Join(root, "internal", "owner", "ok.go"), `package owner

type OwnerCommand struct{ Argv []string }

func Ok() OwnerCommand {
	return OwnerCommand{Argv: []string{"docker", "builder", "prune", "-f"}}
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

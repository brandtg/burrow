package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/owner"
)

// stubDU puts a fake du on PATH that prints a fixed KiB figure, so measure's
// fork-and-parse path is testable without a real du or a big tree.
func stubDU(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "du"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

func TestMeasureDirsViaDu(t *testing.T) {
	stubDU(t, "#!/bin/sh\necho \"42 $4\"\n")
	env := &Env{Run: owner.New(false)}
	dir := t.TempDir()
	targets := []*Target{{Path: dir}}
	measure(context.Background(), env, targets)
	if !targets[0].SizeKnown || targets[0].SizeBytes != 42*1024 {
		t.Fatalf("size = %d known=%v, want 42 KiB", targets[0].SizeBytes, targets[0].SizeKnown)
	}
}

func TestMeasureSingleFileUsesStat(t *testing.T) {
	stubDU(t, "#!/bin/sh\nexit 1\n") // du must not be consulted for files
	env := &Env{Run: owner.New(false)}
	file := filepath.Join(t.TempDir(), "thing")
	if err := os.WriteFile(file, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	targets := []*Target{{Path: file}}
	measure(context.Background(), env, targets)
	if !targets[0].SizeKnown || targets[0].SizeBytes == 0 {
		t.Fatalf("file size = %d known=%v", targets[0].SizeBytes, targets[0].SizeKnown)
	}
}

func TestMeasureUnknownOnFailure(t *testing.T) {
	stubDU(t, "#!/bin/sh\nexit 1\n")
	env := &Env{Run: owner.New(false)}
	targets := []*Target{{Path: t.TempDir()}}
	measure(context.Background(), env, targets)
	if targets[0].SizeKnown {
		t.Fatal("a failed probe must leave the size unknown, not zero")
	}
}

func TestMeasureSkipsAlreadyKnown(t *testing.T) {
	// No du on PATH at all: a target that is already measured must not probe.
	env := &Env{Run: owner.New(false)}
	targets := []*Target{{Path: t.TempDir(), SizeKnown: true, SizeBytes: 7}}
	measure(context.Background(), env, targets)
	if targets[0].SizeBytes != 7 {
		t.Fatalf("known size changed to %d", targets[0].SizeBytes)
	}
}

func TestMeasureCancelledStaysUnknown(t *testing.T) {
	stubDU(t, "#!/bin/sh\necho \"42 $4\"\n")
	env := &Env{Run: owner.New(false)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	targets := []*Target{{Path: t.TempDir()}}
	measure(ctx, env, targets)
	if targets[0].SizeKnown {
		t.Fatal("a cancelled measure must not report a size")
	}
}

package safe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/brandtg/burrow/internal/cli"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/scan"
	"github.com/brandtg/burrow/internal/trash"
	"github.com/brandtg/burrow/internal/whitelist"
)

// TestI1NoMutationBeforePreview runs the read-only pipeline steps (resolve,
// scan, validate, snapshot, whitelist) over a fixture and asserts the tree is
// byte-for-byte unchanged.
func TestI1NoMutationBeforePreview(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "a.txt", "alpha")
	writeFixture(t, root, "sub/b.txt", "beta")
	writeFixture(t, root, "sub/deep/c.txt", "gamma")

	before := digestTree(t, root)

	// Discovery and planning are read-only.
	if _, err := scan.Analyze(context.Background(), root, scan.Options{}); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "a.txt")
	if err := safe.ValidateForDeletion(target); err != nil {
		t.Fatal(err)
	}
	if _, err := safe.TakeSnapshot(target); err != nil {
		t.Fatal(err)
	}
	if _, err := whitelist.Parse(strings.NewReader(filepath.Join(root, "sub") + "\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(filepath.Join(root, "config.toml")); err != nil {
		t.Fatal(err)
	}

	after := digestTree(t, root)
	if before != after {
		t.Fatalf("read-only pipeline mutated the tree:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestI2EveryMutationDeclaresRecovery asserts the target contract refuses a
// mutation with no declared recovery contract.
func TestI2EveryMutationDeclaresRecovery(t *testing.T) {
	if err := (engine.Target{ID: "x", Action: fakeAction{}}).Validate(); err == nil {
		t.Fatal("expected rejection of a target with no recovery contract")
	}
	if err := (engine.Target{ID: "x", Recovery: engine.RecoveryTrash, Path: "/tmp/x", Action: fakeAction{}}).Validate(); err != nil {
		t.Fatalf("expected valid trash target: %v", err)
	}
	if err := (engine.Target{ID: "x", Recovery: engine.RecoveryOwner, Action: fakeAction{recovery: engine.RecoveryOwner}}).Validate(); err != nil {
		t.Fatalf("expected valid owner target: %v", err)
	}
}

type fakeAction struct{ recovery engine.Recovery }

func (f fakeAction) Describe() string { return "fake" }
func (f fakeAction) Recovery() engine.Recovery {
	if f.recovery == 0 {
		return engine.RecoveryTrash
	}
	return f.recovery
}
func (f fakeAction) Run(context.Context, bool) (engine.Result, error) {
	return engine.Result{Status: "ok"}, nil
}

// TestI3NoDeletionOutsideAllowedRoot asserts the allow-list primitive.
func TestI3NoDeletionOutsideAllowedRoot(t *testing.T) {
	allowed := t.TempDir()
	outside := t.TempDir()
	file := filepath.Join(outside, "x")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	v := &safe.Validator{Roots: []string{allowed}, RequireRoot: true}
	if err := v.Validate(file); !errors.Is(err, safe.ErrUnsafe) {
		t.Fatalf("expected unsafe rejection, got %v", err)
	}
	if safe.WithinRoot(file, allowed) {
		t.Fatal("WithinRoot must not accept a path outside the root")
	}
}

// TestI4OwnerCommandsVerbatim asserts owner commands are executed exactly as
// given, with no shell interpolation.
func TestI4OwnerCommandsVerbatim(t *testing.T) {
	dangerous := []string{"apt-get", "clean", ";", "rm", "-rf", "/"}
	planned := owner.Plan(dangerous, false)
	if strings.Join(planned, "\x00") != strings.Join(dangerous, "\x00") {
		t.Fatalf("argv was rewritten: %v", planned)
	}
	r := owner.New(true)
	res, err := r.Run(context.Background(), dangerous, time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Argv, "\x00") != strings.Join(dangerous, "\x00") {
		t.Fatalf("dry-run argv = %v", res.Argv)
	}
}

// TestI5TrashUnavailableNeverUnlinks asserts fail-closed trash behavior.
func TestI5TrashUnavailableNeverUnlinks(t *testing.T) {
	base := t.TempDir()
	blocker := filepath.Join(base, "Trash")
	if err := os.WriteFile(blocker, []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := trash.New(blocker, os.Getuid())
	src := filepath.Join(t.TempDir(), "valuable")
	if err := os.WriteFile(src, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Put(src); err == nil {
		t.Fatal("expected Put to fail when trash is unavailable")
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatalf("source was removed: %v", err)
	}
}

// TestI6RootWithoutSudoRefuses asserts the process refuses to run as root.
func TestI6RootWithoutSudoRefuses(t *testing.T) {
	_, err := engine.Resolve(engine.ResolveOptions{
		Getenv:  func(string) string { return "" },
		Geteuid: func() int { return 0 },
	})
	if err == nil {
		t.Fatal("expected refusal for root without SUDO_USER")
	}
	if !strings.Contains(err.Error(), "root") {
		t.Fatalf("unexpected error: %v", err)
	}

	// A normal user resolves successfully with injected XDG dirs.
	env := map[string]string{
		"XDG_CONFIG_HOME": t.TempDir(),
		"XDG_DATA_HOME":   t.TempDir(),
		"XDG_STATE_HOME":  t.TempDir(),
		"XDG_CACHE_HOME":  t.TempDir(),
	}
	resolved, err := engine.Resolve(engine.ResolveOptions{
		Getenv:     func(k string) string { return env[k] },
		Geteuid:    func() int { return 1000 },
		ConfigPath: filepath.Join(env["XDG_CONFIG_HOME"], "burrow", "config.toml"),
	})
	if err != nil {
		t.Fatalf("expected non-root resolution to succeed: %v", err)
	}
	if resolved.Cfg == nil || resolved.Whitelist == nil {
		t.Fatal("resolution did not populate config and whitelist")
	}
}

// TestI7MutableAncestorGuardStop asserts a privileged path with a user-mutable
// ancestor produces a guard-stop that maps to exit code 75.
func TestI7MutableAncestorGuardStop(t *testing.T) {
	base := t.TempDir()
	mutable := filepath.Join(base, "writable")
	if err := os.Mkdir(mutable, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(mutable, 0o777); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(mutable, "victim")
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ancestor, found := safe.MutableAncestor(target)
	if !found {
		t.Fatal("expected to find a user-mutable ancestor")
	}
	err := safe.GuardStop("privileged path has a user-mutable ancestor: %s", ancestor)
	if code := cli.ExitCode(err); code != cli.ExitGuard {
		t.Fatalf("guard-stop exit code = %d, want %d", code, cli.ExitGuard)
	}
}

func writeFixture(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func digestTree(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := rel + "|" + info.Mode().String() + "|" + info.ModTime().UTC().Format(time.RFC3339Nano)
		if !d.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(data)
			entry += "|" + hex.EncodeToString(sum[:])
		}
		lines = append(lines, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

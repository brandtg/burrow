package safe_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/brandtg/burrow/internal/safe"
)

func TestGuardTriState(t *testing.T) {
	if !safe.OK().Pass() {
		t.Fatal("OK should pass")
	}
	for _, g := range []safe.GuardResult{safe.BusyGuard("owner running"), safe.UnknownGuard("cannot tell")} {
		if g.Pass() {
			t.Fatalf("%s should not pass", g.State)
		}
		if g.Err() == nil {
			t.Fatalf("%s should return an error", g.State)
		}
	}
}

func TestStopWithEmptyReasonFailsClosed(t *testing.T) {
	g := safe.Stop(safe.Unknown, "")
	if g.Pass() {
		t.Fatal("empty-reason stop must not pass")
	}
	if g.Reason == "" {
		t.Fatal("expected a non-empty fail-closed reason")
	}
}

func TestStopCannotDowngradeToSafe(t *testing.T) {
	g := safe.Stop(safe.Safe, "oops")
	if g.Pass() {
		t.Fatal("Stop must never produce a passing result")
	}
	if g.State != safe.Unknown {
		t.Fatalf("expected Unknown, got %s", g.State)
	}
}

func TestMutableAncestor(t *testing.T) {
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
	got, found := safe.MutableAncestor(target)
	if !found {
		t.Fatal("expected a mutable ancestor")
	}
	if got != mutable {
		t.Fatalf("expected %s, got %s", mutable, got)
	}
}

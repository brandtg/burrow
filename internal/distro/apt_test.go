package distro

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/brandtg/burrow/internal/safe"
)

func TestCheckLockProbeAbsentLockIsSafe(t *testing.T) {
	res := CheckLockProbe(context.Background(), LockProbe{
		LockFiles: []string{filepath.Join(t.TempDir(), "no-such.lock")},
	})
	if !res.Pass() {
		t.Fatalf("absent lock must be safe, got %+v", res)
	}
}

func TestCheckLockProbeHeldLockIsBusy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	res := CheckLockProbe(context.Background(), LockProbe{LockFiles: []string{path}})
	if res.Pass() || res.State != safe.Busy {
		t.Fatalf("held lock must be busy, got %+v", res)
	}
}

func TestLockHeldViaProcLocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("no stat_t")
	}
	held, err := lockHeld(uint64(st.Dev), st.Ino)
	if err != nil {
		t.Skipf("/proc/locks unavailable: %v", err)
	}
	if !held {
		t.Fatal("expected /proc/locks to report the held lock")
	}

	// A file with no lock must not be reported as held.
	other := filepath.Join(t.TempDir(), "free.lock")
	if err := os.WriteFile(other, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ofi, err := os.Stat(other)
	if err != nil {
		t.Fatal(err)
	}
	ost := ofi.Sys().(*syscall.Stat_t)
	free, err := lockHeld(uint64(ost.Dev), ost.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if free {
		t.Fatal("unlocked file reported as held")
	}
}

func TestAptAdapterCommands(t *testing.T) {
	a := For(Info{ID: "ubuntu"})
	cache := a.PackageCacheAction()
	if cache.Argv[0] != "apt-get" || cache.Argv[1] != "clean" || !cache.NeedsRoot {
		t.Fatalf("package cache command = %+v", cache)
	}
	res, err := a.ResidualConfigAction([]string{"bash", "zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Argv) != 4 || res.Argv[0] != "dpkg" || res.Argv[1] != "--purge" {
		t.Fatalf("residual command = %v", res.Argv)
	}
	if _, err := a.ResidualConfigAction([]string{"--help"}); err == nil {
		t.Fatal("a leading-dash package name must be rejected")
	}
}

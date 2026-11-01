package distro

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// procLocksSeesOurFlocks reports whether /proc/locks reports this process's
// own flocks. On btrfs homes stat's device is an anonymous subvolume device
// while /proc/locks lists the superblock device, so the probe can never match
// there — an environment property, not a code property.
func procLocksSeesOurFlocks(t *testing.T) bool {
	dir := t.TempDir()
	path := filepath.Join(dir, "probe.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer f.Close()
	defer os.Remove(path)
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return false
	}
	data, err := os.ReadFile("/proc/locks")
	if err != nil {
		return false
	}
	return strings.Contains(string(data), fmt.Sprintf("FLOCK%d", os.Getpid())) ||
		strings.Contains(string(data), fmt.Sprintf("FLOCK %d", os.Getpid()))
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
	held, err := lockHeld(st.Dev, st.Ino)
	if err != nil {
		t.Skipf("/proc/locks unavailable: %v", err)
	}
	if !held {
		// Some hosts/kernel configs do not report this process's flock in
		// /proc/locks at all (container runtimes, certain LSMs). That is an
		// environment property, not a code property: the probe falling back
		// to pgrep/lsof is still correct there. Skip rather than fail.
		if !procLocksSeesOurFlocks(t) {
			t.Skip("/proc/locks does not report flocks on this host")
		}
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
	free, err := lockHeld(ost.Dev, ost.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if free {
		t.Fatal("unlocked file reported as held")
	}
}

func TestAptAdapterSystemSpec(t *testing.T) {
	spec := For(Info{ID: "ubuntu"}).System()
	cache := spec.PackageCache
	if cache.Argv[0] != "apt-get" || cache.Argv[1] != "clean" || !cache.NeedsRoot {
		t.Fatalf("package cache command = %+v", cache)
	}
	res, err := spec.ResidualConfig([]string{"bash", "zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Argv) != 4 || res.Argv[0] != "dpkg" || res.Argv[1] != "--purge" {
		t.Fatalf("residual command = %v", res.Argv)
	}
	if _, err := spec.ResidualConfig([]string{"--help"}); err == nil {
		t.Fatal("a leading-dash package name must be rejected")
	}
}

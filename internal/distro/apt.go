package distro

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// LockProbe names the resources whose activity means a package manager is
// busy. It is data so Plan 07 can add dnf/pacman/zypper probes without
// reshaping the guard.
type LockProbe struct {
	LockFiles []string
	Processes []string
}

// aptProcesses are the process names that indicate apt/dpkg work in progress.
var aptProcesses = []string{"apt", "apt-get", "dpkg", "unattended-upgr", "aptd"}

// aptProbe is the Debian/Ubuntu lock probe.
func aptProbe() LockProbe {
	return LockProbe{
		LockFiles: []string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock"},
		Processes: aptProcesses,
	}
}

// CheckLockProbe evaluates a LockProbe. It fails closed: a probe that cannot be
// evaluated returns Unknown, which stops the action (spec §7.5).
func CheckLockProbe(ctx context.Context, p LockProbe) safe.GuardResult {
	for _, name := range p.Processes {
		running, err := processRunning(name)
		if err != nil {
			return safe.UnknownGuard(fmt.Sprintf("cannot inspect processes for %s: %v", name, err))
		}
		if running {
			return safe.BusyGuard(fmt.Sprintf("%s is running", name))
		}
	}
	for _, lock := range p.LockFiles {
		if res := probeLock(lock); !res.Pass() {
			return res
		}
	}
	return safe.OK()
}

// probeLock reports whether path is locked. A missing lock is safe; a held
// lock is Busy; anything that cannot be proven is Unknown.
//
// The preferred probe is a non-blocking flock. When the lock file cannot be
// opened (it is usually root-only, 0640), it falls back to /proc/locks, which
// is world-readable and lists every held lock by device and inode. That lets a
// normal invoking user still prove the package manager is idle.
func probeLock(path string) safe.GuardResult {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return safe.OK()
		}
		return safe.UnknownGuard(fmt.Sprintf("cannot inspect lock %s: %v", path, err))
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return safe.UnknownGuard(fmt.Sprintf("cannot read stat for lock %s", path))
	}
	if f, oerr := os.OpenFile(path, os.O_RDONLY, 0); oerr == nil {
		ferr := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if ferr == nil {
			_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
			_ = f.Close()
			return safe.OK()
		}
		_ = f.Close()
		if errors.Is(ferr, syscall.EWOULDBLOCK) {
			return safe.BusyGuard(fmt.Sprintf("package manager lock is held: %s", path))
		}
		// Fall through to the /proc/locks probe on any other error.
	}
	held, herr := lockHeld(st.Dev, st.Ino)
	if herr != nil {
		return safe.UnknownGuard(fmt.Sprintf("cannot inspect lock %s: %v", path, herr))
	}
	if held {
		return safe.BusyGuard(fmt.Sprintf("package manager lock is held: %s", path))
	}
	return safe.OK()
}

// lockHeld reports whether /proc/locks contains a lock on the given device and
// inode.
func lockHeld(dev, ino uint64) (bool, error) {
	data, err := os.ReadFile("/proc/locks")
	if err != nil {
		return false, err
	}
	major := (dev >> 8) & 0xfff
	minor := (dev & 0xff) | ((dev >> 12) & 0xfff00)
	target := fmt.Sprintf("%02x:%02x:%d", major, minor, ino)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 6 && fields[5] == target {
			return true, nil
		}
	}
	return false, nil
}

// processRunning reports whether a process whose comm basename is name exists.
// It reads /proc directly so it does not depend on pgrep being installed.
func processRunning(name string) (bool, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false, err
	}
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(de.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", de.Name(), "comm"))
		if err != nil {
			// The process may have exited; skip it.
			continue
		}
		if strings.TrimSpace(string(comm)) == name {
			return true, nil
		}
	}
	return false, nil
}

// --- Adapter command methods. ---

func (aptAdapter) PackageCacheAction() owner.OwnerCommand { return owner.AptPackageCache() }
func (aptAdapter) OrphansAction() owner.OwnerCommand      { return owner.AptOrphans() }
func (aptAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.AptOldKernels() }
func (aptAdapter) ResidualConfigAction(pkgs []string) (owner.OwnerCommand, error) {
	return owner.AptResidualConfig(pkgs)
}
func (aptAdapter) LockGuard() LockProbe { return aptProbe() }

// The remaining families are detection stubs until their plans land (Plan 07
// lands dnf; pacman/zypper/apk remain data to add). Returning a zero
// OwnerCommand and an empty SystemSpec makes the system catalog skip their
// package items rather than guess an argv.

func errNoResidualConfig(family string) error {
	return fmt.Errorf("distro: residual-config is not implemented for %s", family)
}

func (dnfAdapter) LockGuard() LockProbe {
	return LockProbe{
		LockFiles: []string{"/var/lib/rpm/.rpm.lock", "/var/cache/dnf/metadata_lock.pid"},
		Processes: []string{"dnf", "rpm"},
	}
}

func (pacmanAdapter) PackageCacheAction() owner.OwnerCommand { return owner.OwnerCommand{} }
func (pacmanAdapter) OrphansAction() owner.OwnerCommand      { return owner.OwnerCommand{} }
func (pacmanAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.OwnerCommand{} }
func (pacmanAdapter) ResidualConfigAction([]string) (owner.OwnerCommand, error) {
	return owner.OwnerCommand{}, errNoResidualConfig("pacman")
}
func (pacmanAdapter) LockGuard() LockProbe {
	return LockProbe{LockFiles: []string{"/var/lib/pacman/db.lck"}, Processes: []string{"pacman"}}
}
func (pacmanAdapter) System() SystemSpec { return SystemSpec{} }

func (zypperAdapter) PackageCacheAction() owner.OwnerCommand { return owner.OwnerCommand{} }
func (zypperAdapter) OrphansAction() owner.OwnerCommand      { return owner.OwnerCommand{} }
func (zypperAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.OwnerCommand{} }
func (zypperAdapter) ResidualConfigAction([]string) (owner.OwnerCommand, error) {
	return owner.OwnerCommand{}, errNoResidualConfig("zypper")
}
func (zypperAdapter) LockGuard() LockProbe {
	return LockProbe{LockFiles: []string{"/var/run/zypp.pid"}, Processes: []string{"zypper"}}
}
func (zypperAdapter) System() SystemSpec { return SystemSpec{} }

func (apkAdapter) PackageCacheAction() owner.OwnerCommand { return owner.OwnerCommand{} }
func (apkAdapter) OrphansAction() owner.OwnerCommand      { return owner.OwnerCommand{} }
func (apkAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.OwnerCommand{} }
func (apkAdapter) ResidualConfigAction([]string) (owner.OwnerCommand, error) {
	return owner.OwnerCommand{}, errNoResidualConfig("apk")
}
func (apkAdapter) LockGuard() LockProbe {
	return LockProbe{LockFiles: []string{"/lib/apk/db/lock"}, Processes: []string{"apk"}}
}
func (apkAdapter) System() SystemSpec { return SystemSpec{} }

func (genericAdapter) PackageCacheAction() owner.OwnerCommand { return owner.OwnerCommand{} }
func (genericAdapter) OrphansAction() owner.OwnerCommand      { return owner.OwnerCommand{} }
func (genericAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.OwnerCommand{} }
func (genericAdapter) ResidualConfigAction([]string) (owner.OwnerCommand, error) {
	return owner.OwnerCommand{}, errors.New("distro: no package manager detected")
}
func (genericAdapter) LockGuard() LockProbe { return LockProbe{} }
func (genericAdapter) System() SystemSpec   { return SystemSpec{} }

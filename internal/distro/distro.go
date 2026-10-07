// Package distro detects the Linux distribution and exposes a per-family
// adapter. v0.1 exercises the Fedora (dnf) and Debian/Ubuntu (apt) paths;
// pacman/zypper/apk are detected and wired so command tables can land later
// without reshaping the framework.
package distro

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/brandtg/burrow/internal/owner"
)

// Info is the parsed /etc/os-release.
type Info struct {
	ID         string
	IDLike     []string
	VersionID  string
	PrettyName string
}

// Detect reads /etc/os-release.
func Detect() (Info, error) {
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	return DetectFrom(f)
}

// DetectFrom parses an os-release stream.
func DetectFrom(r io.Reader) (Info, error) {
	info := Info{}
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, `"'`)
		switch key {
		case "ID":
			info.ID = value
		case "ID_LIKE":
			info.IDLike = strings.Fields(value)
		case "VERSION_ID":
			info.VersionID = value
		case "PRETTY_NAME":
			info.PrettyName = value
		}
	}
	if err := sc.Err(); err != nil {
		return Info{}, err
	}
	return info, nil
}

// Family returns the package-manager family for the detected distro. This is
// the single switch every distro-specific behavior keys off.
func (i Info) Family() string {
	ids := append([]string{i.ID}, i.IDLike...)
	for _, id := range ids {
		switch id {
		case "debian", "ubuntu":
			return "apt"
		case "fedora", "rhel", "centos", "rocky", "almalinux":
			return "dnf"
		case "arch", "archlinux", "manjaro", "endeavouros":
			return "pacman"
		case "opensuse", "opensuse-leap", "opensuse-tumbleweed", "suse", "sles":
			return "zypper"
		case "alpine":
			return "apk"
		}
	}
	return "unknown"
}

// Adapter describes a package-manager family. The command methods return
// allow-listed argv (invariant I4); a zero-value OwnerCommand means the family
// does not implement that item yet, and the system catalog skips it.
type Adapter interface {
	// ID is the family identifier, e.g. "apt".
	ID() string
	// PackageManager is the primary binary, e.g. "apt-get".
	PackageManager() string
	// LockFiles are the paths whose presence indicates a busy package manager.
	LockFiles() []string
	// NeedsRoot reports whether owner commands for this family need elevation.
	NeedsRoot() bool
	// PackageCacheAction empties the package-manager cache.
	PackageCacheAction() owner.OwnerCommand
	// OrphansAction removes packages no longer required.
	OrphansAction() owner.OwnerCommand
	// ResidualConfigAction purges the given residual-config packages.
	ResidualConfigAction(pkgs []string) (owner.OwnerCommand, error)
	// OldKernelsAction removes old kernels.
	OldKernelsAction() owner.OwnerCommand
	// LockGuard names the locks and processes that indicate a busy manager.
	LockGuard() LockProbe
	// System returns the data behind the generic `system` catalog items so the
	// catalog stays distro-agnostic (Plan 07). It is read-only data.
	System() SystemSpec
}

// For returns the adapter for an os-release.
func For(info Info) Adapter {
	switch info.Family() {
	case "apt":
		return aptAdapter{}
	case "dnf":
		return dnfAdapter{}
	case "pacman":
		return pacmanAdapter{}
	case "zypper":
		return zypperAdapter{}
	case "apk":
		return apkAdapter{}
	default:
		return genericAdapter{}
	}
}

type aptAdapter struct{}

func (aptAdapter) ID() string             { return "apt" }
func (aptAdapter) PackageManager() string { return "apt-get" }
func (aptAdapter) LockFiles() []string {
	return []string{"/var/lib/dpkg/lock-frontend", "/var/lib/dpkg/lock"}
}
func (aptAdapter) NeedsRoot() bool { return true }

type dnfAdapter struct{}

func (dnfAdapter) ID() string             { return "dnf" }
func (dnfAdapter) PackageManager() string { return "dnf" }
func (dnfAdapter) LockFiles() []string {
	return []string{"/var/lib/rpm/.rpm.lock", "/var/cache/dnf/metadata_lock.pid"}
}
func (dnfAdapter) NeedsRoot() bool { return true }

type pacmanAdapter struct{}

func (pacmanAdapter) ID() string             { return "pacman" }
func (pacmanAdapter) PackageManager() string { return "pacman" }
func (pacmanAdapter) LockFiles() []string    { return []string{"/var/lib/pacman/db.lck"} }
func (pacmanAdapter) NeedsRoot() bool        { return true }

type zypperAdapter struct{}

func (zypperAdapter) ID() string             { return "zypper" }
func (zypperAdapter) PackageManager() string { return "zypper" }
func (zypperAdapter) LockFiles() []string    { return []string{"/var/run/zypp.pid"} }
func (zypperAdapter) NeedsRoot() bool        { return true }

type apkAdapter struct{}

func (apkAdapter) ID() string             { return "apk" }
func (apkAdapter) PackageManager() string { return "apk" }
func (apkAdapter) LockFiles() []string    { return []string{"/lib/apk/db/lock"} }
func (apkAdapter) NeedsRoot() bool        { return true }

type genericAdapter struct{}

func (genericAdapter) ID() string             { return "unknown" }
func (genericAdapter) PackageManager() string { return "" }
func (genericAdapter) LockFiles() []string    { return nil }
func (genericAdapter) NeedsRoot() bool        { return false }

// String renders a short human description.
func (i Info) String() string {
	name := i.PrettyName
	if name == "" {
		name = i.ID
	}
	if name == "" {
		name = "unknown"
	}
	return fmt.Sprintf("%s (%s)", name, i.Family())
}

package owner

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// OwnerCommand is an allow-listed argv from the catalog (invariant I4). Argv is
// passed verbatim to the Runner; it is never built from user input. The only
// command whose argv varies with discovered data is the dpkg purge list, and
// that list is validated against the parsed residual-config set before use.
type OwnerCommand struct {
	ID        string
	Argv      []string
	NeedsRoot bool
	ReadOnly  bool
	Timeout   time.Duration
}

// Valid reports whether the command has an id and a non-empty argv.
func (c OwnerCommand) Valid() bool { return c.ID != "" && len(c.Argv) > 0 }

// --- Debian/Ubuntu (apt/dpkg) owner commands. ---

// AptPackageCache empties /var/cache/apt/archives.
func AptPackageCache() OwnerCommand {
	return OwnerCommand{
		ID:        "system.pkg-cache",
		Argv:      []string{"apt-get", "clean"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// AptOrphans removes packages no longer required by anything.
func AptOrphans() OwnerCommand {
	return OwnerCommand{
		ID:        "system.orphans",
		Argv:      []string{"apt-get", "autoremove", "--purge"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// AptOldKernels removes old kernels. apt owns the selection; the argv is the
// same autoremove invocation.
func AptOldKernels() OwnerCommand {
	return OwnerCommand{
		ID:        "system.old-kernels",
		Argv:      []string{"apt-get", "autoremove", "--purge"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// AptResidualConfig purges a package list derived from `dpkg -l` output. Every
// name is validated before it becomes an argument so no user input, file
// content, or option can be smuggled into the argv (I4). The caller must also
// ensure the list is a subset of the parsed `^rc` set.
func AptResidualConfig(pkgs []string) (OwnerCommand, error) {
	if len(pkgs) == 0 {
		return OwnerCommand{}, errors.New("owner: empty residual-config package list")
	}
	for _, p := range pkgs {
		if !ValidPackageName(p) {
			return OwnerCommand{}, fmt.Errorf("owner: invalid package name %q", p)
		}
	}
	argv := make([]string, 0, len(pkgs)+2)
	argv = append(argv, "dpkg", "--purge")
	argv = append(argv, pkgs...)
	return OwnerCommand{
		ID:        "system.residual-config",
		Argv:      argv,
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}, nil
}

// --- Fedora/RHEL (dnf/rpm) owner commands. ---

// DnfPackageCache empties the dnf package cache.
func DnfPackageCache() OwnerCommand {
	return OwnerCommand{
		ID:        "system.pkg-cache",
		Argv:      []string{"dnf", "clean", "all"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// DnfOrphans removes packages no longer required by anything.
func DnfOrphans() OwnerCommand {
	return OwnerCommand{
		ID:        "system.orphans",
		Argv:      []string{"dnf", "autoremove", "-y"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// DnfOldKernels removes old kernels, keeping the running one plus one. dnf owns
// the selection; package-cleanup is the documented helper.
func DnfOldKernels() OwnerCommand {
	return OwnerCommand{
		ID:        "system.old-kernels",
		Argv:      []string{"package-cleanup", "--oldkernels", "--count=2"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// DnfOrphansProbe lists packages dnf considers unneeded (read-only). The
// format string carries a literal newline escape so dnf emits one name per
// line; without it dnf concatenates every name into a single token.
func DnfOrphansProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.dnf.unneeded", Argv: []string{"dnf", "repoquery", "--unneeded", "--qf", "%{name}\\n"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// RpmKernelProbe lists installed kernel packages (read-only).
func RpmKernelProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.rpm.kernels", Argv: []string{"rpm", "-q", "kernel"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// --- systemd owner commands. ---

// JournaldVacuumSize bounds the journal by size.
func JournaldVacuumSize(size string) OwnerCommand {
	return OwnerCommand{
		ID:        "system.journald",
		Argv:      []string{"journalctl", "--vacuum-size=" + size},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// JournaldVacuumTime bounds the journal by age.
func JournaldVacuumTime(age string) OwnerCommand {
	return OwnerCommand{
		ID:        "system.journald",
		Argv:      []string{"journalctl", "--vacuum-time=" + age},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// SystemdTmpfilesClean applies the tmpfiles aging rules.
func SystemdTmpfilesClean(id string) OwnerCommand {
	return OwnerCommand{
		ID:        id,
		Argv:      []string{"systemd-tmpfiles", "--clean"},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}
}

// --- Per-user language/toolchain owner commands. ---
//
// These are inherently per-user and must never be elevated: they operate on the
// invoking user's own cache. NeedsRoot is false and the runner resolves the
// binary as that user.
//
// Provenance: the keep/discard reasoning for each toolchain is a behavioral
// port of Mole's
// lib/clean/dev.sh, reimplemented clean-room.

// NpmCacheClean clears the npm cache.
func NpmCacheClean() OwnerCommand {
	return OwnerCommand{ID: "dev.npm", Argv: []string{"npm", "cache", "clean", "--force"}, Timeout: TimeoutOwnerClean}
}

// PnpmStorePrune prunes the pnpm content-addressable store.
func PnpmStorePrune() OwnerCommand {
	return OwnerCommand{ID: "dev.pnpm", Argv: []string{"pnpm", "store", "prune"}, Timeout: TimeoutOwnerClean}
}

// BunCacheRm removes the bun package cache.
func BunCacheRm() OwnerCommand {
	return OwnerCommand{ID: "dev.bun", Argv: []string{"bun", "pm", "cache", "rm"}, Timeout: TimeoutOwnerClean}
}

// GoCleanModCache clears the module download cache.
func GoCleanModCache() OwnerCommand {
	return OwnerCommand{ID: "dev.go-mod", Argv: []string{"go", "clean", "-modcache"}, Timeout: TimeoutOwnerClean}
}

// GoCleanCache clears the Go build cache. Go documents this as safe to run
// concurrently with a build, so it carries no process guard.
func GoCleanCache() OwnerCommand {
	return OwnerCommand{ID: "dev.go-build", Argv: []string{"go", "clean", "-cache"}, Timeout: TimeoutOwnerClean}
}

// PipCachePurge purges the pip wheel cache.
func PipCachePurge() OwnerCommand {
	return OwnerCommand{ID: "dev.pip", Argv: []string{"pip3", "cache", "purge"}, Timeout: TimeoutOwnerClean}
}

// UvCachePrune prunes the uv cache.
func UvCachePrune() OwnerCommand {
	return OwnerCommand{ID: "dev.uv", Argv: []string{"uv", "cache", "prune"}, Timeout: TimeoutOwnerClean}
}

// CondaClean removes conda index caches, tarballs, and logfiles.
func CondaClean() OwnerCommand {
	return OwnerCommand{
		ID:      "dev.conda",
		Argv:    []string{"conda", "clean", "--yes", "--index-cache", "--tarballs", "--logfiles"},
		Timeout: TimeoutOwnerClean,
	}
}

// NixCollectGarbage deletes generations older than 30 days.
func NixCollectGarbage() OwnerCommand {
	return OwnerCommand{
		ID:      "dev.nix",
		Argv:    []string{"nix-collect-garbage", "--delete-older-than", "30d"},
		Timeout: TimeoutOwnerClean,
	}
}

// CorepackCacheClean clears the corepack cache.
func CorepackCacheClean() OwnerCommand {
	return OwnerCommand{ID: "dev.corepack", Argv: []string{"corepack", "cache", "clean"}, Timeout: TimeoutOwnerClean}
}

// MiseCacheClear clears the mise tool cache.
func MiseCacheClear() OwnerCommand {
	return OwnerCommand{ID: "dev.mise", Argv: []string{"mise", "cache", "clear"}, Timeout: TimeoutOwnerClean}
}

// GhClearCache clears the GitHub CLI cache. The runner sets the invoking user's
// XDG_CACHE_HOME for per-user commands, so gh clears the same cache directory
// that discovery resolved, without
// building a dynamic argv.
func GhClearCache() OwnerCommand {
	return OwnerCommand{ID: "dev.gh-cli", Argv: []string{"gh", "config", "clear-cache"}, Timeout: TimeoutOwnerClean}
}

// --- Containers: docker/podman owner commands. ---
//
// Docker/podman are typically reachable as the invoking user (rootless or the
// docker group), so these run without elevation. If the socket is root-only the
// command fails; burrow never assumes elevation on the user's behalf.

// DockerBuilderPrune reclaims the build cache.
func DockerBuilderPrune() OwnerCommand {
	return OwnerCommand{ID: "containers.docker-build-cache", Argv: []string{"docker", "builder", "prune", "-f"}, Timeout: TimeoutOwnerClean}
}

// DockerImagePrune removes dangling images only.
func DockerImagePrune() OwnerCommand {
	return OwnerCommand{ID: "containers.docker-dangling", Argv: []string{"docker", "image", "prune", "-f"}, Timeout: TimeoutOwnerClean}
}

// DockerImagePruneAll removes every unused image.
func DockerImagePruneAll() OwnerCommand {
	return OwnerCommand{ID: "containers.docker-unused", Argv: []string{"docker", "image", "prune", "-a", "-f"}, Timeout: TimeoutOwnerClean}
}

// DockerVolumePrune removes unused volumes. Volumes can hold data, not just
// cache; this is a high-risk action.
func DockerVolumePrune() OwnerCommand {
	return OwnerCommand{ID: "containers.docker-volumes", Argv: []string{"docker", "volume", "prune", "-f"}, Timeout: TimeoutOwnerClean}
}

// DockerSystemPruneAll is the superset prune: every unused image, container,
// network, build cache, and volume.
func DockerSystemPruneAll() OwnerCommand {
	return OwnerCommand{ID: "containers.docker-all", Argv: []string{"docker", "system", "prune", "-a", "--volumes", "-f"}, Timeout: TimeoutOwnerClean}
}

// PodmanSystemPrune removes unused podman state. Volumes are not included in
// v0.1 (no opt-in surface yet).
func PodmanSystemPrune() OwnerCommand {
	return OwnerCommand{ID: "containers.podman", Argv: []string{"podman", "system", "prune", "-f"}, Timeout: TimeoutOwnerClean}
}

// DockerSystemDFProbe reports docker's per-category disk usage (read-only).
func DockerSystemDFProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.docker.df", Argv: []string{"docker", "system", "df"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// DockerInfoProbe checks that the docker daemon is reachable (read-only; it
// never triggers a pull).
func DockerInfoProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.docker.info", Argv: []string{"docker", "info"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// PodmanInfoProbe checks that podman is usable (read-only).
func PodmanInfoProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.podman.info", Argv: []string{"podman", "info"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// --- Desktop packages: flatpak/snap owner commands. ---

// FlatpakUninstallUnused removes unused runtimes. It is per-user; it must never
// run as root.
func FlatpakUninstallUnused() OwnerCommand {
	return OwnerCommand{ID: "flatpak.unused", Argv: []string{"flatpak", "uninstall", "--unused", "-y"}, Timeout: TimeoutOwnerClean}
}

// FlatpakRepair repairs the system flatpak installation and needs root.
func FlatpakRepair() OwnerCommand {
	return OwnerCommand{ID: "flatpak.repair", Argv: []string{"flatpak", "repair"}, NeedsRoot: true, Timeout: TimeoutOwnerClean}
}

// FlatpakListAppsProbe lists installed application ids (read-only).
func FlatpakListAppsProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.flatpak.apps", Argv: []string{"flatpak", "list", "--app", "--columns=application"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// SnapListProbe lists installed snaps (read-only).
func SnapListProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.snap.list", Argv: []string{"snap", "list"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// SnapListAllProbe lists installed snaps including disabled revisions
// (read-only).
func SnapListAllProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.snap.list-all", Argv: []string{"snap", "list", "--all"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// SnapRemoveRevision purges one disabled revision. The name and revision come
// from `snap list --all` output and are validated before they become argv so no
// discovered string can smuggle an option into the command (invariant I4).
func SnapRemoveRevision(name, revision string) (OwnerCommand, error) {
	if !ValidSnapName(name) {
		return OwnerCommand{}, fmt.Errorf("owner: invalid snap name %q", name)
	}
	if !ValidSnapRevision(revision) {
		return OwnerCommand{}, fmt.Errorf("owner: invalid snap revision %q", revision)
	}
	return OwnerCommand{
		ID:        "snap.old-revisions",
		Argv:      []string{"snap", "remove", "--purge", name, "--revision", revision},
		NeedsRoot: true,
		Timeout:   TimeoutOwnerClean,
	}, nil
}

// ValidSnapName reports whether s is a safe snap name: lowercase letters,
// digits and single hyphens, not starting or ending with a hyphen. A leading
// '-' is rejected so a name can never be read as an option.
func ValidSnapName(s string) bool {
	if s == "" || s[0] == '-' || s[len(s)-1] == '-' {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-':
		default:
			return false
		}
	}
	return true
}

// ValidSnapRevision reports whether s is a decimal snap revision.
func ValidSnapRevision(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// --- Read-only probes used during discovery (never elevated, never mutating). ---

// NpmCacheProbe asks npm where its cache lives.
func NpmCacheProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.npm.cache", Argv: []string{"npm", "config", "get", "cache"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// PnpmStoreProbe asks pnpm where its store lives.
func PnpmStoreProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.pnpm.store", Argv: []string{"pnpm", "store", "path"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// BunCacheProbe asks bun where its cache lives.
func BunCacheProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.bun.cache", Argv: []string{"bun", "pm", "cache"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// GoModCacheProbe reports GOMODCACHE.
func GoModCacheProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.go.modcache", Argv: []string{"go", "env", "GOMODCACHE"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// GoBuildCacheProbe reports GOCACHE.
func GoBuildCacheProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.go.gocache", Argv: []string{"go", "env", "GOCACHE"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// PipCacheDirProbe reports the pip cache directory.
func PipCacheDirProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.pip.cache", Argv: []string{"pip3", "cache", "dir"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// UvCacheDirProbe reports the uv cache directory.
func UvCacheDirProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.uv.cache", Argv: []string{"uv", "cache", "dir"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// MiseCachePathProbe reports the mise cache directory.
func MiseCachePathProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.mise.cache", Argv: []string{"mise", "cache", "path"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// AptOrphanProbe simulates an autoremove and reports what it would remove.
func AptOrphanProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.apt.orphans", Argv: []string{"apt-get", "-s", "autoremove"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// DpkgListProbe lists installed and residual-config packages.
func DpkgListProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.dpkg.list", Argv: []string{"dpkg", "-l"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// DpkgKernelProbe lists installed kernel image packages.
func DpkgKernelProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.dpkg.kernels", Argv: []string{"dpkg-query", "-W", "-f=${Package}\n", "linux-image-*"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// JournaldDiskUsageProbe reports the on-disk journal size.
func JournaldDiskUsageProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.journald.disk", Argv: []string{"journalctl", "--disk-usage"}, ReadOnly: true, Timeout: TimeoutSizeProbe}
}

// UnameProbe reports the running kernel release.
func UnameProbe() OwnerCommand {
	return OwnerCommand{ID: "probe.uname", Argv: []string{"uname", "-r"}, ReadOnly: true, Timeout: TimeoutQuickDetect}
}

// ValidPackageName reports whether s is a safe dpkg package name: lowercase
// alphanumerics plus '+', '-' and '.', starting with an alphanumeric. A leading
// '-' is rejected so a name can never be read as an option.
func ValidPackageName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case (c == '+' || c == '-' || c == '.') && i > 0:
		default:
			return false
		}
	}
	return true
}

// ValidatePackageSubset returns an error if any name in pkgs is absent from the
// allow set. This is the check that keeps `dpkg --purge` bounded to packages
// the tool itself parsed (invariant I4).
func ValidatePackageSubset(pkgs []string, allow map[string]bool) error {
	var bad []string
	for _, p := range pkgs {
		if !allow[p] {
			bad = append(bad, p)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("owner: package list not in parsed set: %s", strings.Join(bad, ", "))
	}
	return nil
}

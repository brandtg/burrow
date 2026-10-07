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

// --- Debian/Ubuntu (apt/dpkg) owner commands (spec §8.1, §21.1). ---

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

// --- Per-user language/toolchain owner commands (spec §8.4, §21.1). ---
//
// These are inherently per-user and must never be elevated: they operate on the
// invoking user's own cache. NeedsRoot is false and the runner resolves the
// binary as that user (spec §7.6).
//
// Provenance: the owner-command list is cross-checked against spec §21.1; the
// keep/discard reasoning for each toolchain is a behavioral port of Mole's
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
// that discovery resolved (spec §8.4's `XDG_CACHE_HOME=<parent>` intent) without
// building a dynamic argv.
func GhClearCache() OwnerCommand {
	return OwnerCommand{ID: "dev.gh-cli", Argv: []string{"gh", "config", "clear-cache"}, Timeout: TimeoutOwnerClean}
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
// the tool itself parsed (I4 / plan 03 note).
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

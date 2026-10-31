// Package system implements the `system` cleanup category: package
// manager caches, the systemd journal, logs, temp files, and core dumps.
//
// Debian/Ubuntu apt/dpkg is the exercised path. Every mutating command is an
// allow-listed owner command (invariant I4); nothing here hand-deletes
// package-manager or OS state. Discovery and measurement are read-only.
package system

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// ID is the category identifier.
const ID = "system"

// Category enumerates system cleanup targets.
type Category struct{}

// New returns the system category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "System" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Package manager, journal, logs, and temp files. Owner commands; reclaimed state is re-downloadable, not recoverable."
}

// Discover enumerates system targets. Per-item probe failures are skipped, not
// returned as errors, so one missing tool never blanks the category.
func (*Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	var out []engine.Target
	out = append(out, packageItems(ctx, env)...)
	out = append(out, journaldItem(ctx, env)...)
	out = append(out, tmpfilesItems(env)...)
	out = append(out, coreDumpItems(env)...)
	return out, nil
}

// lockGuard adapts the adapter's lock probe to the engine Guard contract.
type lockGuard struct{ probe distro.LockProbe }

func (g lockGuard) Check(ctx context.Context, env *engine.Env) error {
	return distro.CheckLockProbe(ctx, g.probe).Err()
}

// commandAvailable reports whether an owner command's binary is on PATH. It
// keeps discovery from offering an item whose owner is absent (for example
// `package-cleanup`, which ships in dnf-utils, not base dnf).
func commandAvailable(cmd owner.OwnerCommand) bool {
	if !cmd.Valid() {
		return false
	}
	return owner.Available(cmd.Argv[0])
}

func packageItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Adapter == nil {
		return nil
	}
	spec := env.Adapter.System()
	guard := lockGuard{probe: env.Adapter.LockGuard()}
	var out []engine.Target

	if spec.PackageCache.Valid() && commandAvailable(spec.PackageCache) {
		cmd := spec.PackageCache
		out = append(out, engine.Target{
			ID:        cmd.ID,
			Category:  ID,
			Display:   "package cache",
			Path:      spec.PackageCachePath,
			Action:    action.NewOwnerCommand(cmd, env.Run),
			Recovery:  engine.RecoveryOwner,
			NeedsRoot: cmd.NeedsRoot,
			Guard:     guard,
			Risk:      engine.RiskLow,
			DefaultOn: true,
			Evidence:  []string{spec.PackageCacheEvidence},
		})
	}

	if env.Run != nil && spec.Orphans.Valid() && spec.OrphansProbe.Valid() && commandAvailable(spec.Orphans) {
		if probe, err := env.Run.Probe(ctx, spec.OrphansProbe.Argv, owner.TimeoutSizeProbe); err == nil {
			if count, size, known := spec.ParseOrphans(probe); count > 0 {
				cmd := spec.Orphans
				out = append(out, engine.Target{
					ID:        cmd.ID,
					Category:  ID,
					Display:   "orphaned packages",
					SizeBytes: size,
					SizeKnown: known,
					Action:    action.NewOwnerCommand(cmd, env.Run),
					Recovery:  engine.RecoveryOwner,
					NeedsRoot: cmd.NeedsRoot,
					Guard:     guard,
					Risk:      engine.RiskHigh,
					DefaultOn: false,
					Evidence:  []string{"the package manager reports removable packages"},
				})
			}
		}
	}

	if env.Run != nil && spec.ResidualConfig != nil && spec.ResidualProbe.Valid() {
		if probe, err := env.Run.Probe(ctx, spec.ResidualProbe.Argv, owner.TimeoutSizeProbe); err == nil {
			pkgs := spec.ParseResidual(probe)
			allow := make(map[string]bool, len(pkgs))
			for _, p := range pkgs {
				allow[p] = true
			}
			// Defense in depth: the purge list must be a subset of the set we
			// parsed from the package-manager output (invariant I4).
			if len(pkgs) > 0 && owner.ValidatePackageSubset(pkgs, allow) == nil {
				if cmd, err := spec.ResidualConfig(pkgs); err == nil {
					out = append(out, engine.Target{
						ID:        cmd.ID,
						Category:  ID,
						Display:   "residual package config",
						Action:    action.NewOwnerCommand(cmd, env.Run),
						Recovery:  engine.RecoveryOwner,
						NeedsRoot: cmd.NeedsRoot,
						Guard:     guard,
						Risk:      engine.RiskMed,
						DefaultOn: false,
						Evidence:  []string{"the package manager reports residual config"},
					})
				}
			}
		}
	}

	if env.Run != nil && spec.OldKernels.Valid() && spec.KernelsProbe.Valid() && commandAvailable(spec.OldKernels) {
		if kernels, err := env.Run.Probe(ctx, spec.KernelsProbe.Argv, owner.TimeoutSizeProbe); err == nil {
			if running, err := env.Run.Probe(ctx, owner.UnameProbe().Argv, owner.TimeoutQuickDetect); err == nil {
				if spec.HasOldKernels(kernels, running) {
					cmd := spec.OldKernels
					out = append(out, engine.Target{
						ID:        cmd.ID,
						Category:  ID,
						Display:   "old kernels",
						Action:    action.NewOwnerCommand(cmd, env.Run),
						Recovery:  engine.RecoveryOwner,
						NeedsRoot: cmd.NeedsRoot,
						Guard:     guard,
						Risk:      engine.RiskHigh,
						DefaultOn: false,
						Evidence:  []string{"installed kernels differ from the running kernel"},
					})
				}
			}
		}
	}
	return out
}

func journaldItem(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil {
		return nil
	}
	out, err := env.Run.Probe(ctx, owner.JournaldDiskUsageProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return nil
	}
	size, known := parseJournaldUsage(out)
	cmd := journaldCommand(env)
	if !cmd.Valid() {
		return nil
	}
	return []engine.Target{{
		ID:        cmd.ID,
		Category:  ID,
		Display:   "systemd journal",
		SizeBytes: size,
		SizeKnown: known,
		Action:    action.NewOwnerCommand(cmd, env.Run),
		Recovery:  engine.RecoveryOwner,
		NeedsRoot: cmd.NeedsRoot,
		Risk:      engine.RiskLow,
		DefaultOn: true,
		Evidence:  []string{"journald owns its journal; vacuum is its own command"},
	}}
}

func journaldCommand(env *engine.Env) owner.OwnerCommand {
	if env != nil && env.Cfg != nil {
		if env.Cfg.Clean.JournaldMaxAge != "" && env.Cfg.Clean.JournaldMaxSize == "" {
			return owner.JournaldVacuumTime(env.Cfg.Clean.JournaldMaxAge)
		}
		if env.Cfg.Clean.JournaldMaxSize != "" {
			return owner.JournaldVacuumSize(env.Cfg.Clean.JournaldMaxSize)
		}
	}
	return owner.JournaldVacuumSize("500M")
}

func tmpfilesItems(env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil || !owner.Available("systemd-tmpfiles") {
		return nil
	}
	var out []engine.Target
	clean := owner.SystemdTmpfilesClean

	if size, known := measureRotatedLogs("/var/log"); known || dirExists("/var/log") {
		cmd := clean("system.rotated-logs")
		out = append(out, engine.Target{
			ID:        cmd.ID,
			Category:  ID,
			Display:   "rotated logs",
			Path:      "/var/log",
			SizeBytes: size,
			SizeKnown: known,
			Action:    action.NewOwnerCommand(cmd, env.Run),
			Recovery:  engine.RecoveryOwner,
			NeedsRoot: cmd.NeedsRoot,
			Risk:      engine.RiskMed,
			DefaultOn: false,
			Evidence:  []string{"systemd-tmpfiles applies the distro aging rules"},
		})
	}
	cmd := clean("system.tmp")
	out = append(out, engine.Target{
		ID:        cmd.ID,
		Category:  ID,
		Display:   "temporary files",
		Action:    action.NewOwnerCommand(cmd, env.Run),
		Recovery:  engine.RecoveryOwner,
		NeedsRoot: cmd.NeedsRoot,
		Risk:      engine.RiskLow,
		DefaultOn: false,
		Evidence:  []string{"systemd-tmpfiles cleans /tmp and /var/tmp per age rules"},
	})
	if dirExists("/var/crash") {
		cmd = clean("system.crash")
		out = append(out, engine.Target{
			ID:        cmd.ID,
			Category:  ID,
			Display:   "crash reports",
			Path:      "/var/crash",
			Action:    action.NewOwnerCommand(cmd, env.Run),
			Recovery:  engine.RecoveryOwner,
			NeedsRoot: cmd.NeedsRoot,
			Risk:      engine.RiskLow,
			DefaultOn: false,
			Evidence:  []string{"/var/crash holds crash reports"},
		})
	}
	return out
}

func coreDumpItems(env *engine.Env) []engine.Target {
	if env == nil {
		return nil
	}
	var out []engine.Target
	for _, path := range findUserCoreFiles(env.Home) {
		t, err := newTrashTarget(env, "system.core-dumps", "core dump", path, env.Home, engine.RiskLow, false)
		if err == nil {
			out = append(out, t)
		}
	}
	return out
}

// newTrashTarget builds a validated, identity-snapshotted trash target.
func newTrashTarget(env *engine.Env, id, display, path, root string, risk engine.Risk, defaultOn bool) (engine.Target, error) {
	protected := []string(nil)
	if env.Cfg != nil {
		protected = env.Cfg.Clean.ProtectedPaths
	}
	v := safe.NewValidator(env.UID(), protected...)
	if root != "" {
		v.Roots = []string{root}
		v.RequireRoot = true
	}
	if err := v.Validate(path); err != nil {
		return engine.Target{}, err
	}
	snap, err := safe.TakeSnapshot(path)
	if err != nil {
		return engine.Target{}, err
	}
	tr := trash.New(env.Paths.TrashDir, env.UID())
	return engine.Target{
		ID:        id,
		Category:  ID,
		Display:   display,
		Path:      path,
		Action:    action.NewTrash(path, tr, snap, v),
		Recovery:  engine.RecoveryTrash,
		Risk:      risk,
		DefaultOn: defaultOn,
		Evidence:  []string{"user-owned file"},
	}, nil
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// findUserCoreFiles returns top-level core files in home.
func findUserCoreFiles(home string) []string {
	if home == "" {
		return nil
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var out []string
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if name == "core" || strings.HasPrefix(name, "core.") {
			out = append(out, filepath.Join(home, name))
		}
	}
	return out
}

// Package system implements the `system` cleanup category (spec §8.1): package
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

func packageItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Adapter == nil {
		return nil
	}
	guard := lockGuard{probe: env.Adapter.LockGuard()}
	var out []engine.Target

	if cmd := env.Adapter.PackageCacheAction(); cmd.Valid() {
		out = append(out, engine.Target{
			ID:        cmd.ID,
			Category:  ID,
			Display:   "package cache",
			Path:      "/var/cache/apt/archives",
			Action:    action.NewOwnerCommand(cmd, env.Run),
			Recovery:  engine.RecoveryOwner,
			NeedsRoot: cmd.NeedsRoot,
			Guard:     guard,
			Risk:      engine.RiskLow,
			DefaultOn: true,
			Evidence:  []string{"apt owns /var/cache/apt/archives"},
		})
	}

	if env.Run != nil {
		if probe, err := env.Run.Probe(ctx, owner.AptOrphanProbe().Argv, owner.TimeoutSizeProbe); err == nil {
			if parseAptRemovals(probe) > 0 {
				if cmd := env.Adapter.OrphansAction(); cmd.Valid() {
					size, known := parseAptFreed(probe)
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
						Evidence:  []string{"apt-get -s autoremove lists removable packages"},
					})
				}
			}
		}
	}

	if env.Run != nil {
		if probe, err := env.Run.Probe(ctx, owner.DpkgListProbe().Argv, owner.TimeoutSizeProbe); err == nil {
			pkgs := parseResidualConfig(probe)
			allow := make(map[string]bool, len(pkgs))
			for _, p := range pkgs {
				allow[p] = true
			}
			// Defense in depth: the purge list must be a subset of the set we
			// parsed from dpkg output (I4 / plan 03 note).
			if len(pkgs) > 0 && owner.ValidatePackageSubset(pkgs, allow) == nil {
				if cmd, err := env.Adapter.ResidualConfigAction(pkgs); err == nil {
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
						Evidence:  []string{"dpkg -l reports ^rc packages"},
					})
				}
			}
		}
	}

	if oldKernelsPresent(ctx, env) {
		if cmd := env.Adapter.OldKernelsAction(); cmd.Valid() {
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
				Evidence:  []string{"installed linux-image packages differ from the running kernel"},
			})
		}
	}
	return out
}

func oldKernelsPresent(ctx context.Context, env *engine.Env) bool {
	if env == nil || env.Run == nil {
		return false
	}
	pkgs, err := env.Run.Probe(ctx, owner.DpkgKernelProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return false
	}
	running, err := env.Run.Probe(ctx, owner.UnameProbe().Argv, owner.TimeoutQuickDetect)
	if err != nil {
		return false
	}
	runningRel := strings.TrimSpace(running)
	for _, p := range parseKernelPackages(pkgs) {
		if kernelVersion(p) != runningRel {
			return true
		}
	}
	return false
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
		t, err := newTrashTarget(env, "system.core-dumps", "core dump", path, env.Home, engine.RiskLow, false, false)
		if err == nil {
			out = append(out, t)
		}
	}
	return out
}

// newTrashTarget builds a validated, identity-snapshotted trash target.
func newTrashTarget(env *engine.Env, id, display, path, root string, risk engine.Risk, defaultOn, permanent bool) (engine.Target, error) {
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
		Action:    action.NewTrash(path, tr, snap, v, permanent),
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

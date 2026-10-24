// Package desktoppkgs implements the `desktop-pkgs` cleanup category (spec
// §8.3): flatpak and snap. Owner commands mutate package state; the two
// user-data items trash a directory only after proving the owning application
// is not installed, and they skip when that cannot be proven (fail closed).
//
// Provenance: behavioral port of Mole's desktop/container cleanup shape,
// reimplemented clean-room. No Mole source was copied.
package desktoppkgs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// ID is the category identifier.
const ID = "desktop-pkgs"

// snapSnapsDir is the snap revision store. It is a variable so tests can point
// it at a fixture without needing root.
var snapSnapsDir = "/var/lib/snapd/snaps"

// Category enumerates flatpak and snap cleanup targets.
type Category struct{}

// New returns the desktop-pkgs category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "Desktop packages" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Flatpak and snap packages. Owner commands plus trashed user data for applications that are no longer installed."
}

// Discover enumerates desktop-package targets. Absence of flatpak/snap is
// normal: their items simply do not appear.
func (*Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	var out []engine.Target
	out = append(out, flatpakItems(ctx, env)...)
	out = append(out, snapItems(ctx, env)...)
	return out, nil
}

func flatpakItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil || !owner.Available("flatpak") {
		return nil
	}
	var out []engine.Target
	out = append(out, newOwnerTarget(env, owner.FlatpakUninstallUnused(), "unused flatpak runtimes", engine.RiskLow, true, nil))
	out = append(out, newOwnerTarget(env, owner.FlatpakRepair(), "flatpak repair", engine.RiskLow, false, nil))
	out = append(out, flatpakUserdataItems(ctx, env)...)
	return out
}

func flatpakUserdataItems(ctx context.Context, env *engine.Env) []engine.Target {
	root := filepath.Join(env.Home, ".var", "app")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	installed, known := flatpakInstalled(ctx, env.Run)
	if !known {
		// The app inventory is unknown, so no directory can be proven
		// orphaned. Skip rather than guess (invariant I5).
		return nil
	}
	var out []engine.Target
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		id := de.Name()
		if installed[id] {
			continue
		}
		path := filepath.Join(root, id)
		guard := appAbsentGuard{id: id, kind: "flatpak"}
		if t, err := newUserdataTarget(env, "flatpak.userdata", "flatpak app data "+id, path, root, guard); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func snapItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil || !owner.Available("snap") {
		return nil
	}
	var out []engine.Target
	out = append(out, snapRevisionItems(ctx, env)...)
	out = append(out, snapUserdataItems(ctx, env)...)
	return out
}

func snapRevisionItems(ctx context.Context, env *engine.Env) []engine.Target {
	out, err := env.Run.Probe(ctx, owner.SnapListAllProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return nil
	}
	var targets []engine.Target
	for _, rev := range parseDisabledRevisions(out) {
		cmd, err := owner.SnapRemoveRevision(rev.name, rev.rev)
		if err != nil {
			continue
		}
		targets = append(targets, engine.Target{
			// Each revision is its own target, so the target id must be
			// revision-specific even though the owner command's logical id is
			// the shared "snap.old-revisions" (spec §6.2: Target.ID is unique).
			ID:        cmd.ID + "." + rev.name + "_" + rev.rev,
			Category:  ID,
			Display:   fmt.Sprintf("snap revision %s_%s", rev.name, rev.rev),
			Path:      filepath.Join(snapSnapsDir, rev.name+"_"+rev.rev+".snap"),
			Action:    action.NewOwnerCommand(cmd, env.Run),
			Recovery:  engine.RecoveryOwner,
			NeedsRoot: true,
			Risk:      engine.RiskMed,
			DefaultOn: true,
			Evidence:  []string{"snap list --all reports a disabled revision"},
		})
	}
	return targets
}

func snapUserdataItems(ctx context.Context, env *engine.Env) []engine.Target {
	root := filepath.Join(env.Home, "snap")
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	installed, known := snapInstalled(ctx, env.Run)
	if !known {
		return nil
	}
	var out []engine.Target
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		name := de.Name()
		if installed[name] {
			continue
		}
		path := filepath.Join(root, name)
		guard := appAbsentGuard{id: name, kind: "snap"}
		if t, err := newUserdataTarget(env, "snap.userdata", "snap data "+name, path, root, guard); err == nil {
			out = append(out, t)
		}
	}
	return out
}

func newOwnerTarget(env *engine.Env, cmd owner.OwnerCommand, display string, risk engine.Risk, def bool, guard engine.Guard) engine.Target {
	return engine.Target{
		ID:        cmd.ID,
		Category:  ID,
		Display:   display,
		Action:    action.NewOwnerCommand(cmd, env.Run),
		Recovery:  engine.RecoveryOwner,
		NeedsRoot: cmd.NeedsRoot,
		Guard:     guard,
		Risk:      risk,
		DefaultOn: def,
	}
}

// newUserdataTarget builds a validated, identity-snapshotted trash target whose
// path is confined to root and owned by the invoking user.
func newUserdataTarget(env *engine.Env, id, display, path, root string, guard engine.Guard) (engine.Target, error) {
	protected := []string(nil)
	if env.Cfg != nil {
		protected = env.Cfg.Clean.ProtectedPaths
	}
	v := safe.NewValidator(env.UID(), protected...)
	v.Roots = []string{root}
	v.RequireRoot = true
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
		NeedsRoot: false,
		Guard:     guard,
		Risk:      engine.RiskMed,
		DefaultOn: false,
		Evidence:  []string{"user data directory with no installed application"},
	}, nil
}

// appAbsentGuard re-checks, at execution time, that the application owning a
// user-data directory is still absent. Unknown state and a now-installed app
// both stop the trash (fail closed).
type appAbsentGuard struct {
	id   string
	kind string
}

func (g appAbsentGuard) Check(ctx context.Context, env *engine.Env) error {
	var r *owner.Runner
	if env != nil {
		r = env.Run
	}
	switch g.kind {
	case "flatpak":
		installed, known := flatpakInstalled(ctx, r)
		if !known {
			return safe.UnknownGuard("cannot determine installed flatpak applications").Err()
		}
		if installed[g.id] {
			return safe.BusyGuard(fmt.Sprintf("flatpak %s is installed", g.id)).Err()
		}
	case "snap":
		installed, known := snapInstalled(ctx, r)
		if !known {
			return safe.UnknownGuard("cannot determine installed snaps").Err()
		}
		if installed[g.id] {
			return safe.BusyGuard(fmt.Sprintf("snap %s is installed", g.id)).Err()
		}
	}
	return nil
}

func flatpakInstalled(ctx context.Context, r *owner.Runner) (map[string]bool, bool) {
	if r == nil {
		return nil, false
	}
	out, err := r.Probe(ctx, owner.FlatpakListAppsProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return nil, false
	}
	return parseFlatpakApps(out), true
}

func snapInstalled(ctx context.Context, r *owner.Runner) (map[string]bool, bool) {
	if r == nil {
		return nil, false
	}
	out, err := r.Probe(ctx, owner.SnapListProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return nil, false
	}
	return parseSnapNames(out), true
}

// parseFlatpakApps reads `flatpak list --app --columns=application` output. An
// application id is a single reverse-DNS token with no whitespace.
func parseFlatpakApps(out string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.ContainsAny(line, " \t") || !strings.Contains(line, ".") {
			continue
		}
		set[line] = true
	}
	return set
}

// parseSnapNames reads `snap list` output, returning the installed snap names.
func parseSnapNames(out string) map[string]bool {
	set := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if strings.EqualFold(fields[0], "name") {
			continue
		}
		if !owner.ValidSnapName(fields[0]) {
			continue
		}
		set[fields[0]] = true
	}
	return set
}

// snapRevision is one disabled snap revision.
type snapRevision struct {
	name string
	rev  string
}

// parseDisabledRevisions reads `snap list --all` output and returns each row
// whose Notes column reports "disabled". Name and revision are validated so a
// malformed row can never reach the argv.
func parseDisabledRevisions(out string) []snapRevision {
	var revs []snapRevision
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		if !owner.ValidSnapName(fields[0]) || !owner.ValidSnapRevision(fields[2]) {
			continue
		}
		if !strings.Contains(strings.ToLower(line), "disabled") {
			continue
		}
		revs = append(revs, snapRevision{name: fields[0], rev: fields[2]})
	}
	return revs
}

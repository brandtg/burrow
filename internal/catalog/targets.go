// This file holds helpers shared by the catalog data packages (dev, user-cache,
// downloads): they build validated, identity-snapshotted trash targets, expose
// the per-user allow-list that backs invariant I3, and provide the process
// guard and owner-path resolver used by the toolchain items.
package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// TrashTargetOptions configures NewTrashTarget.
type TrashTargetOptions struct {
	ID         string
	Category   string
	Display    string
	Path       string
	Roots      []string
	Risk       engine.Risk
	Regen      engine.Regen
	Gated      bool
	DefaultOn  bool
	Evidence   []string
	Group      string
	GroupTitle string
}

// NewTrashTarget validates, snapshots, and builds a trash-contract target. It
// fails closed: a path that is absent or fails validation yields an error and
// no target. When Roots is empty the per-user allow-list from AllowedRoots is
// used, so a target can never point outside the invoking user's own trees.
func NewTrashTarget(env *engine.Env, o TrashTargetOptions) (engine.Target, error) {
	if env == nil {
		return engine.Target{}, errors.New("catalog: nil env")
	}
	var protected []string
	if env.Cfg != nil {
		protected = env.Cfg.Clean.ProtectedPaths
	}
	v := safe.NewValidator(env.UID(), protected...)
	roots := o.Roots
	if len(roots) == 0 {
		roots = AllowedRoots(env)
	}
	if len(roots) > 0 {
		v.Roots = roots
		v.RequireRoot = true
	}
	if err := v.Validate(o.Path); err != nil {
		return engine.Target{}, err
	}
	snap, err := safe.TakeSnapshot(o.Path)
	if err != nil {
		return engine.Target{}, err
	}
	tr := trash.New(env.Paths.TrashDir, env.UID())
	return engine.Target{
		ID:         o.ID,
		Category:   o.Category,
		Display:    o.Display,
		Path:       o.Path,
		Action:     action.NewTrash(o.Path, tr, snap, v, false),
		Recovery:   engine.RecoveryTrash,
		Risk:       o.Risk,
		Regen:      o.Regen,
		Gated:      o.Gated,
		DefaultOn:  o.DefaultOn,
		Evidence:   o.Evidence,
		Group:      o.Group,
		GroupTitle: o.GroupTitle,
	}, nil
}

// AllowedRoots returns the per-user directories a trash-contract target may
// live under. It is the allow-list that backs invariant I3 for user caches.
func AllowedRoots(env *engine.Env) []string {
	if env == nil {
		return nil
	}
	var roots []string
	for _, r := range []string{env.Home, env.XDGCache, env.XDGConfig, env.XDGData, env.XDGState, env.XDGRuntime} {
		if r != "" {
			roots = append(roots, r)
		}
	}
	return roots
}

// ProcessGuard fails closed when any named process is running (spec §7.5). A
// missing pgrep or an indeterminate probe is Unknown, which stops the action.
type ProcessGuard struct{ Names []string }

// Check implements engine.Guard.
func (g ProcessGuard) Check(ctx context.Context, env *engine.Env) error {
	if env == nil || env.Run == nil {
		return safe.UnknownGuard("no runner available to inspect processes").Err()
	}
	for _, name := range g.Names {
		running, err := processRunning(ctx, env, name)
		if err != nil {
			return safe.UnknownGuard(err.Error()).Err()
		}
		if running {
			return safe.BusyGuard(name + " is running").Err()
		}
	}
	return nil
}

// processRunning reports whether a process named name is running, using pgrep.
// Exit code 1 means "no match" and is not an error.
func processRunning(ctx context.Context, env *engine.Env, name string) (bool, error) {
	_, err := env.Run.Probe(ctx, []string{"pgrep", "-x", name}, owner.TimeoutQuickDetect)
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("cannot inspect process %s: %w", name, err)
}

// ResolveOwnerPath probes an owner command and returns the absolute path it
// reports on the first non-empty line. It fails closed on a relative or empty
// path: owner output is untrusted input (spec §7.3).
func ResolveOwnerPath(ctx context.Context, r *owner.Runner, argv []string) (string, error) {
	if r == nil {
		return "", errors.New("catalog: no runner available")
	}
	out, err := r.Probe(ctx, argv, owner.TimeoutQuickDetect)
	if err != nil {
		return "", err
	}
	line := firstLine(out)
	if line == "" {
		return "", errors.New("owner reported no path")
	}
	if !filepath.IsAbs(line) {
		return "", fmt.Errorf("owner reported a relative path: %q", line)
	}
	return filepath.Clean(line), nil
}

func firstLine(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}
	return ""
}

// DirExists reports whether path is an existing directory.
func DirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// FileExists reports whether path exists (of any type).
func FileExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// InHome reports whether path lies within the invoking user's home. Owner
// caches are per-user; a path reported outside home is treated as unsafe.
func InHome(env *engine.Env, path string) bool {
	return env != nil && env.Home != "" && safe.WithinRoot(path, env.Home)
}

// IsRecent reports whether path has been modified within the given window,
// using the path's own mtime (cheap; no tree walk). within <= 0 disables the
// check and reports recent=false. known is false when the path cannot be
// stat'd; callers should then not gate the item (nothing was proven).
func IsRecent(path string, within time.Duration) (recent, known bool) {
	if within <= 0 || path == "" {
		return false, true
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return false, false
	}
	return time.Since(fi.ModTime()) < within, true
}

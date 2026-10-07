package dev

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// gitTrackedGuard keeps a candidate that git tracks. Git state is treated as
// untrusted: if it cannot be proven that the path is untracked, the guard stops
// the action (fail closed). A path outside any work tree is safe to trash.
type gitTrackedGuard struct{ path string }

func (g gitTrackedGuard) Check(ctx context.Context, env *engine.Env) error {
	if env == nil || env.Run == nil {
		return safe.UnknownGuard("no runner available to inspect git").Err()
	}
	// Run read-only git probes regardless of the run's dry-run flag: the guard
	// must see real state even when the plan is only being previewed.
	r := *env.Run
	r.DryRun = false
	dir := filepath.Dir(g.path)

	res, err := r.Run(ctx, []string{"git", "-C", dir, "rev-parse", "--is-inside-work-tree"}, owner.TimeoutQuickDetect, false)
	if err != nil {
		if res != nil && res.ExitCode == 128 {
			return nil // not a git repository: safe to trash
		}
		return safe.UnknownGuard("cannot determine git state: " + err.Error()).Err()
	}
	if strings.TrimSpace(res.Stdout) != "true" {
		return nil
	}

	rootRes, err := r.Run(ctx, []string{"git", "-C", dir, "rev-parse", "--show-toplevel"}, owner.TimeoutQuickDetect, false)
	if err != nil {
		return safe.UnknownGuard("cannot resolve git root: " + err.Error()).Err()
	}
	root := strings.TrimSpace(rootRes.Stdout)
	if root == "" {
		return safe.UnknownGuard("git reported an empty work tree").Err()
	}
	rel, err := filepath.Rel(root, g.path)
	if err != nil {
		return safe.UnknownGuard("candidate is outside its git root").Err()
	}
	trackedRes, err := r.Run(ctx, []string{"git", "-C", root, "ls-files", "--", rel}, owner.TimeoutQuickDetect, false)
	if err != nil {
		return safe.UnknownGuard("cannot list tracked files: " + err.Error()).Err()
	}
	if strings.TrimSpace(trackedRes.Stdout) != "" {
		return safe.BusyGuard("candidate is git-tracked").Err()
	}
	return nil
}

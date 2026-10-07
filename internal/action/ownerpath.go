package action

import (
	"context"
	"fmt"
	"strings"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// PathResolver re-resolves an owner-reported path immediately before an action.
// Implementations must be read-only and should use Runner.Probe so a dry-run
// still computes the same plan.
type PathResolver func(ctx context.Context, r *owner.Runner) (string, error)

// OwnerPathAction is an OwnerCommandAction whose target path is reported by the
// owner itself (for example `npm config get cache`). Because the path is
// untrusted input, it is re-resolved immediately before the command runs and
// the action refuses when the path changed or escaped its allowed parent
// (spec §7.3). The command argv itself stays an exact catalog entry (I4).
type OwnerPathAction struct {
	Command      owner.OwnerCommand
	Runner       *owner.Runner
	Resolve      PathResolver
	Expected     string
	AllowedRoots []string
}

// Describe renders a human description.
func (a *OwnerPathAction) Describe() string {
	return "run " + strings.Join(owner.Plan(a.Command.Argv, a.Command.NeedsRoot), " ")
}

// Recovery declares the owner contract.
func (a *OwnerPathAction) Recovery() engine.Recovery { return engine.RecoveryOwner }

// PlannedArgv returns the argv the review screen will show.
func (a *OwnerPathAction) PlannedArgv() []string {
	return owner.Plan(a.Command.Argv, a.Command.NeedsRoot)
}

// Run re-resolves the owner path, then delegates to the exact owner command.
func (a *OwnerPathAction) Run(ctx context.Context, dryRun bool) (engine.Result, error) {
	if a.Resolve != nil {
		got, err := a.Resolve(ctx, a.Runner)
		if err != nil {
			return skip(err.Error()), safe.GuardStop("%s", err.Error())
		}
		if got != a.Expected {
			reason := fmt.Sprintf("owner path changed: %s -> %s", a.Expected, got)
			return skip(reason), safe.GuardStop("%s", reason)
		}
		if len(a.AllowedRoots) > 0 && !safe.WithinAnyRoot(got, a.AllowedRoots) {
			reason := fmt.Sprintf("owner path %s is outside the allowed parent", got)
			return skip(reason), safe.GuardStop("%s", reason)
		}
	}
	return (&OwnerCommandAction{Command: a.Command, Runner: a.Runner}).Run(ctx, dryRun)
}

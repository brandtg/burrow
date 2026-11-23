// Package action holds the two Action implementations: TrashAction (move to the
// freedesktop Trash) and OwnerCommandAction (run an allow-listed owner
// command). Both re-validate at execution time and are
// idempotent. Trash is the only recoverable delete path; emptying the Trash is
// the one permanent mutation, and it must never be journaled as a recoverable
// action.
//
// Provenance: the recovery-contract split (trash vs owner) and the review-then-
// act discipline are behavioral ports of Mole's cleanup UX, reimplemented
// clean-room. No Mole source was copied.
package action

import (
	"context"
	"errors"
	"os"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// OwnerCommandAction runs an exact, allow-listed argv (invariant I4).
type OwnerCommandAction struct {
	Command owner.OwnerCommand
	Runner  *owner.Runner
}

// NewOwnerCommand builds an owner-command action.
func NewOwnerCommand(cmd owner.OwnerCommand, r *owner.Runner) *OwnerCommandAction {
	return &OwnerCommandAction{Command: cmd, Runner: r}
}

// Recovery declares the owner contract.
func (a *OwnerCommandAction) Recovery() engine.Recovery { return engine.RecoveryOwner }

// PlannedArgv returns the argv the review screen will show.
func (a *OwnerCommandAction) PlannedArgv() []string {
	return owner.Plan(a.Command.Argv, a.Command.NeedsRoot)
}

// Run executes the command under its bounded timeout. dryRun computes the same
// argv without starting a process. The error carries the outcome; the result
// carries the executed argv and timeout flag.
func (a *OwnerCommandAction) Run(ctx context.Context, dryRun bool) (engine.Result, error) {
	r := a.Runner
	if r == nil {
		r = owner.New(dryRun)
	} else {
		cp := *r
		cp.DryRun = dryRun
		r = &cp
	}
	res, err := r.Run(ctx, a.Command.Argv, a.Command.Timeout, a.Command.NeedsRoot)
	if res == nil {
		return engine.Result{}, err
	}
	return engine.Result{Argv: res.Argv, TimedOut: res.TimedOut}, err
}

// TrashAction moves a user-owned path to the Trash (recoverable).
type TrashAction struct {
	Path      string
	Trash     *trash.Trash
	Snapshot  *safe.Snapshot
	Validator *safe.Validator
	Size      int64
}

// NewTrash builds a trash action. snapshot may be nil only for an
// already-absent path; a present path with no snapshot is refused at Run time
// (fail closed).
func NewTrash(path string, t *trash.Trash, snapshot *safe.Snapshot, v *safe.Validator) *TrashAction {
	return &TrashAction{Path: path, Trash: t, Snapshot: snapshot, Validator: v}
}

// Recovery declares the trash contract.
func (a *TrashAction) Recovery() engine.Recovery { return engine.RecoveryTrash }

// Run re-validates the path and identity, then moves the item. A path that is
// already absent is treated as done (idempotent). The context is honored before
// starting and through the cross-device copy fallback. Every refusal is a
// guard-stop error carrying its own reason.
func (a *TrashAction) Run(ctx context.Context, dryRun bool) (engine.Result, error) {
	if err := ctx.Err(); err != nil {
		return engine.Result{}, err
	}
	if a.Validator != nil {
		if err := a.Validator.Validate(a.Path); err != nil {
			return engine.Result{}, safe.GuardStop("%s", err.Error())
		}
	}
	if _, err := os.Lstat(a.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return engine.Result{}, nil
		}
		return engine.Result{}, safe.GuardStop("%s", err.Error())
	}
	if a.Snapshot == nil {
		// Fail closed: without an identity snapshot the TOCTOU check cannot run,
		// so a present path must not be mutated (invariant I5).
		return engine.Result{}, safe.GuardStop("%s", "no identity snapshot")
	}
	if ok, err := a.Snapshot.Matches(); err != nil || !ok {
		return engine.Result{}, safe.GuardStop("%s", "path identity changed since snapshot")
	}
	if dryRun {
		return engine.Result{Bytes: a.Size}, nil
	}
	if a.Trash == nil {
		return engine.Result{}, safe.GuardStop("%s", "trash is unavailable")
	}
	entry, err := a.Trash.PutContext(ctx, a.Path)
	if err != nil {
		if entry.Name != "" {
			// The item is fully in the trash and restorable; the source-removal
			// failure is reported but does not fail the item.
			return engine.Result{Bytes: a.Size}, nil
		}
		return engine.Result{}, err
	}
	return engine.Result{Bytes: a.Size}, nil
}

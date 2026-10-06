// Package action holds the two Action implementations from spec §6.2:
// TrashAction (move to the freedesktop Trash, or an explicit permanent delete)
// and OwnerCommandAction (run an allow-listed owner command). Both re-validate
// at execution time and are idempotent.
//
// Provenance: the recovery-contract split (trash vs owner) and the review-then-
// act discipline are behavioral ports of Mole's cleanup UX, reimplemented
// clean-room. No Mole source was copied.
package action

import (
	"context"
	"errors"
	"os"
	"strings"

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

// Describe renders a human description.
func (a *OwnerCommandAction) Describe() string {
	return "run " + strings.Join(owner.Plan(a.Command.Argv, a.Command.NeedsRoot), " ")
}

// Recovery declares the owner contract.
func (a *OwnerCommandAction) Recovery() engine.Recovery { return engine.RecoveryOwner }

// PlannedArgv returns the argv the review screen will show.
func (a *OwnerCommandAction) PlannedArgv() []string {
	return owner.Plan(a.Command.Argv, a.Command.NeedsRoot)
}

// Run executes the command under its bounded timeout. dryRun computes the same
// argv without starting a process.
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
		return engine.Result{Status: engine.StatusFailed, Reason: errString(err)}, err
	}
	out := engine.Result{Argv: res.Argv, TimedOut: res.TimedOut}
	switch {
	case dryRun:
		out.Status = engine.StatusDryRun
	case err == nil:
		out.Status = engine.StatusOK
	case res.TimedOut:
		out.Status = engine.StatusSkipped
		out.Reason = "timed out"
	default:
		out.Status = engine.StatusFailed
		if s := strings.TrimSpace(res.Stderr); s != "" {
			out.Reason = s
		} else {
			out.Reason = errString(err)
		}
	}
	return out, err
}

// TrashAction moves a user-owned path to the Trash (recoverable), or deletes it
// permanently only when Permanent is explicitly set.
type TrashAction struct {
	Path      string
	Trash     *trash.Trash
	Snapshot  *safe.Snapshot
	Validator *safe.Validator
	Permanent bool
	Size      int64
}

// NewTrash builds a trash action. snapshot may be nil when identity cannot be
// captured; the action still re-validates the path.
func NewTrash(path string, t *trash.Trash, snapshot *safe.Snapshot, v *safe.Validator, permanent bool) *TrashAction {
	return &TrashAction{Path: path, Trash: t, Snapshot: snapshot, Validator: v, Permanent: permanent}
}

// Describe renders a human description.
func (a *TrashAction) Describe() string {
	if a.Permanent {
		return "permanently delete " + a.Path
	}
	return "trash " + a.Path
}

// Recovery declares the trash contract.
func (a *TrashAction) Recovery() engine.Recovery { return engine.RecoveryTrash }

// SetPermanent switches this action from Trash to an explicit permanent delete.
// It is only called when --permanent is set; the default remains Trash.
func (a *TrashAction) SetPermanent(p bool) { a.Permanent = p }

// Run re-validates the path and identity, then moves the item. A path that is
// already absent is treated as done (idempotent).
func (a *TrashAction) Run(ctx context.Context, dryRun bool) (engine.Result, error) {
	if a.Validator != nil {
		if err := a.Validator.Validate(a.Path); err != nil {
			return skip(err.Error()), safe.GuardStop("%s", err.Error())
		}
	}
	if _, err := os.Lstat(a.Path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return engine.Result{Status: engine.StatusOK, Reason: "already absent"}, nil
		}
		return skip(err.Error()), safe.GuardStop("%s", err.Error())
	}
	if a.Snapshot != nil {
		ok, err := a.Snapshot.Matches()
		if err != nil || !ok {
			reason := "path identity changed since snapshot"
			return skip(reason), safe.GuardStop("%s", reason)
		}
	}
	if dryRun {
		return engine.Result{Status: engine.StatusDryRun, Bytes: a.Size}, nil
	}
	if a.Permanent {
		if err := trash.Remove(a.Path); err != nil {
			return engine.Result{Status: engine.StatusFailed, Reason: err.Error()}, err
		}
		return engine.Result{Status: engine.StatusOK, Bytes: a.Size}, nil
	}
	if a.Trash == nil {
		return skip("trash is unavailable"), safe.GuardStop("%s", "trash is unavailable")
	}
	entry, err := a.Trash.Put(a.Path)
	if err != nil {
		if entry.Name != "" {
			return engine.Result{Status: engine.StatusOK, Bytes: a.Size, Reason: err.Error()}, nil
		}
		return engine.Result{Status: engine.StatusFailed, Reason: err.Error()}, err
	}
	return engine.Result{Status: engine.StatusOK, Bytes: a.Size}, nil
}

func skip(reason string) engine.Result {
	return engine.Result{Status: engine.StatusSkipped, Reason: reason}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

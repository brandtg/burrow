package action

import (
	"context"

	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// EmptyTrashAction empties the user's Trash. It is irreversible, so it declares
// the delete contract (neither recoverable nor re-downloadable) and is only
// ever reached through an explicit opt-in (user-cache.trash is default OFF and
// high risk).
type EmptyTrashAction struct {
	Trash *trash.Trash
	Path  string
	Size  int64
}

// Recovery declares the delete contract: the items are gone, not recoverable.
func (a *EmptyTrashAction) Recovery() engine.Recovery { return engine.RecoveryDelete }

// Run empties the trash. dryRun computes the same plan without mutating.
func (a *EmptyTrashAction) Run(_ context.Context, dryRun bool) (engine.Result, error) {
	if dryRun {
		return engine.Result{Bytes: a.Size}, nil
	}
	if a.Trash == nil {
		return engine.Result{}, safe.GuardStop("%s", "trash is unavailable")
	}
	if err := a.Trash.Empty(); err != nil {
		return engine.Result{}, err
	}
	return engine.Result{Bytes: a.Size}, nil
}

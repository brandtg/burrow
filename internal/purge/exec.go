package purge

import (
	"errors"
	"path/filepath"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// Executor moves purgeable artifacts to the Trash with validation, identity
// re-checking, and journaling. It is the only purge path that mutates.
//
// Provenance: recoverable by default; there is no permanent-delete primitive.
// Artifacts are rebuildable, but a wrong guess costs disk space, not data, so
// they go through the same freedesktop Trash contract as every other
// hand-delete in burrow (independent implementation; no Mole source copied).
type Executor struct {
	Validator *safe.Validator
	Trash     *trash.Trash
	Journal   *journal.Writer
	Command   string

	runID string
}

// RunID lazily starts a journal run frame and returns its id. A nil journal
// yields an empty run id.
func (e *Executor) RunID() (string, error) {
	if e.runID != "" {
		return e.runID, nil
	}
	if e.Journal == nil {
		return "", nil
	}
	id, err := e.Journal.StartRun(e.command())
	if err != nil {
		return "", err
	}
	e.runID = id
	return id, nil
}

// EndRun writes the run_end frame when a run was started.
func (e *Executor) EndRun(freed int64) error {
	if e.runID == "" || e.Journal == nil {
		return nil
	}
	return e.Journal.EndRun(e.runID, e.command(), freed)
}

// Delete moves a to the Trash. dryRun records the plan without mutating and
// returns zero bytes. The artifact must carry the snapshot taken during
// discovery; without one it is a guard-stop (fail closed).
func (e *Executor) Delete(a *Artifact, dryRun bool) (int64, error) {
	if a == nil {
		return 0, errors.New("purge: nil artifact")
	}
	if dryRun {
		e.record(a, a.Size, journal.StatusDryRun, "")
		return 0, nil
	}
	if a.Snapshot == nil {
		err := safe.GuardStop("no identity snapshot for %s", a.Path)
		e.record(a, 0, journal.StatusSkipped, err.Error())
		return 0, err
	}
	if filepath.Clean(a.Path) != a.Snapshot.Path {
		err := safe.GuardStop("identity snapshot does not match %s", a.Path)
		e.record(a, 0, journal.StatusSkipped, err.Error())
		return 0, err
	}
	if e.Validator != nil {
		if err := e.Validator.Validate(a.Path); err != nil {
			e.record(a, 0, journal.StatusSkipped, err.Error())
			return 0, err
		}
	}
	ok, err := a.Snapshot.Matches()
	if err != nil {
		err = safe.GuardStop("cannot re-check identity for %s: %v", a.Path, err)
		e.record(a, 0, journal.StatusSkipped, err.Error())
		return 0, err
	}
	if !ok {
		err := safe.GuardStop("path identity changed since snapshot: %s", a.Path)
		e.record(a, 0, journal.StatusSkipped, err.Error())
		return 0, err
	}
	if e.Trash == nil {
		err := safe.GuardStop("trash is unavailable for %s", a.Path)
		e.record(a, 0, journal.StatusSkipped, err.Error())
		return 0, err
	}
	entry, err := e.Trash.Put(a.Path)
	if err != nil {
		if entry.Name != "" {
			// The item is fully in the trash and restorable even though the
			// source could not be unlinked (cross-device copy).
			e.record(a, a.Size, journal.StatusOK, err.Error())
			return a.Size, nil
		}
		e.record(a, a.Size, journal.StatusFailed, err.Error())
		return 0, err
	}
	e.record(a, a.Size, journal.StatusOK, "")
	return a.Size, nil
}

func (e *Executor) command() string {
	if e.Command != "" {
		return e.Command
	}
	return "purge"
}

func (e *Executor) record(a *Artifact, size int64, status, reason string) {
	if e.Journal == nil {
		return
	}
	runID, err := e.RunID()
	if err != nil {
		return
	}
	_ = e.Journal.Append(journal.Record{
		RunID:    runID,
		Command:  e.command(),
		Item:     a.Type,
		Action:   journal.ActionTrash,
		Recovery: journal.RecoveryTrash,
		Path:     a.Path,
		Status:   status,
		Bytes:    size,
		Reason:   reason,
	})
}

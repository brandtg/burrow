package purge

import (
	"errors"

	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
)

// Executor performs permanent artifact deletion with validation, identity
// re-checking, and journaling. It is the only purge path that mutates, and it
// deletes only through safe.RemoveTree.
type Executor struct {
	Validator *safe.Validator
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

// Delete permanently removes a. dryRun records the plan without mutating and
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
	if err := safe.RemoveTree(a.Path, e.Validator, a.Snapshot); err != nil {
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
		Action:   journal.ActionPurge,
		Recovery: journal.RecoveryOwner,
		Path:     a.Path,
		Status:   status,
		Bytes:    size,
		Reason:   reason,
	})
}

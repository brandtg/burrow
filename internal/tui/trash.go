package tui

import (
	"github.com/brandtg/burrow/internal/journal"
	"github.com/brandtg/burrow/internal/safe"
	"github.com/brandtg/burrow/internal/trash"
)

// TrashExec performs analyze deletions: validate, re-check the identity
// snapshot taken at selection time, move to Trash, and journal the result.
//
// It never falls back to a permanent delete. A failed or changed identity is a
// guard-stop (exit code 75), not a wrong delete (spec §7.3, invariant I5).
type TrashExec struct {
	Trash     *trash.Trash
	Validator *safe.Validator
	Journal   *journal.Writer
	Command   string

	runID string
}

// RunID returns the current run id, starting a run frame lazily on first use.
// A nil journal yields an empty run id.
func (e *TrashExec) RunID() (string, error) {
	if e.runID != "" {
		return e.runID, nil
	}
	if e.Journal == nil {
		return "", nil
	}
	command := e.Command
	if command == "" {
		command = "analyze"
	}
	id, err := e.Journal.StartRun(command)
	if err != nil {
		return "", err
	}
	e.runID = id
	return id, nil
}

// EndRun writes the run_end frame when a run was started.
func (e *TrashExec) EndRun(freed int64) error {
	if e.runID == "" || e.Journal == nil {
		return nil
	}
	command := e.Command
	if command == "" {
		command = "analyze"
	}
	return e.Journal.EndRun(e.runID, command, freed)
}

// MoveToTrash moves snap.Path into the trash after proving the path still matches the
// snapshot. size is the on-disk size used for the journal record; item is the
// stable journal label (for analyze, the path itself).
func (e *TrashExec) MoveToTrash(snap *safe.Snapshot, size int64, item string) error {
	if snap == nil {
		return safe.GuardStop("no identity snapshot for %s", item)
	}
	if e.Validator != nil {
		if err := e.Validator.Validate(snap.Path); err != nil {
			e.record(snap.Path, item, size, journal.StatusSkipped, err.Error())
			return err
		}
	}
	ok, err := snap.Matches()
	if err != nil {
		e.record(snap.Path, item, size, journal.StatusSkipped, err.Error())
		return safe.GuardStop("%v", err)
	}
	if !ok {
		reason := "path identity changed since snapshot"
		e.record(snap.Path, item, size, journal.StatusSkipped, reason)
		return safe.GuardStop("%s: %s", reason, snap.Path)
	}
	entry, err := e.Trash.Put(snap.Path)
	if err != nil {
		e.record(snap.Path, item, size, journal.StatusFailed, err.Error())
		return err
	}
	e.record(entry.Path, item, size, journal.StatusOK, "")
	return nil
}

func (e *TrashExec) record(path, item string, size int64, status, reason string) {
	if e.Journal == nil {
		return
	}
	runID, err := e.RunID()
	if err != nil {
		return
	}
	command := e.Command
	if command == "" {
		command = "analyze"
	}
	_ = e.Journal.Append(journal.Record{
		RunID:   runID,
		Command: command,
		Item:    item,
		Action:  journal.ActionTrash,
		Path:    path,
		Status:  status,
		Bytes:   size,
		Reason:  reason,
	})
}

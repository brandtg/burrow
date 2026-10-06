package safe

import (
	"os"
	"path/filepath"
)

// RemoveTree permanently deletes path and its subtree. It is the single
// permanent-delete primitive in burrow (invariant I3): it refuses unless the
// path passes the caller's Validator and still matches the identity snapshot
// taken during planning. A nil snapshot is a guard-stop (fail closed).
//
// This is the one function in package safe that mutates the filesystem. It is
// deliberately confined here so the static destructive-sink gate can prove no
// other package deletes paths directly. It never follows a symlink root:
// Validator.Validate rejects a symlinked target before the sink is reached.
//
// Provenance: the snapshot-before-delete discipline is an independent
// reimplementation of Mole's TOCTOU guard (Mole is GPL-3.0; no source copied).
func RemoveTree(path string, v *Validator, snap *Snapshot) error {
	if v != nil {
		if err := v.Validate(path); err != nil {
			return err
		}
	}
	if snap == nil {
		return GuardStop("no identity snapshot for %s", path)
	}
	if filepath.Clean(path) != snap.Path {
		return GuardStop("identity snapshot does not match %s", path)
	}
	ok, err := snap.Matches()
	if err != nil {
		return GuardStop("cannot re-check identity for %s: %v", path, err)
	}
	if !ok {
		return GuardStop("path identity changed since snapshot: %s", path)
	}
	return os.RemoveAll(path)
}

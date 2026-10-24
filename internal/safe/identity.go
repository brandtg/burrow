package safe

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Snapshot records the identity of a path and its parent immediately before a
// mutation. Re-stat'ing and comparing immediately before the sink defeats a
// path swap between discovery and execution (TOCTOU), spec §7.3.
//
// Provenance: independent reimplementation of Mole's
// _mole_snapshot_path_identity / _mole_path_matches_identity.
type Snapshot struct {
	Path      string
	ParentDev uint64
	ParentIno uint64
	TargetDev uint64
	TargetIno uint64
	Size      int64
	Mode      os.FileMode
}

func statIdentity(path string) (dev, ino uint64, size int64, mode os.FileMode, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, 0, 0, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, 0, fmt.Errorf("cannot read stat for %s", path)
	}
	return st.Dev, st.Ino, fi.Size(), fi.Mode(), nil
}

// TakeSnapshot captures the target and its parent. It fails closed: if either
// cannot be stat'ed the snapshot is not returned.
func TakeSnapshot(path string) (*Snapshot, error) {
	clean := filepath.Clean(path)
	dev, ino, size, mode, err := statIdentity(clean)
	if err != nil {
		return nil, err
	}
	pdev, pino, _, _, err := statIdentity(filepath.Dir(clean))
	if err != nil {
		return nil, err
	}
	return &Snapshot{
		Path:      clean,
		ParentDev: pdev,
		ParentIno: pino,
		TargetDev: dev,
		TargetIno: ino,
		Size:      size,
		Mode:      mode,
	}, nil
}

// Matches re-stats the target and parent and reports whether the identity is
// unchanged. Any error is reported as a non-match so callers fail closed.
func (s *Snapshot) Matches() (bool, error) {
	dev, ino, _, _, err := statIdentity(s.Path)
	if err != nil {
		return false, err
	}
	pdev, pino, _, _, err := statIdentity(filepath.Dir(s.Path))
	if err != nil {
		return false, err
	}
	if dev != s.TargetDev || ino != s.TargetIno || pdev != s.ParentDev || pino != s.ParentIno {
		return false, &Violation{Path: s.Path, Reason: "path identity changed since snapshot"}
	}
	return true, nil
}

// GuardStopError is returned when a guard or identity check refuses an action.
// It maps to exit code 75.
type GuardStopError struct {
	Reason string
}

func (e *GuardStopError) Error() string { return e.Reason }

// GuardStop builds a guard-stop error.
func GuardStop(format string, args ...any) *GuardStopError {
	return &GuardStopError{Reason: fmt.Sprintf(format, args...)}
}

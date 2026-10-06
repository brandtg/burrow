package safe

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// MutableAncestor walks from the parent of path up to the filesystem root and
// returns the first directory that a non-root user could rename or replace:
// one owned by a non-root uid and user-writable, or one with a group/other
// write bit. This backs invariant I7 (a privileged delete must not traverse a
// user-mutable ancestor).
//
// It fails closed: if any ancestor cannot be inspected, it returns an error
// rather than reporting "no mutable ancestor", because an uninspectable
// ancestor is not provably safe. Callers must treat the error as an unknown
// guard-stop. Symlinks are followed (os.Stat) because that is the path a
// privileged delete would traverse.
//
// Provenance: independent reimplementation of Mole's
// _mole_privileged_path_has_mutable_ancestor.
func MutableAncestor(path string) (string, bool, error) {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		fi, err := os.Stat(dir)
		if err != nil {
			return "", false, fmt.Errorf("cannot inspect ancestor %s: %w", dir, err)
		}
		if fi.IsDir() {
			st, ok := fi.Sys().(*syscall.Stat_t)
			if !ok {
				return "", false, fmt.Errorf("cannot read stat for ancestor %s", dir)
			}
			if isMutableDir(st) {
				return dir, true, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false, nil
		}
		dir = parent
	}
}

func isMutableDir(st *syscall.Stat_t) bool {
	mode := st.Mode
	// group- or other-writable, or owned by a non-root uid and user-writable.
	if mode&0o022 != 0 {
		return true
	}
	return st.Uid != 0 && mode&0o200 != 0
}

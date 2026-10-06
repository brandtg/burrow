package safe

import (
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
// Provenance: independent reimplementation of Mole's
// _mole_privileged_path_has_mutable_ancestor.
func MutableAncestor(path string) (string, bool) {
	dir := filepath.Dir(filepath.Clean(path))
	for {
		if fi, err := os.Lstat(dir); err == nil && fi.IsDir() {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				if isMutableDir(st) {
					return dir, true
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
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

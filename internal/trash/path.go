package trash

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// devOf returns the device id of a stat result.
func devOf(fi os.FileInfo) (uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}

// devOfPath stats path and returns its device id.
func devOfPath(path string) (uint64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, err
	}
	dev, ok := devOf(fi)
	if !ok {
		return 0, fmt.Errorf("trash: cannot read device for %s", path)
	}
	return dev, nil
}

// mountTopDir returns the mount point that contains path. It walks up until the
// parent device differs from the current directory's device.
func mountTopDir(path string, dev uint64) string {
	dir := filepath.Clean(path)
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir
		}
		pdev, err := devOfPath(parent)
		if err != nil || pdev != dev {
			return dir
		}
		dir = parent
	}
}

// hasStickyBit reports whether path is a directory with the sticky bit set and
// is not a symlink, as required for a shared $topdir/.Trash directory.
func hasStickyBit(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return st.Mode&syscall.S_ISVTX != 0
}

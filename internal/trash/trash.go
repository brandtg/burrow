// Package trash implements the freedesktop.org Trash Specification natively
// (no third-party dependency): Put, List, Restore, and Empty.
//
// Fail-closed: if a move cannot complete, Put returns an error and leaves the
// source in place. It never silently downgrades to a permanent unlink.
//
// Provenance: independent implementation of the freedesktop Trash Spec
// (https://specifications.freedesktop.org/trash-spec/). The fail-closed move
// mirrors Mole's trash discipline behaviourally; no Mole source was copied.
package trash

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Entry is one item in the trash.
type Entry struct {
	Name         string // entry name inside files/ (and the .trashinfo basename)
	InfoPath     string // absolute path to the .trashinfo file
	Path         string // decoded original absolute path
	DeletionDate time.Time
}

// errSourceNotRemoved is returned by move when the cross-device copy reached
// the trash but the source could not be removed. The trash entry is valid and
// restorable; the item may also still exist (partially) at the source.
var errSourceNotRemoved = errors.New("trash: copied to trash but source could not be removed")

// Trash is a handle to a user's trash. The home trash is the primary location;
// items on other filesystems are stored in per-mount trash directories.
type Trash struct {
	home string
	uid  int
}

// New returns a Trash rooted at home (the Trash directory itself, e.g.
// $XDG_DATA_HOME/Trash).
func New(home string, uid int) *Trash {
	return &Trash{home: home, uid: uid}
}

// Home returns the home trash root.
func (t *Trash) Home() string { return t.home }

// dirFor returns the trash directory that should hold path. The home trash is
// used when path is on the same filesystem; otherwise a per-mount trash is
// selected (or created) under the mount's top directory.
func (t *Trash) dirFor(path string) (string, error) {
	dev, err := devOfPath(path)
	if err != nil {
		return "", err
	}
	homeDev, err := devOfPath(t.home)
	if err != nil || homeDev == dev {
		if err := os.MkdirAll(t.home, 0o700); err != nil {
			return "", fmt.Errorf("trash: cannot create home trash: %w", err)
		}
		return t.home, nil
	}
	top := mountTopDir(path, dev)
	if top == "/" {
		// The root filesystem should have a home trash on it; if it does not,
		// refuse rather than scattering trash at the filesystem root.
		return "", fmt.Errorf("trash: no home trash on device for %s", path)
	}
	shared := filepath.Join(top, ".Trash")
	if hasStickyBit(shared) {
		dir := filepath.Join(shared, strconv.Itoa(t.uid))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return "", fmt.Errorf("trash: cannot create %s: %w", dir, err)
		}
		return dir, nil
	}
	dir := filepath.Join(top, fmt.Sprintf(".Trash-%d", t.uid))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("trash: cannot create %s: %w", dir, err)
	}
	return dir, nil
}

// Put moves path into the trash and returns its entry. The original path is
// recorded percent-encoded in the .trashinfo file.
func (t *Trash) Put(path string) (Entry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Entry{}, err
	}
	abs = filepath.Clean(abs)
	fi, err := os.Lstat(abs)
	if err != nil {
		return Entry{}, err
	}
	dir, err := t.dirFor(abs)
	if err != nil {
		return Entry{}, err
	}
	filesDir := filepath.Join(dir, "files")
	infoDir := filepath.Join(dir, "info")
	if err := os.MkdirAll(filesDir, 0o700); err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(infoDir, 0o700); err != nil {
		return Entry{}, err
	}

	name := uniqueName(filesDir, infoDir, filepath.Base(abs))
	infoPath := filepath.Join(infoDir, name+".trashinfo")
	when := time.Now()

	if err := os.WriteFile(infoPath, encodeInfo(abs, when), 0o600); err != nil {
		return Entry{}, fmt.Errorf("trash: cannot write info file: %w", err)
	}

	dest := filepath.Join(filesDir, name)
	if err := move(abs, dest, fi); err != nil {
		if errors.Is(err, errSourceNotRemoved) {
			// The item is fully in the trash; keep the .trashinfo so the entry
			// stays consistent and restorable. The source removal failed, so
			// the item may exist in both places. Return the valid Entry
			// alongside the error so callers can still recover it.
			return Entry{Name: name, InfoPath: infoPath, Path: abs, DeletionDate: when}, fmt.Errorf("trash: %s: %w", abs, err)
		}
		// The move did not place the item; roll back the info file so the
		// trash does not contain a dangling entry. The source is untouched.
		_ = os.Remove(infoPath)
		return Entry{}, fmt.Errorf("trash: cannot move %s to trash: %w", abs, err)
	}
	return Entry{Name: name, InfoPath: infoPath, Path: abs, DeletionDate: when}, nil
}

// List returns the entries in the home trash. Entries whose files/ counterpart
// is missing are skipped.
func (t *Trash) List() ([]Entry, error) {
	infoDir := filepath.Join(t.home, "info")
	filesDir := filepath.Join(t.home, "files")
	dirents, err := os.ReadDir(infoDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []Entry
	for _, de := range dirents {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".trashinfo") {
			continue
		}
		name := strings.TrimSuffix(de.Name(), ".trashinfo")
		if _, err := os.Lstat(filepath.Join(filesDir, name)); err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(infoDir, de.Name()))
		if err != nil {
			continue
		}
		info, err := parseInfo(data)
		if err != nil {
			continue
		}
		out = append(out, Entry{
			Name:         name,
			InfoPath:     filepath.Join(infoDir, de.Name()),
			Path:         info.Path,
			DeletionDate: info.DeletionDate,
		})
	}
	return out, nil
}

// Restore moves an entry back to its original path. It refuses if the target
// already exists (fail closed) or the entry is no longer present.
func (t *Trash) Restore(e Entry) error {
	filesPath := filepath.Join(t.home, "files", e.Name)
	fi, err := os.Lstat(filesPath)
	if err != nil {
		return fmt.Errorf("item is not present in the trash: %w", err)
	}
	if _, err := os.Lstat(e.Path); err == nil {
		return fmt.Errorf("restore target already exists: %s", e.Path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.Path), 0o700); err != nil {
		return err
	}
	if err := move(filesPath, e.Path, fi); err != nil {
		return err
	}
	_ = os.Remove(e.InfoPath)
	return nil
}

// Empty removes every item from the home trash. This is irreversible and is
// only reached through an explicit opt-in.
func (t *Trash) Empty() error {
	filesDir := filepath.Join(t.home, "files")
	infoDir := filepath.Join(t.home, "info")
	if dirents, err := os.ReadDir(filesDir); err == nil {
		for _, de := range dirents {
			if err := os.RemoveAll(filepath.Join(filesDir, de.Name())); err != nil {
				return err
			}
		}
	}
	if dirents, err := os.ReadDir(infoDir); err == nil {
		for _, de := range dirents {
			if !de.IsDir() && strings.HasSuffix(de.Name(), ".trashinfo") {
				if err := os.Remove(filepath.Join(infoDir, de.Name())); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// uniqueName returns base, or base.N for the first N >= 2 where neither the
// files/ nor the info/ name is taken. The info filename always mirrors the
// files/ entry name.
func uniqueName(filesDir, infoDir, base string) string {
	candidate := base
	for n := 2; ; n++ {
		_, filesErr := os.Lstat(filepath.Join(filesDir, candidate))
		_, infoErr := os.Lstat(filepath.Join(infoDir, candidate+".trashinfo"))
		if errors.Is(filesErr, os.ErrNotExist) && errors.Is(infoErr, os.ErrNotExist) {
			return candidate
		}
		candidate = base + "." + strconv.Itoa(n)
	}
}

// move renames src to dst. If src and dst are on different devices it falls
// back to a recursive copy followed by removal of the source. The copy is
// performed into a temporary name in the destination directory and renamed
// into place, so a partial copy is never visible as a trash entry.
func move(src, dst string, fi os.FileInfo) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else if !errors.Is(err, syscall.EXDEV) {
		return err
	}
	tmp := dst + ".burrow-partial"
	if err := copyRecursive(src, tmp, fi); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(src); err != nil {
		return fmt.Errorf("%w: %v", errSourceNotRemoved, err)
	}
	return nil
}

// copyRecursive copies a tree for the cross-device fallback. It preserves file
// mode and symlinks but not mtime, ownership, or extended attributes; restored
// items get fresh mtimes. That is acceptable for trash semantics (the content
// and structure are preserved) and avoids depending on platform-specific
// xattr/chown support.
func copyRecursive(src, dst string, fi os.FileInfo) error {
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		dirents, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, de := range dirents {
			childInfo, err := os.Lstat(filepath.Join(src, de.Name()))
			if err != nil {
				return err
			}
			if err := copyRecursive(filepath.Join(src, de.Name()), filepath.Join(dst, de.Name()), childInfo); err != nil {
				return err
			}
		}
		return nil
	default:
		return copyFile(src, dst, fi.Mode().Perm())
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

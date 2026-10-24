// Package safe is the safety core: path validation, identity snapshots, and
// fail-closed guards. Nothing in this package mutates the filesystem.
//
// Provenance: this is an independent reimplementation of the safety contract
// Mole applies on macOS (path validation, TOCTOU identity snapshots,
// user-mutable-ancestor checks). No Mole source was copied (Mole is GPL-3.0).
package safe

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

// ErrUnsafe is the sentinel returned (via errors.Is) by every validation
// failure. Callers use it to distinguish a safety refusal from an I/O error.
var ErrUnsafe = errors.New("unsafe path")

// Violation is a concrete reason a path was rejected.
type Violation struct {
	Path   string
	Reason string
}

func (e *Violation) Error() string {
	return fmt.Sprintf("unsafe path %q: %s", e.Path, e.Reason)
}

// Is lets errors.Is(err, ErrUnsafe) succeed for every violation.
func (e *Violation) Is(target error) bool { return target == ErrUnsafe }

func violation(path, reason string) *Violation {
	return &Violation{Path: path, Reason: reason}
}

// builtinProtectedRoots are directories that must never themselves be deleted.
// Documented subpaths may still be targeted via owner commands.
var builtinProtectedRoots = []string{
	"/",
	"/boot",
	"/bin",
	"/sbin",
	"/usr",
	"/etc",
	"/var",
	"/var/lib",
	"/var/log",
	"/home",
}

// isProtectedRoot reports whether clean (already filepath.Clean'd) is exactly a
// protected root. /lib, /lib64, /lib32, ... are matched by the /lib* rule.
func isProtectedRoot(clean string) bool {
	for _, root := range builtinProtectedRoots {
		if clean == root {
			return true
		}
	}
	if clean == "/lib" {
		return true
	}
	base := strings.TrimPrefix(clean, "/")
	if strings.HasPrefix(base, "lib") && !strings.Contains(base, "/") {
		rest := strings.TrimPrefix(base, "lib")
		if rest == "" {
			return true
		}
		allDigits := true
		for _, r := range rest {
			if !unicode.IsDigit(r) {
				allDigits = false
				break
			}
		}
		if allDigits {
			return true
		}
	}
	return false
}

// within reports whether p is root itself or lies underneath it.
func within(p, root string) bool {
	if p == root {
		return true
	}
	return strings.HasPrefix(p, root+string(filepath.Separator))
}

// physicalPath resolves symlinks in the parent directory only, leaving the
// final component untouched. Only the deletion root itself must be a real,
// non-symlink path; ancestors are allowed to be symlinks (spec §7.2, "at the
// root").
func physicalPath(clean string) (string, error) {
	parent, err := filepath.EvalSymlinks(filepath.Dir(clean))
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, filepath.Base(clean)), nil
}

// resolveOrSelf resolves all symlinks in p, or returns p unchanged when it
// cannot be resolved (for example, an allow-listed root that does not exist
// yet).
func resolveOrSelf(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// Validator applies the deletion-safety policy. The zero value is not usable;
// construct one with NewValidator.
type Validator struct {
	// Protected are additional absolute paths that are protected (config-driven).
	Protected []string
	// UID is the invoking user's uid. When CheckOwnership is true the target
	// must be owned by this uid (trash-contract rule).
	UID int
	// CheckOwnership enables the uid comparison.
	CheckOwnership bool
	// Roots, when non-empty, is an allow-list: the path must live inside one.
	Roots []string
	// RequireRoot enables the allow-list check.
	RequireRoot bool
}

// NewValidator returns a Validator with the built-in protected set.
func NewValidator(uid int, protected ...string) *Validator {
	return &Validator{
		Protected:      append([]string(nil), protected...),
		UID:            uid,
		CheckOwnership: uid >= 0,
	}
}

// DefaultValidator protects the built-in roots but does not check ownership.
var DefaultValidator = &Validator{UID: -1}

// ValidateForDeletion validates path against the built-in protected set.
func ValidateForDeletion(path string) error {
	return DefaultValidator.Validate(path)
}

// ValidateForDeletion is the policy-bound form.
func (v *Validator) ValidateForDeletion(path string) error { return v.Validate(path) }

// Validate applies every rule in spec §7.2 and fails closed on the first
// violation.
func (v *Validator) Validate(path string) error {
	if path == "" {
		return violation(path, "path is empty")
	}
	if !filepath.IsAbs(path) {
		return violation(path, "path is not absolute")
	}
	for _, r := range path {
		if unicode.IsControl(r) {
			return violation(path, "path contains control characters")
		}
	}
	if strings.Contains(path, "//") {
		return violation(path, "path contains an empty segment (//)")
	}
	clean := filepath.Clean(path)
	if clean != path {
		return violation(path, "path is not in canonical form")
	}
	trimmed := strings.TrimPrefix(clean, string(filepath.Separator))
	for _, seg := range strings.Split(trimmed, string(filepath.Separator)) {
		if seg == "" || seg == "." || seg == ".." {
			return violation(path, "path contains a '.' or '..' segment")
		}
	}
	if isProtectedRoot(clean) {
		return violation(path, "path is a protected root or under one")
	}
	for _, p := range v.Protected {
		pc := filepath.Clean(p)
		if within(clean, pc) {
			return violation(path, "path is a protected root or under one")
		}
	}
	if v.RequireRoot {
		ok := false
		for _, r := range v.Roots {
			if within(clean, filepath.Clean(r)) {
				ok = true
				break
			}
		}
		if !ok {
			return violation(path, "path is outside the allow-listed roots")
		}
	}
	fi, err := os.Lstat(clean)
	if err != nil {
		return violation(path, fmt.Sprintf("cannot resolve path: %v", err))
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		resolved, rerr := filepath.EvalSymlinks(clean)
		if rerr != nil {
			resolved = clean
		}
		return violation(path, fmt.Sprintf("path root is a symlink (resolves to %s)", resolved))
	}
	// Ancestors may legitimately be symlinks (Fedora Silverblue's /home ->
	// /var/home, symlinked mounts, some container homes). Resolve the parent
	// only and re-apply the protected and allow-list checks to the physical
	// path, so an ancestor symlink cannot redirect the target into a protected
	// directory or outside the allow-listed roots.
	physical, err := physicalPath(clean)
	if err != nil {
		return violation(path, fmt.Sprintf("cannot resolve path: %v", err))
	}
	if physical != clean {
		if isProtectedRoot(physical) {
			return violation(path, "path is a protected root or under one")
		}
		for _, p := range v.Protected {
			pc := filepath.Clean(p)
			if within(physical, pc) || within(physical, resolveOrSelf(pc)) {
				return violation(path, "path is a protected root or under one")
			}
		}
		if v.RequireRoot {
			ok := false
			for _, r := range v.Roots {
				rc := filepath.Clean(r)
				if within(physical, rc) || within(physical, resolveOrSelf(rc)) {
					ok = true
					break
				}
			}
			if !ok {
				return violation(path, "path is outside the allow-listed roots")
			}
		}
	}
	if v.CheckOwnership {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return violation(path, "cannot resolve path: cannot read stat")
		}
		if int(st.Uid) != v.UID {
			return violation(path, fmt.Sprintf("path is not owned by the invoking user (uid %d, owner %d)", v.UID, st.Uid))
		}
	}
	return nil
}

// WithinRoot reports whether path is root itself or lies underneath it. Both
// arguments are cleaned first. This is the primitive behind invariant I3.
func WithinRoot(path, root string) bool {
	return within(filepath.Clean(path), filepath.Clean(root))
}

// WithinAnyRoot reports whether path is inside any of roots.
func WithinAnyRoot(path string, roots []string) bool {
	for _, r := range roots {
		if WithinRoot(path, r) {
			return true
		}
	}
	return false
}

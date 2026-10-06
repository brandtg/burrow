// Package i18n holds the user-facing message catalog.
//
// v0.1 ships English only, but every string is addressed by a stable key so a
// translation can be added later without touching call sites. Keep keys
// grouped by package prefix.
package i18n

import "fmt"

// Key identifies a translatable message.
type Key string

const (
	// CLI / general.
	AppName       Key = "app.name"
	AppTagline    Key = "app.tagline"
	ErrGeneric    Key = "err.generic"
	ErrUsage      Key = "err.usage"
	ErrNotImpl    Key = "err.not_implemented"
	ErrGuardStop  Key = "err.guard_stop"
	ErrTimeout    Key = "err.timeout"
	ErrInterrupt  Key = "err.interrupt"
	WarnJournal   Key = "warn.journal_write"
	WarnNoJournal Key = "warn.journal_unavailable"

	// Environment / elevation.
	ErrRootRefused Key = "err.root_refused"
	ErrUserResolve Key = "err.user_resolve"

	// Path safety.
	ErrPathEmpty       Key = "err.path_empty"
	ErrPathNotAbs      Key = "err.path_not_absolute"
	ErrPathControl     Key = "err.path_control"
	ErrPathDoubleSlash Key = "err.path_double_slash"
	ErrPathNotClean    Key = "err.path_not_clean"
	ErrPathDotSegment  Key = "err.path_dot_segment"
	ErrPathProtected   Key = "err.path_protected"
	ErrPathSymlink     Key = "err.path_symlink_root"
	ErrPathNotOwned    Key = "err.path_not_owned"
	ErrPathNoRoot      Key = "err.path_outside_roots"
	ErrPathResolve     Key = "err.path_resolve"

	// Identity / guards.
	ErrIdentityChanged Key = "err.identity_changed"
	ErrGuardUnknown    Key = "err.guard_unknown"
	ErrGuardBusy       Key = "err.guard_busy"
	ErrMutableAncestor Key = "err.mutable_ancestor"

	// Trash.
	ErrTrashUnavailable  Key = "err.trash_unavailable"
	ErrTrashNotInTrash   Key = "err.trash_not_in_trash"
	ErrTrashTargetExists Key = "err.trash_target_exists"
)

var en = map[Key]string{
	AppName:    "burrow",
	AppTagline: "a prescriptive Linux disk-space and cleanup tool",

	ErrGeneric:    "error: %v",
	ErrUsage:      "usage error: %v",
	ErrNotImpl:    "%s is not implemented yet",
	ErrGuardStop:  "stopped by safety guard: %s",
	ErrTimeout:    "timed out after %s: %s",
	ErrInterrupt:  "interrupted",
	WarnJournal:   "warning: could not write journal record: %v",
	WarnNoJournal: "warning: journal unavailable: %v",

	ErrRootRefused: "refusing to run as root without SUDO_USER; run burrow as your normal user",
	ErrUserResolve: "cannot resolve invoking user: %v",

	ErrPathEmpty:       "path is empty",
	ErrPathNotAbs:      "path is not absolute",
	ErrPathControl:     "path contains control characters",
	ErrPathDoubleSlash: "path contains an empty segment (//)",
	ErrPathNotClean:    "path is not in canonical form",
	ErrPathDotSegment:  "path contains a '.' or '..' segment",
	ErrPathProtected:   "path is a protected root or under one",
	ErrPathSymlink:     "path root is a symlink (resolves to %s)",
	ErrPathNotOwned:    "path is not owned by the invoking user (uid %d, owner %d)",
	ErrPathNoRoot:      "path is outside the allow-listed roots",
	ErrPathResolve:     "cannot resolve path: %v",

	ErrIdentityChanged: "path identity changed since snapshot",
	ErrGuardUnknown:    "cannot prove the action is safe right now",
	ErrGuardBusy:       "the owner is busy",
	ErrMutableAncestor: "privileged path has a user-mutable ancestor: %s",

	ErrTrashUnavailable:  "trash is unavailable: %v",
	ErrTrashNotInTrash:   "item is not present in the trash",
	ErrTrashTargetExists: "restore target already exists: %s",
}

// T returns the message for k, or the key itself if untranslated.
func T(k Key) string {
	if s, ok := en[k]; ok {
		return s
	}
	return string(k)
}

// Tf formats the message for k with args.
func Tf(k Key, args ...any) string {
	return fmt.Sprintf(T(k), args...)
}

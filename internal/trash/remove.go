package trash

import "os"

// Remove permanently deletes path. It is the explicit --permanent escape hatch
// (spec §5.2, §7.4) and the only place a user cache is unlinked rather than
// trashed. It is still gated by the caller's path validation and identity
// snapshot; it must never be reached implicitly. The destructive sink lives
// here so the static gate can see it inside an allow-listed package.
func Remove(path string) error {
	return os.RemoveAll(path)
}

// Package disk provides shared conversions from stat results to on-disk sizes.
package disk

import (
	"os"
	"syscall"
)

// Blocks512 returns the on-disk size of fi in bytes from its 512-byte block
// count, falling back to the apparent size when the platform cannot report
// blocks. Callers that need "does this have a block count" should not use this
// helper.
func Blocks512(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

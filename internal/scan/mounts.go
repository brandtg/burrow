package scan

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// mountEntry is one line of /proc/self/mountinfo. Source identifies the backing
// store (a block device for local filesystems); dev is the kernel's major:minor
// for the mount. A scan stays within the backing store of its root, so btrfs
// subvolumes that share a block device (for example / and /home) are treated as
// the same drive even though their st_dev differs.
type mountEntry struct {
	point  string
	source string
	fstype string
	dev    string
}

// parseMountInfo parses the mountinfo(5) stream. Lines that do not contain the
// optional-field separator are skipped rather than aborting the table.
func parseMountInfo(r io.Reader) []mountEntry {
	var mounts []mountEntry
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 10 {
			continue
		}
		sep := -1
		for i, f := range fields {
			if f == "-" {
				sep = i
				break
			}
		}
		if sep < 0 || sep+2 >= len(fields) {
			continue
		}
		mounts = append(mounts, mountEntry{
			point:  unescapeMount(fields[4]),
			source: unescapeMount(fields[sep+2]),
			fstype: fields[sep+1],
			dev:    fields[2],
		})
	}
	// Longest mount point first so mountFor returns the most specific match.
	sort.SliceStable(mounts, func(i, j int) bool {
		return len(mounts[i].point) > len(mounts[j].point)
	})
	return mounts
}

// loadMounts reads the live mount table. A missing or unreadable file yields an
// empty table, which makes the caller fall back to st_dev comparison.
func loadMounts() []mountEntry {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return nil
	}
	defer f.Close()
	return parseMountInfo(f)
}

// mountFor returns the mount that contains path, choosing the longest matching
// mount point. ok is false when path is not under any known mount (for example
// an empty table).
func mountFor(mounts []mountEntry, path string) (mountEntry, bool) {
	path = filepath.Clean(path)
	for _, m := range mounts {
		if withinMount(path, m.point) {
			return m, true
		}
	}
	return mountEntry{}, false
}

// withinMount reports whether path is the mount point itself or lives below it.
func withinMount(path, point string) bool {
	if path == point {
		return true
	}
	if point == "/" {
		return strings.HasPrefix(path, "/")
	}
	return strings.HasPrefix(path, point+"/")
}

// sameBacking reports whether two mounts are backed by the same store. Source
// equality is checked first so btrfs subvolumes on one device compare equal;
// the major:minor pair is the fallback for mounts without a meaningful source.
func sameBacking(a, b mountEntry) bool {
	if a.source != "" && a.source == b.source {
		return true
	}
	return a.dev != "" && a.dev == b.dev
}

// isPseudoFstype reports whether a mount is a kernel/userspace virtual
// filesystem rather than a real backing store. Such mounts are still skipped
// by the boundary but are not worth a warning: a tmpfs credential store or a
// gvfs fuse mount is not "another drive" the user must scan separately.
func isPseudoFstype(fstype string) bool {
	switch fstype {
	case "proc", "sysfs", "devtmpfs", "devpts", "tmpfs", "cgroup", "cgroup2",
		"securityfs", "debugfs", "tracefs", "configfs", "bpf", "nsfs",
		"mqueue", "hugetlbfs", "pstore", "efivarfs", "autofs", "rpc_pipefs",
		"fusectl", "binfmt_misc", "selinuxfs", "ramfs", "overlay", "squashfs":
		return true
	}
	return strings.HasPrefix(fstype, "fuse.")
}

// unescapeMount decodes the octal escapes mountinfo(5) uses for whitespace and
// backslashes in mount points and sources (for example \040 for a space).
func unescapeMount(s string) string {
	if !strings.ContainsRune(s, '\\') {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+3 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		if v, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
			b.WriteByte(byte(v))
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

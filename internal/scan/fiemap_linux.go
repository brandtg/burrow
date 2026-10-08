//go:build linux

package scan

import (
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	fiemapExtentShared = 0x00002000
	fsIOCFiemap        = 0xC020660B
	fiemapHeaderSize   = 32
	fiemapExtentSize   = 56
	fiemapMaxExtents   = 128
)

// fiemapHeader mirrors struct fiemap (include/uapi/linux/fiemap.h).
type fiemapHeader struct {
	start         uint64
	length        uint64
	flags         uint32
	mappedExtents uint32
	extentCount   uint32
	reserved      uint32
}

// fiemapExtent mirrors struct fiemap_extent.
type fiemapExtent struct {
	logical    uint64
	physical   uint64
	length     uint64
	reserved64 [2]uint64
	flags      uint32
	reserved   [3]uint32
}

// sharedExtentFS reports whether path is on a filesystem that can share blocks
// between files (reflink). Only these need the shared-extent accounting pass.
func sharedExtentFS(path string) bool {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return false
	}
	switch uint64(st.Type) {
	case 0x9123683E: // btrfs
		return true
	case 0x58465342: // xfs
		return true
	}
	return false
}

// fiemapFile returns a file's physical extents and on-disk block count. It
// never follows a symlink; callers pass regular files only.
func fiemapFile(path string) (fileInfoExtents, error) {
	var out fileInfoExtents
	f, err := os.Open(path)
	if err != nil {
		return out, err
	}
	defer f.Close()

	var st syscall.Stat_t
	if err := syscall.Fstat(int(f.Fd()), &st); err != nil {
		return out, err
	}
	out.dev = st.Dev
	out.ino = st.Ino
	out.blocks = st.Blocks * 512

	buf := make([]byte, fiemapHeaderSize+fiemapMaxExtents*fiemapExtentSize)
	hdr := (*fiemapHeader)(unsafe.Pointer(&buf[0]))
	exts := unsafe.Slice((*fiemapExtent)(unsafe.Pointer(&buf[fiemapHeaderSize])), fiemapMaxExtents)

	var start uint64
	for {
		hdr.start = start
		hdr.length = ^uint64(0)
		hdr.flags = 0
		hdr.mappedExtents = 0
		hdr.extentCount = fiemapMaxExtents
		hdr.reserved = 0

		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), fsIOCFiemap, uintptr(unsafe.Pointer(hdr)))
		if errno != 0 {
			return out, errno
		}
		mapped := int(hdr.mappedExtents)
		if mapped > fiemapMaxExtents {
			mapped = fiemapMaxExtents
		}
		for i := 0; i < mapped; i++ {
			e := exts[i]
			out.extents = append(out.extents, extent{
				physical: e.physical,
				length:   e.length,
				shared:   e.flags&fiemapExtentShared != 0,
			})
			start = e.logical + e.length
		}
		if mapped < fiemapMaxExtents {
			break
		}
	}
	return out, nil
}

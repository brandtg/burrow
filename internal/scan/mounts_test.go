package scan

import (
	"strings"
	"testing"
)

const sampleMountInfo = `36 35 98:0 /mnt1 /mnt2 rw,noatime master:1 - ext3 /dev/root rw,errors=continue
43 1 0:35 /root / rw,relatime shared:1 - btrfs /dev/nvme0n1p6 rw,subvolid=257
60 43 0:35 /home /home rw,relatime shared:162 - btrfs /dev/nvme0n1p6 rw,subvolid=256
810 53 8:1 / /run/media/greg-brandt/storage rw,nosuid - ext4 /dev/sda1 rw
20 1 0:20 / /mnt/with\040space rw - vfat /dev/sdb1 rw
`

func TestParseMountInfo(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	if len(mounts) != 5 {
		t.Fatalf("parsed %d mounts, want 5", len(mounts))
	}
	// Longest point first so mountFor is a simple scan.
	for i := 1; i < len(mounts); i++ {
		if len(mounts[i-1].point) < len(mounts[i].point) {
			t.Fatalf("mounts not sorted by point length: %+v", mounts)
		}
	}
	// The escaped space in the mount point must be decoded.
	if m, ok := mountFor(mounts, "/mnt/with space/file"); !ok || m.source != "/dev/sdb1" {
		t.Fatalf("escaped mount point not decoded: %+v ok=%v", m, ok)
	}
}

func TestMountForLongestPrefix(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	m, ok := mountFor(mounts, "/run/media/greg-brandt/storage/system_backup/data")
	if !ok {
		t.Fatal("no mount matched")
	}
	if m.point != "/run/media/greg-brandt/storage" || m.source != "/dev/sda1" {
		t.Fatalf("matched %+v, want the storage ext4 mount", m)
	}
}

func TestSameBackingBtrfsSubvolumes(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	root, _ := mountFor(mounts, "/")
	home, _ := mountFor(mounts, "/home/greg-brandt")
	if !sameBacking(root, home) {
		t.Fatalf("btrfs subvolumes on one device should share a backing store: %+v vs %+v", root, home)
	}
	storage, _ := mountFor(mounts, "/run/media/greg-brandt/storage")
	if sameBacking(root, storage) {
		t.Fatal("a separate block device must not compare equal")
	}
}

func TestIsPseudoFstype(t *testing.T) {
	for _, f := range []string{"tmpfs", "proc", "sysfs", "devtmpfs", "fuse.portal", "fuse.gvfsd-fuse", "overlay"} {
		if !isPseudoFstype(f) {
			t.Errorf("%s should be pseudo", f)
		}
	}
	for _, f := range []string{"ext4", "btrfs", "xfs", "vfat", "ntfs3"} {
		if isPseudoFstype(f) {
			t.Errorf("%s should be a real filesystem", f)
		}
	}
}

func TestUnescapeMount(t *testing.T) {
	cases := map[string]string{
		`/mnt/with\040space`: "/mnt/with space",
		`/plain`:             "/plain",
		`/a\134b`:            `/a\b`,
		`/trailing\`:         `/trailing\`,
	}
	for in, want := range cases {
		if got := unescapeMount(in); got != want {
			t.Errorf("unescapeMount(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMakeIgnoreDropsAncestorOfRoot(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	root := "/run/media/greg-brandt/storage/system_backup"
	rootMount, ok := mountFor(mounts, root)
	if !ok {
		t.Fatal("no mount for root")
	}
	skips := newSkipCollector()
	// /run is an ancestor of the root; applying it would prune the whole scan.
	ignore := makeIgnore(root, Options{Ignore: []string{"/run"}}, mounts, rootMount, ok, skips)
	if ignore("data", root+"/data") {
		t.Fatal("an ignore that is an ancestor of the scan root must be dropped")
	}
}

func TestMakeIgnoreKeepsExplicitChildIgnore(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	root := "/run/media/greg-brandt/storage/system_backup"
	rootMount, ok := mountFor(mounts, root)
	skips := newSkipCollector()
	ignore := makeIgnore(root, Options{Ignore: []string{root + "/data"}}, mounts, rootMount, ok, skips)
	if !ignore("data", root+"/data") {
		t.Fatal("a child ignore must still apply")
	}
	if ignore("other", root+"/other") {
		t.Fatal("a sibling must not be ignored")
	}
}

func TestMakeIgnoreMountBoundary(t *testing.T) {
	mounts := parseMountInfo(strings.NewReader(sampleMountInfo))
	root := "/"
	rootMount, ok := mountFor(mounts, root)
	skips := newSkipCollector()
	ignore := makeIgnore(root, Options{OneFileSystem: true}, mounts, rootMount, ok, skips)

	// A real filesystem on another device is skipped and recorded.
	if !ignore("storage", "/run/media/greg-brandt/storage") {
		t.Fatal("a mount on another device must be skipped")
	}
	if got := skips.list(); len(got) != 1 || got[0].Path != "/run/media/greg-brandt/storage" {
		t.Fatalf("skipped mounts = %+v", got)
	}
	// A btrfs subvolume on the same device stays in scope.
	if ignore("home", "/home") {
		t.Fatal("a subvolume on the same backing device must not be skipped")
	}
}

func TestMakeIgnoreMountBoundaryPseudoNotWarned(t *testing.T) {
	const info = `53 43 0:29 / /run rw,nosuid - tmpfs tmpfs rw
54 53 0:40 / /run/credentials/x rw - tmpfs none rw
810 53 8:1 / /run/media/storage rw - ext4 /dev/sda1 rw
`
	mounts := parseMountInfo(strings.NewReader(info))
	root := "/run"
	rootMount, ok := mountFor(mounts, root)
	if !ok || rootMount.point != "/run" {
		t.Fatalf("root mount = %+v ok=%v", rootMount, ok)
	}
	skips := newSkipCollector()
	ignore := makeIgnore(root, Options{OneFileSystem: true}, mounts, rootMount, ok, skips)
	if !ignore("x", "/run/credentials/x") {
		t.Fatal("a pseudo mount must still be skipped")
	}
	if !ignore("storage", "/run/media/storage") {
		t.Fatal("a real mount on another device must be skipped")
	}
	if got := skips.list(); len(got) != 1 || got[0].Path != "/run/media/storage" {
		t.Fatalf("pseudo mounts should not be warned about: %+v", got)
	}
}

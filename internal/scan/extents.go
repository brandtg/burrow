package scan

import (
	"context"

	"github.com/brandtg/burrow/internal/owner"
)

// extent is one physical extent of a file, as reported by FIEMAP. shared is
// true when the extent is reflinked (referenced by another file).
type extent struct {
	physical uint64
	length   uint64
	shared   bool
}

// extentKey identifies a physical extent. Two files that reflink the same
// blocks report the same key, which is how sharing is detected without any
// filesystem-specific bookkeeping.
type extentKey struct {
	physical uint64
	length   uint64
}

// inodeKey identifies a file on disk. Hardlinks share an inode and must be
// counted once, matching gdu's inode de-duplication.
type inodeKey struct {
	dev uint64
	ino uint64
}

// fileInfoExtents is the result of inspecting one file.
type fileInfoExtents struct {
	dev     uint64
	ino     uint64
	blocks  int64 // on-disk usage (st_blocks*512), shared extents counted in full
	extents []extent
}

// fileExtents pairs a scanned file with its physical extents so its on-disk
// size can be attributed fairly against every other file sharing those blocks.
type fileExtents struct {
	node    *Node
	blocks  int64
	extents []extent
}

// applySharedExtents replaces the logical on-disk sizes in a scanned tree with
// shared-extent-aware sizes on filesystems that support reflinks (btrfs, xfs).
//
// gdu (like du) charges every inode the full block count of its extents, so a
// tree of reflinked copies is reported at N times its real size. Here each
// physical extent is charged once and split evenly among the files that
// reference it, which keeps every directory total equal to the bytes that
// subtree actually occupies. Files with no shared extents are left untouched,
// so the pass is a no-op on a tree without reflinks and never runs on a
// filesystem that cannot share blocks.
func applySharedExtents(ctx context.Context, root *Node, rootPath string) {
	if root == nil || !sharedExtentFS(rootPath) {
		return
	}
	// The pass re-opens every file, so it gets its own scan-sized budget rather
	// than sharing the directory walk's deadline. A canceled context (Ctrl-C)
	// still aborts it.
	passCtx, cancel := context.WithTimeout(ctx, owner.TimeoutScanPerRoot)
	defer cancel()

	files := collectSharedFiles(passCtx, root)
	if files == nil {
		return
	}
	applyExtentAccounting(files)
	recomputeDirSizes(root)
}

// collectSharedFiles walks the tree and returns the files that have at least
// one reflinked extent. Files that share an inode are counted once (later
// occurrences are zeroed). A canceled context aborts the pass and returns nil.
func collectSharedFiles(ctx context.Context, root *Node) []fileExtents {
	seen := map[inodeKey]bool{}
	var out []fileExtents
	var walk func(n *Node) bool
	walk = func(n *Node) bool {
		if ctx.Err() != nil {
			return false
		}
		if !n.IsDir {
			if n.Type == "File" && n.Path != "" {
				info, err := fiemapFile(n.Path)
				if err == nil {
					key := inodeKey{dev: info.dev, ino: info.ino}
					if seen[key] {
						n.Size = 0
					} else {
						seen[key] = true
						for _, e := range info.extents {
							if e.shared {
								out = append(out, fileExtents{node: n, blocks: info.blocks, extents: info.extents})
								break
							}
						}
					}
				}
			}
			return true
		}
		for _, c := range n.Children {
			if !walk(c) {
				return false
			}
		}
		return true
	}
	if !walk(root) {
		return nil
	}
	return out
}

// applyExtentAccounting sets each file's on-disk size to its fair share of the
// physical extents it references: the exclusive bytes in full, plus 1/N of
// every shared extent, where N is the number of scanned files sharing it. The
// sum over all files therefore equals the bytes the tree really occupies.
func applyExtentAccounting(files []fileExtents) {
	sharers := map[extentKey]int{}
	for _, f := range files {
		for _, e := range f.extents {
			if e.shared {
				sharers[extentKey{e.physical, e.length}]++
			}
		}
	}
	for _, f := range files {
		var totalLen, sharedLen uint64
		for _, e := range f.extents {
			totalLen += e.length
			if e.shared {
				sharedLen += e.length
			}
		}
		if totalLen == 0 || f.blocks <= 0 {
			f.node.Size = 0
			continue
		}
		// Compressed extents report a logical length, so scale the file's real
		// on-disk blocks by each extent's share of the logical length. The
		// result is exact for uncompressed files and proportional otherwise.
		exclusive := float64(f.blocks) * float64(totalLen-sharedLen) / float64(totalLen)
		shared := 0.0
		for _, e := range f.extents {
			if !e.shared {
				continue
			}
			n := sharers[extentKey{e.physical, e.length}]
			if n < 1 {
				n = 1
			}
			shared += float64(f.blocks) * float64(e.length) / float64(totalLen) / float64(n)
		}
		f.node.Size = int64(exclusive + shared + 0.5)
	}
}

// recomputeDirSizes re-aggregates directory on-disk sizes bottom-up. gdu does
// not charge a directory for its own metadata, so a directory's usage is the
// sum of its children's usage.
func recomputeDirSizes(n *Node) int64 {
	if !n.IsDir {
		return n.Size
	}
	var sum int64
	for _, c := range n.Children {
		sum += recomputeDirSizes(c)
	}
	n.Size = sum
	return sum
}

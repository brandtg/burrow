// Package scan is the only place that imports gdu. All gdu usage is confined
// here so the in-process analyzer can be swapped for the `gdu --json`
// shell-out without touching the rest of the tree (spec D2 / risk table).
package scan

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/brandtg/burrow/internal/owner"
	"github.com/dundee/gdu/v5/pkg/analyze"
	"github.com/dundee/gdu/v5/pkg/fs"
)

// DefaultIgnore is the default set of pseudo-filesystems a scan does not
// descend into (spec §10).
var DefaultIgnore = []string{"/proc", "/dev", "/sys", "/run"}

// Scan status values reported by Status and the --json schema (spec §10).
const (
	StatusComplete    = "complete"
	StatusPartial     = "partial"
	StatusUnavailable = "unavailable"
)

// Scanner is the stable interface over the scan engine. Callers never import
// gdu directly, so the engine can be swapped without touching them.
type Scanner interface {
	Scan(ctx context.Context, root string, opts Options) (*Node, error)
}

// Options controls a scan.
type Options struct {
	// Ignore is a list of absolute paths to skip (the path and its subtree).
	// When nil, DefaultIgnore is used; an explicit empty non-nil slice disables
	// the default ignores.
	Ignore []string
	// OneFileSystem does not descend across mount points.
	OneFileSystem bool
	// NoHidden skips dotfiles.
	NoHidden bool
	// Apparent records whether the caller wants apparent sizes presented. The
	// Node always carries both Size (on-disk) and Apparent; this flag only
	// selects which one presentation layers should use.
	Apparent bool
	// Depth limits how many levels of children are retained. Depth <= 0 means
	// unlimited; Depth 1 keeps the root and its immediate children.
	Depth int
}

// Node is burrow's own item model. Other packages never see gdu types.
//
// Size is on-disk usage (blocks*512) and Apparent is the apparent size. They
// are never swapped: a caller that reads Size as apparent would render the TUI
// and --json inverted.
type Node struct {
	Name      string
	Path      string
	Size      int64 // on-disk (blocks*512)
	Apparent  int64 // apparent size
	IsDir     bool
	Type      string
	Mtime     time.Time
	ItemCount int64
	Children  []*Node
	SizeKnown bool // false when the engine could not size this node
}

// DisplaySize returns the size a presentation layer should show, honoring the
// Apparent option.
func (n *Node) DisplaySize(apparent bool) int64 {
	if n == nil {
		return 0
	}
	if apparent {
		return n.Apparent
	}
	return n.Size
}

// GDU is the default Scanner, backed by gdu's in-process parallel analyzer.
type GDU struct{}

// Default returns the default Scanner.
func Default() Scanner { return GDU{} }

// Scan analyzes root and returns a Node tree. It honors ctx cancellation and
// applies the per-root deadline from owner.TimeoutScanPerRoot.
func (GDU) Scan(ctx context.Context, root string, opts Options) (*Node, error) {
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)

	scanCtx, cancel := context.WithTimeout(ctx, owner.TimeoutScanPerRoot)
	defer cancel()

	// A single file is sized directly; gdu's directory analyzer is for trees.
	if fi, err := os.Lstat(abs); err != nil {
		return nil, err
	} else if !fi.IsDir() {
		return fileNode(abs, fi), nil
	}

	analyzer := analyze.CreateAnalyzer()
	ignore := makeIgnore(abs, opts)
	var fileFilter func(string) bool
	if opts.NoHidden {
		// gdu applies the ignore predicate only to directories, so hidden
		// files need the file-type filter as well.
		fileFilter = func(name string) bool { return strings.HasPrefix(name, ".") }
	}
	done := make(chan fs.Item, 1)
	go func() {
		done <- analyzer.AnalyzeDir(abs, ignore, fileFilter)
	}()

	var item fs.Item
	select {
	case <-scanCtx.Done():
		analyzer.Cancel()
		return nil, scanCtx.Err()
	case item = <-done:
	}
	if item == nil {
		// The engine could not provide a tree (for example an unreadable
		// root). Fall back to du/stat and mark the size unknown on failure.
		return fallback(scanCtx, abs)
	}
	// AnalyzeDir builds the tree but leaves size/usage aggregation to the
	// caller. A shared, non-nil HardLinkedItems map makes shared inodes count
	// once (spec §8.8).
	item.UpdateStats(make(fs.HardLinkedItems))
	return build(scanCtx, item, opts.Depth, opts.Depth <= 0), nil
}

// Analyze is the Plan 01 entry point, retained for callers that do not need
// the Scanner interface.
func Analyze(ctx context.Context, path string, opts Options) (*Node, error) {
	return GDU{}.Scan(ctx, path, opts)
}

// build copies the gdu item tree into burrow's Node model, bounded by depth.
func build(ctx context.Context, item fs.Item, depth int, unlimited bool) *Node {
	n := &Node{
		Name:      item.GetName(),
		Path:      item.GetPath(),
		Size:      item.GetUsage(),
		Apparent:  item.GetSize(),
		IsDir:     item.IsDir(),
		Type:      item.GetType(),
		Mtime:     item.GetMtime(),
		ItemCount: item.GetItemCount(),
		SizeKnown: item.GetFlag() != '!',
	}
	if item.IsDir() {
		if unlimited || depth > 0 {
			for child := range item.GetFiles(fs.SortBySize, fs.SortDesc) {
				n.Children = append(n.Children, build(ctx, child, depth-1, unlimited))
			}
		}
		// A directory the engine flagged as unreadable has no usable size.
		// Try du as a last resort before declaring the node unknown.
		if !n.SizeKnown {
			if size, ok := duSize(ctx, n.Path); ok {
				n.Size = size
				n.Apparent = size
				n.SizeKnown = true
			}
		}
	}
	return n
}

// fallback sizes path without gdu. Directories use du -skP (on-disk); files use
// stat. On any failure the node is returned with SizeKnown=false so callers
// render "unknown" rather than 0.
func fallback(ctx context.Context, path string) (*Node, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return fileNode(path, fi), nil
	}
	n := &Node{
		Name:  filepath.Base(path),
		Path:  path,
		IsDir: true,
		Type:  "Directory",
		Mtime: fi.ModTime(),
	}
	if size, ok := duSize(ctx, path); ok {
		n.Size = size
		n.Apparent = size
		n.SizeKnown = true
	}
	return n, nil
}

// fileNode sizes a single file from stat: on-disk from Blocks*512, apparent
// from Size.
func fileNode(path string, fi os.FileInfo) *Node {
	n := &Node{
		Name:      filepath.Base(path),
		Path:      path,
		Apparent:  fi.Size(),
		Mtime:     fi.ModTime(),
		Type:      "File",
		SizeKnown: true,
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		n.Size = st.Blocks * 512
	}
	return n
}

// duSize runs `du -skP` under the per-root scan deadline and returns on-disk
// bytes. It is the fallback when the engine cannot size a subtree.
func duSize(ctx context.Context, path string) (int64, bool) {
	probeCtx, cancel := context.WithTimeout(ctx, owner.TimeoutScanPerRoot)
	defer cancel()
	out, err := exec.CommandContext(probeCtx, "du", "-skP", "--", path).Output()
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return 0, false
	}
	kib, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	return kib * 1024, true
}

// Status classifies a scanned tree for the --json scan_status field.
func Status(n *Node) string {
	if n == nil {
		return StatusUnavailable
	}
	if !n.SizeKnown && n.IsDir && len(n.Children) == 0 {
		return StatusUnavailable
	}
	if hasUnknown(n) {
		return StatusPartial
	}
	return StatusComplete
}

func hasUnknown(n *Node) bool {
	if n == nil {
		return false
	}
	if !n.SizeKnown {
		return true
	}
	for _, c := range n.Children {
		if hasUnknown(c) {
			return true
		}
	}
	return false
}

// FileCount returns the number of non-directory nodes in the tree.
func FileCount(n *Node) int64 {
	if n == nil {
		return 0
	}
	var count int64
	if !n.IsDir {
		count = 1
	}
	for _, c := range n.Children {
		count += FileCount(c)
	}
	return count
}

// Flatten returns every node in the tree in depth-first order (excluding n).
func Flatten(n *Node) []*Node {
	if n == nil {
		return nil
	}
	var out []*Node
	var walk func(*Node)
	walk = func(node *Node) {
		for _, c := range node.Children {
			out = append(out, c)
			walk(c)
		}
	}
	walk(n)
	return out
}

// makeIgnore builds gdu's ignore predicate from the options, merging the
// default pseudo-filesystem list when the caller did not supply one.
func makeIgnore(root string, opts Options) func(name, path string) bool {
	ignoreList := opts.Ignore
	if ignoreList == nil {
		ignoreList = DefaultIgnore
	}
	ignored := make([]string, 0, len(ignoreList))
	for _, p := range ignoreList {
		ignored = append(ignored, filepath.Clean(p))
	}
	rootDev := deviceOf(root)
	return func(name, path string) bool {
		clean := filepath.Clean(path)
		for _, p := range ignored {
			if clean == p || strings.HasPrefix(clean, p+string(filepath.Separator)) {
				return true
			}
		}
		if opts.NoHidden && strings.HasPrefix(name, ".") {
			return true
		}
		if opts.OneFileSystem && rootDev != 0 {
			if d := deviceOf(clean); d != 0 && d != rootDev {
				return true
			}
		}
		return false
	}
}

func deviceOf(path string) uint64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(st.Dev)
}

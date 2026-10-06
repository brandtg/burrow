// Package scan is the only place that imports gdu. All gdu usage is confined
// here so the in-process analyzer can be swapped for the `gdu --json`
// shell-out without touching the rest of the tree (spec D2 / risk table).
package scan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/dundee/gdu/v5/pkg/analyze"
	"github.com/dundee/gdu/v5/pkg/fs"
)

// Options controls a scan.
type Options struct {
	// Ignore is a list of absolute paths to skip (the path and its subtree).
	Ignore []string
	// OneFileSystem does not descend across mount points.
	OneFileSystem bool
	// NoHidden skips dotfiles.
	NoHidden bool
}

// Node is burrow's own item model. Other packages never see gdu types.
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
}

// Analyze scans path and returns a Node tree. It honors ctx cancellation.
func Analyze(ctx context.Context, path string, opts Options) (*Node, error) {
	analyzer := analyze.CreateAnalyzer()
	ignore := makeIgnore(path, opts)
	done := make(chan fs.Item, 1)
	go func() {
		done <- analyzer.AnalyzeDir(path, ignore, nil)
	}()
	select {
	case <-ctx.Done():
		analyzer.Cancel()
		return nil, ctx.Err()
	case item := <-done:
		if item == nil {
			return nil, os.ErrNotExist
		}
		// AnalyzeDir builds the tree but leaves size/usage aggregation to the
		// caller. Compute it once at the root so every node has stats.
		item.UpdateStats(fs.HardLinkedItems{})
		return build(item), nil
	}
}

func build(item fs.Item) *Node {
	n := &Node{
		Name:      item.GetName(),
		Path:      item.GetPath(),
		Size:      item.GetUsage(),
		Apparent:  item.GetSize(),
		IsDir:     item.IsDir(),
		Type:      item.GetType(),
		Mtime:     item.GetMtime(),
		ItemCount: item.GetItemCount(),
	}
	if item.IsDir() {
		for child := range item.GetFiles(fs.SortBySize, fs.SortDesc) {
			n.Children = append(n.Children, build(child))
		}
	}
	return n
}

func makeIgnore(root string, opts Options) func(name, path string) bool {
	ignored := make([]string, 0, len(opts.Ignore))
	for _, p := range opts.Ignore {
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

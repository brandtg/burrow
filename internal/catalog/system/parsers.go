package system

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/disk"
)

// parseJournaldUsage extracts the size from `journalctl --disk-usage` output:
// "Archived and active journals take up 512.0M in the file system."
func parseJournaldUsage(out string) (int64, bool) {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "up" && i+1 < len(fields) {
			size, err := config.ParseBytes(fields[i+1])
			if err != nil {
				return 0, false
			}
			return size, true
		}
	}
	return 0, false
}

// measureRotatedLogs sums the on-disk size of rotated log files under dir
// (depth <= 2, ignoring the journal subdirectory). It is read-only.
func measureRotatedLogs(dir string) (int64, bool) {
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		return 0, false
	}
	var total int64
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == dir {
				return nil
			}
			rel, relErr := filepath.Rel(dir, path)
			if relErr == nil && strings.Count(rel, string(filepath.Separator)) >= 1 {
				return filepath.SkipDir
			}
			if d.Name() == "journal" {
				return filepath.SkipDir
			}
			return nil
		}
		if isRotatedLog(d.Name()) {
			if info, infoErr := d.Info(); infoErr == nil {
				total += disk.Blocks512(info)
			}
		}
		return nil
	})
	return total, true
}

func isRotatedLog(name string) bool {
	lower := strings.ToLower(name)
	for _, suf := range []string{".gz", ".old", ".1", ".2"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	return false
}

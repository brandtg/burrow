package system

import (
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// parseAptRemovals extracts the "N to remove" count from `apt-get -s
// autoremove` output. A missing count means nothing would be removed.
func parseAptRemovals(out string) int {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		for i, f := range fields {
			if f == "to" && i+1 < len(fields) && fields[i+1] == "remove" && i > 0 {
				if n, err := strconv.Atoi(fields[i-1]); err == nil {
					return n
				}
			}
		}
	}
	return 0
}

// parseAptFreed extracts the estimated freed bytes from `apt-get -s` output.
func parseAptFreed(out string) (int64, bool) {
	const marker = "After this operation, "
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, marker)
		if i < 0 {
			continue
		}
		fields := strings.Fields(line[i+len(marker):])
		if len(fields) < 2 {
			continue
		}
		n, err := strconv.ParseFloat(strings.Trim(fields[0], ","), 64)
		if err != nil {
			continue
		}
		mult := int64(1)
		switch strings.ToLower(strings.Trim(fields[1], ",")) {
		case "kb", "kib":
			mult = 1 << 10
		case "mb", "mib":
			mult = 1 << 20
		case "gb", "gib":
			mult = 1 << 30
		}
		return int64(n * float64(mult)), true
	}
	return 0, false
}

// parseResidualConfig returns package names from `dpkg -l` lines with status
// `rc` (removed but config files remain).
func parseResidualConfig(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "rc ") && !strings.HasPrefix(line, "rc\t") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			pkgs = append(pkgs, fields[1])
		}
	}
	return pkgs
}

// parseJournaldUsage extracts the size from `journalctl --disk-usage` output:
// "Archived and active journals take up 512.0M in the file system."
func parseJournaldUsage(out string) (int64, bool) {
	fields := strings.Fields(out)
	for i, f := range fields {
		if f == "up" && i+1 < len(fields) {
			return parseHumanSize(fields[i+1])
		}
	}
	return 0, false
}

// parseHumanSize parses values such as "512.0M" or "1.5G".
func parseHumanSize(s string) (int64, bool) {
	upper := strings.ToUpper(strings.TrimSpace(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(upper, "K"):
		mult, upper = 1<<10, strings.TrimSuffix(upper, "K")
	case strings.HasSuffix(upper, "M"):
		mult, upper = 1<<20, strings.TrimSuffix(upper, "M")
	case strings.HasSuffix(upper, "G"):
		mult, upper = 1<<30, strings.TrimSuffix(upper, "G")
	case strings.HasSuffix(upper, "B"):
		upper = strings.TrimSuffix(upper, "B")
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(upper), 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return int64(n * float64(mult)), true
}

// parseKernelPackages returns non-empty lines (package names).
func parseKernelPackages(out string) []string {
	var pkgs []string
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" {
			pkgs = append(pkgs, p)
		}
	}
	return pkgs
}

// kernelVersion strips the linux-image package prefix.
func kernelVersion(pkg string) string {
	v := strings.TrimPrefix(pkg, "linux-image-")
	v = strings.TrimPrefix(v, "unsigned-")
	return v
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
				total += fileDiskUsage(info)
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

func fileDiskUsage(fi os.FileInfo) int64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Blocks * 512
	}
	return fi.Size()
}

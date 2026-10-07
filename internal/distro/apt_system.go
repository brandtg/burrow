package distro

import (
	"strconv"
	"strings"

	"github.com/brandtg/burrow/internal/owner"
)

// System implements Adapter for the apt/dpkg family (Debian, Ubuntu).
func (aptAdapter) System() SystemSpec {
	return SystemSpec{
		PackageCache:         owner.AptPackageCache(),
		PackageCachePath:     "/var/cache/apt/archives",
		PackageCacheEvidence: "apt owns /var/cache/apt/archives",
		Orphans:              owner.AptOrphans(),
		OrphansProbe:         owner.AptOrphanProbe(),
		ParseOrphans:         parseAptOrphans,
		ResidualConfig:       owner.AptResidualConfig,
		ResidualProbe:        owner.DpkgListProbe(),
		ParseResidual:        parseResidualConfig,
		OldKernels:           owner.AptOldKernels(),
		KernelsProbe:         owner.DpkgKernelProbe(),
		HasOldKernels:        aptHasOldKernels,
	}
}

// parseAptOrphans extracts the "N to remove" count and estimated freed bytes
// from `apt-get -s autoremove` output.
func parseAptOrphans(out string) (int, int64, bool) {
	freed, known := parseAptFreed(out)
	return parseAptRemovals(out), freed, known
}

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

// aptHasOldKernels reports whether any installed linux-image differs from the
// running kernel.
func aptHasOldKernels(kernelsOut, running string) bool {
	runningRel := strings.TrimSpace(running)
	for _, p := range parseKernelPackages(kernelsOut) {
		if kernelVersion(p) != runningRel {
			return true
		}
	}
	return false
}

// dnf.go implements the Fedora/RHEL (dnf/rpm) adapter. It is the acceptance
// test for the adapter design: the whole family lands here plus the
// owner command table, with no change to the engine or catalog framework.
package distro

import (
	"strings"

	"github.com/brandtg/burrow/internal/owner"
)

// System implements Adapter for the dnf family (Fedora, RHEL, CentOS, Rocky,
// AlmaLinux).
func (dnfAdapter) System() SystemSpec {
	return SystemSpec{
		PackageCache:         owner.DnfPackageCache(),
		PackageCachePath:     "/var/cache/dnf",
		PackageCacheEvidence: "dnf owns /var/cache/dnf",
		Orphans:              owner.DnfOrphans(),
		OrphansProbe:         owner.DnfOrphansProbe(),
		ParseOrphans:         parseDnfOrphans,
		OldKernels:           owner.DnfOldKernels(),
		KernelsProbe:         owner.RpmKernelProbe(),
		HasOldKernels:        dnfHasOldKernels,
	}
}

func (dnfAdapter) PackageCacheAction() owner.OwnerCommand { return owner.DnfPackageCache() }
func (dnfAdapter) OrphansAction() owner.OwnerCommand      { return owner.DnfOrphans() }
func (dnfAdapter) OldKernelsAction() owner.OwnerCommand   { return owner.DnfOldKernels() }
func (dnfAdapter) ResidualConfigAction([]string) (owner.OwnerCommand, error) {
	return owner.OwnerCommand{}, errNoResidualConfig("dnf")
}

// parseDnfOrphans counts the package names emitted by `dnf repoquery
// --unneeded --qf %{name}`. dnf does not report a freed-size estimate, so the
// size stays unknown rather than being shown as zero.
func parseDnfOrphans(out string) (int, int64, bool) {
	n := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n, 0, false
}

// dnfHasOldKernels reports whether any installed `kernel-*` package differs
// from the running kernel release.
func dnfHasOldKernels(kernelsOut, running string) bool {
	runningRel := strings.TrimSpace(running)
	for _, line := range strings.Split(kernelsOut, "\n") {
		p := strings.TrimSpace(line)
		if p == "" {
			continue
		}
		if v := strings.TrimPrefix(p, "kernel-"); v != runningRel {
			return true
		}
	}
	return false
}

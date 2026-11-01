package distro

import "github.com/brandtg/burrow/internal/owner"

// SystemSpec is the distro-specific data behind the `system` catalog items.
// The system catalog drives it generically, so adding a distro
// family is a data-only change to its adapter. A zero-value
// field means the family does not implement that item yet and the catalog skips
// it rather than guessing an argv.
type SystemSpec struct {
	PackageCache         owner.OwnerCommand
	PackageCachePath     string
	PackageCacheEvidence string

	Orphans      owner.OwnerCommand
	OrphansProbe owner.OwnerCommand
	// ParseOrphans returns the count of removable packages and, when the probe
	// reports it, the estimated freed bytes.
	ParseOrphans func(out string) (count int, freed int64, freedKnown bool)

	// ResidualConfig is nil when the family has no residual-config concept.
	ResidualConfig func(pkgs []string) (owner.OwnerCommand, error)
	ResidualProbe  owner.OwnerCommand
	ParseResidual  func(out string) []string

	OldKernels    owner.OwnerCommand
	KernelsProbe  owner.OwnerCommand
	HasOldKernels func(kernelsOut, running string) bool
}

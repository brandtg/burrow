package distro

import (
	"context"
	"fmt"

	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// Container-runtime guard (spec §7.5), used by the containers catalog (plan
// 05). It fails closed: a probe that cannot prove the daemon is reachable
// returns Unknown, which stops the action.

// CheckContainerRuntime proves a container runtime's daemon is reachable. It
// runs a read-only `<binary> info` (docker or podman); it never triggers a
// pull. An unreachable daemon is Unknown, not Busy, but both stop the action.
func CheckContainerRuntime(ctx context.Context, r *owner.Runner, binary string) safe.GuardResult {
	if r == nil {
		return safe.UnknownGuard("no command runner available")
	}
	if binary == "" {
		return safe.UnknownGuard("no container runtime detected")
	}
	if _, err := r.Probe(ctx, []string{binary, "info"}, owner.TimeoutQuickDetect); err != nil {
		return safe.UnknownGuard(fmt.Sprintf("%s daemon is not reachable: %v", binary, err))
	}
	return safe.OK()
}

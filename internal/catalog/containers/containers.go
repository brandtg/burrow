// Package containers implements the `containers` cleanup category (spec §8.2):
// docker and podman pruning. Every mutating action is an allow-listed owner
// command (invariant I4); discovery and measurement are read-only. Volumes are
// treated as data, not cache: the volume and system-wide prunes are high risk,
// default OFF, and never auto-selected.
//
// Provenance: behavioral port of Mole's container-cleanup shape, reimplemented
// clean-room. No Mole source was copied.
package containers

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/brandtg/burrow/internal/action"
	"github.com/brandtg/burrow/internal/config"
	"github.com/brandtg/burrow/internal/distro"
	"github.com/brandtg/burrow/internal/engine"
	"github.com/brandtg/burrow/internal/owner"
	"github.com/brandtg/burrow/internal/safe"
)

// ID is the category identifier.
const ID = "containers"

// Category enumerates container-runtime cleanup targets.
type Category struct{}

// New returns the containers category.
func New() *Category { return &Category{} }

// ID returns the category identifier.
func (*Category) ID() string { return ID }

// Title is the human label.
func (*Category) Title() string { return "Containers" }

// PreviewNote is the category guidance shown in the preview.
func (*Category) PreviewNote() string {
	return "Container runtime caches and unused images. Owner commands; reclaimed state is re-downloadable. Volumes can hold data, not cache."
}

// Discover enumerates container targets. The docker items are only offered when
// the docker binary is present; the daemon is re-checked by a guard at
// execution so an unreachable daemon skips the item rather than failing it.
func (*Category) Discover(ctx context.Context, env *engine.Env) ([]engine.Target, error) {
	var out []engine.Target
	out = append(out, dockerItems(ctx, env)...)
	out = append(out, podmanItems(ctx, env)...)
	// containers.k8s-evicted is report-only in v0.1 and has no action, so it is
	// intentionally not a target yet: the engine requires every target to carry
	// a valid recovery contract and action.
	return out, nil
}

// Reconcile drops docker sub-prunes that a broader selected prune already
// covers, so a run never executes redundant prunes (plan 05 risk note):
// `docker system prune -a --volumes` supersedes every part, and
// `docker image prune -a` supersedes the dangling-only prune.
func (*Category) Reconcile(plan *engine.Plan) {
	if plan == nil {
		return
	}
	all := selected(plan, "containers.docker-all")
	unused := all || selected(plan, "containers.docker-unused")
	superseded := map[string]bool{}
	if all {
		superseded["containers.docker-build-cache"] = true
		superseded["containers.docker-unused"] = true
		superseded["containers.docker-volumes"] = true
	}
	if unused {
		superseded["containers.docker-dangling"] = true
	}
	for _, it := range plan.Items {
		if superseded[it.Target.ID] {
			it.Selected = false
		}
	}
}

func selected(plan *engine.Plan, id string) bool {
	for _, it := range plan.Items {
		if it.Target.ID == id && it.Selected {
			return true
		}
	}
	return false
}

// runtimeGuard fails closed when the runtime daemon cannot be proven reachable.
type runtimeGuard struct{ binary string }

func (g runtimeGuard) Check(ctx context.Context, env *engine.Env) error {
	if env == nil {
		return safe.UnknownGuard("no environment").Err()
	}
	return distro.CheckContainerRuntime(ctx, env.Run, g.binary).Err()
}

func dockerItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil || !owner.Available("docker") {
		return nil
	}
	df, dfKnown := dockerDFFor(ctx, env.Run)
	evidence := []string{"docker is installed; the daemon is re-checked before pruning"}
	if dfKnown {
		evidence = append(evidence, dockerEvidence(df)...)
	}
	if dockerBuildRunning() {
		evidence = append(evidence, "a docker build is in progress")
	}
	guard := runtimeGuard{binary: "docker"}
	total, totalKnown := df.reclaimableTotal()

	return []engine.Target{
		newOwnerTarget(env, owner.DockerBuilderPrune(), "docker build cache", "", df.buildCache, df.buildCacheKnown, guard, engine.RiskLow, true, evidence),
		// `docker system df` cannot separate dangling images from other unused
		// images, so the dangling prune's saving is left unknown rather than
		// overstated with the whole images figure.
		newOwnerTarget(env, owner.DockerImagePrune(), "dangling images", "", 0, false, guard, engine.RiskLow, true, evidence),
		newOwnerTarget(env, owner.DockerImagePruneAll(), "unused images", "", df.images, df.imagesKnown, guard, engine.RiskMed, false, evidence),
		newOwnerTarget(env, owner.DockerVolumePrune(), "unused volumes", "", df.volumes, df.volumesKnown, guard, engine.RiskHigh, false, evidence),
		newOwnerTarget(env, owner.DockerSystemPruneAll(), "all docker state (images, volumes, cache)", "", total, totalKnown, guard, engine.RiskHigh, false, evidence),
	}
}

func podmanItems(ctx context.Context, env *engine.Env) []engine.Target {
	if env == nil || env.Run == nil || !owner.Available("podman") {
		return nil
	}
	guard := runtimeGuard{binary: "podman"}
	cmd := owner.PodmanSystemPrune()
	return []engine.Target{
		newOwnerTarget(env, cmd, "podman system prune", "", 0, false, guard, engine.RiskMed, false,
			[]string{"podman is present; prune removes unused images, containers and networks"}),
	}
}

func newOwnerTarget(env *engine.Env, cmd owner.OwnerCommand, display, path string, size int64, sizeKnown bool, guard engine.Guard, risk engine.Risk, def bool, evidence []string) engine.Target {
	return engine.Target{
		ID:        cmd.ID,
		Category:  ID,
		Display:   display,
		Path:      path,
		SizeBytes: size,
		SizeKnown: sizeKnown,
		Action:    action.NewOwnerCommand(cmd, env.Run),
		Recovery:  engine.RecoveryOwner,
		NeedsRoot: cmd.NeedsRoot,
		Guard:     guard,
		Risk:      risk,
		DefaultOn: def,
		Evidence:  evidence,
	}
}

func dockerDFFor(ctx context.Context, r *owner.Runner) (dockerDF, bool) {
	out, err := r.Probe(ctx, owner.DockerSystemDFProbe().Argv, owner.TimeoutSizeProbe)
	if err != nil {
		return dockerDF{}, false
	}
	return parseDockerDF(out), true
}

func dockerEvidence(d dockerDF) []string {
	var parts []string
	if d.imagesKnown {
		parts = append(parts, "images "+config.FormatBytes(d.images))
	}
	if d.buildCacheKnown {
		parts = append(parts, "build cache "+config.FormatBytes(d.buildCache))
	}
	if d.volumesKnown {
		parts = append(parts, "volumes "+config.FormatBytes(d.volumes))
	}
	if d.containersKnown {
		parts = append(parts, "containers "+config.FormatBytes(d.containers))
	}
	if len(parts) == 0 {
		return nil
	}
	return []string{"docker system df reclaimable: " + strings.Join(parts, ", ")}
}

// dockerDF is the parsed `docker system df` breakdown. Each row's reclaimable
// column is the space the corresponding prune would free.
type dockerDF struct {
	images, buildCache, volumes, containers                     int64
	imagesKnown, buildCacheKnown, volumesKnown, containersKnown bool
}

// reclaimableTotal sums every known category; it approximates what
// `docker system prune -a --volumes` frees.
func (d dockerDF) reclaimableTotal() (int64, bool) {
	var total int64
	known := false
	add := func(v int64, k bool) {
		if k {
			total += v
			known = true
		}
	}
	add(d.images, d.imagesKnown)
	add(d.buildCache, d.buildCacheKnown)
	add(d.volumes, d.volumesKnown)
	add(d.containers, d.containersKnown)
	return total, known
}

// parseDockerDF parses the tabular `docker system df` output. It finds the
// first integer column (TOTAL) to split the possibly multi-word type label from
// the numeric columns, then reads the RECLAIMABLE column.
func parseDockerDF(out string) dockerDF {
	var d dockerDF
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		idx := -1
		for i, f := range fields {
			if isAllDigits(f) {
				idx = i
				break
			}
		}
		if idx <= 0 || idx+3 >= len(fields) {
			continue
		}
		typ := strings.ToLower(strings.Join(fields[:idx], " "))
		size, ok := parseDockerSize(fields[idx+3])
		if !ok {
			continue
		}
		switch typ {
		case "images":
			d.images, d.imagesKnown = size, true
		case "build cache":
			d.buildCache, d.buildCacheKnown = size, true
		case "local volumes", "volumes":
			d.volumes, d.volumesKnown = size, true
		case "containers":
			d.containers, d.containersKnown = size, true
		}
	}
	return d
}

// dockerUnits are docker's decimal (1000-based) size suffixes.
var dockerUnits = map[string]float64{
	"B":  1,
	"kB": 1e3, "KB": 1e3,
	"MB": 1e6,
	"GB": 1e9,
	"TB": 1e12,
	"PB": 1e15,
}

// parseDockerSize parses a docker size string such as "1.2GB" or "800MB".
func parseDockerSize(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" || s == "N/A" {
		return 0, false
	}
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	num, err := strconv.ParseFloat(s[:i], 64)
	if err != nil {
		return 0, false
	}
	unit := s[i:]
	mult, ok := dockerUnits[unit]
	if !ok {
		if unit != "" {
			return 0, false
		}
		mult = 1
	}
	return int64(num * mult), true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// dockerBuildRunning reports whether a docker client build is in progress. It
// is read-only and best-effort: a build does not necessarily make the prune
// unsafe, but it is recorded in the item's evidence.
func dockerBuildRunning() bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, de := range entries {
		if !de.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(de.Name()); err != nil {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", de.Name(), "comm"))
		if err != nil || strings.TrimSpace(string(comm)) != "docker" {
			continue
		}
		args, err := os.ReadFile(filepath.Join("/proc", de.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if bytes.Contains(args, []byte("build")) {
			return true
		}
	}
	return false
}

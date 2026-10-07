# Plan 05 — `containers` + `flatpak`/`snap`

**Status:** in review
**Milestone:** M4
**Depends on:** 03
**Blocks:** 07
**Parallel with:** 04, 06

## Goal

Add container-runtime pruning and desktop package cleanup (flatpak/snap). These
are high-value, high-variance categories: the actions are owner commands, sizing
comes from runtime introspection, and several items are destructive enough that
they must default OFF and never auto-select.

## Deliverables

```
internal/catalog/containers/   docker/podman items (spec §8.2)
internal/catalog/desktoppkgs/  flatpak/snap items (spec §8.3)
internal/owner/commands.go     docker/podman/snap/flatpak argv tables
internal/distro/ (guards)      container runtime + snapd guards
testdata/containers/           stubbed runtime fixtures + golden plans
```

## Tasks

### 1. `containers` category (spec §8.2)
- Detect `docker` present + daemon reachable; size via `docker system df` and show
  the breakdown. Only run the prune the user picked.
- Items:

| Item | Action argv | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `containers.docker-build-cache` | `docker builder prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-dangling` | `docker image prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-unused` | `docker image prune -a -f` | owner | maybe | daemon up | med | off |
| `containers.docker-volumes` | `docker volume prune -f` | owner | maybe | daemon up | high | off |
| `containers.docker-all` | `docker system prune -a --volumes -f` | owner | maybe | daemon up | high | off |
| `containers.podman` | `podman system prune -f` (+ `--volumes` if opted) | owner | maybe | — | med | off |
| `containers.k8s-evicted` | report only in v0.1 | — | no | — | — | off |

- Guard: daemon reachability + a `docker build` in progress is recorded (the prune
  may still be fine, but record it; fail closed on unknown daemon state).
- `containers.docker-volumes` and `containers.docker-all` are high risk: red,
  default OFF, never auto-selected.

### 2. `desktop-pkgs` category (spec §8.3)
- Items:

| Item | Action | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `flatpak.unused` | `flatpak uninstall --unused -y` | owner | per-user | — | low | on |
| `flatpak.repair` | `flatpak repair` | owner | maybe | — | low | off |
| `flatpak.userdata` | Trash `~/.var/app/<id>` dirs with no installed app | trash | no | app not installed | med | off |
| `snap.old-revisions` | `snap remove --purge <name> --revision <rev>` per disabled | owner | yes | — | med | on |
| `snap.cache` | delete `/var/lib/snapd/cache/*` | owner | yes | snapd idle | low | on |
| `snap.userdata` | Trash `~/snap/<name>` for uninstalled snaps | trash | no | not installed | med | off |

- Detect disabled snap revisions via `snap list --all`; each disabled revision is
  its own row so the user can pick.
- `flatpak uninstall --unused` runs per-user (never root); `snap` commands that
  mutate system state run as root via sudo.
- `flatpak.userdata` / `snap.userdata` must confirm the app is actually absent
  before trashing the data dir; unknown => skip.

### 3. Sizing
- Docker: parse `docker system df` output; show per-category breakdown.
- Snap: `du` of `/var/lib/snapd/cache` and revision dirs under the 30s size-probe
  timeout; unknown on timeout.
- Flatpak: `flatpak list` + `du` of unused app dirs.

### 4. Guards and elevation
- Container runtime guard added to the guard framework from Plan 03.
- snapd idle check before `snap.cache`.
- Never run per-user flatpak as root.

## Acceptance criteria

- Stubbed `docker`/`snap`/`flatpak` tests produce exactly the planned argv; high
  risk items are default OFF and never auto-selected.
- Daemon-unreachable fixture -> container items skipped with reason, not failed.
- `snap.old-revisions` produces one row per disabled revision with the right
  `--revision`.
- `flatpak.userdata`/`snap.userdata` skip when app state is unknown.
- `--dry-run` mutates nothing; `docker system df` sizing rendered.
- `scripts/check.sh` green.

## Risks & notes

- **Volumes are data, not cache.** `docker volume prune` can destroy databases.
  Keep it high-risk, default OFF, with an explicit warning in the preview.
- **`docker system prune -a` is a superset** of the other docker items; if both
  are selected, avoid running redundant prunes (order/de-dup in the plan).
- **Daemon detection must be side-effect-free.** `docker info`/`system df` are
  read-only; never trigger a pull.
- **snap/flatpak absence is normal**; items simply do not appear when the tool is
  not installed.

## Provenance

Owner-command lists cross-checked against spec §21.1. Behavioral port of Mole's
container/desktop cleanup shape, reimplemented clean-room. No Mole source copied.

---

## Implementation notes (Plan 05)

Recorded after landing the plan. Acceptance criteria pass and `scripts/check.sh`
is green (golangci-lint and govulncheck warn as not installed on the authoring
host, matching Plans 01/02).

### Docker sizing

- Discovery runs `docker system df` once (read-only) and parses the table by
  locating the first integer column (TOTAL) to split the possibly multi-word
  type label, then reading the RECLAIMABLE column. `parseDockerSize` handles
  docker's decimal (1000-based) suffixes.
- Per-item sizing: build cache from the `Build Cache` row; unused images from
  `Images`; volumes from `Local Volumes`; `containers.docker-all` sums every
  known category. `containers.docker-dangling` is left `SizeKnown=false`:
  `docker system df` cannot isolate dangling images from other unused images, so
  showing the whole `Images` figure would overstate the saving (spec §8.8, "no
  fake savings").
- The per-category breakdown is recorded in each docker target's `Evidence`
  (`docker system df reclaimable: ...`). A running `docker build` is detected
  read-only and appended to the evidence; it does not block the prune.

### Reconciler hook

- `internal/catalog/catalog.go` defines an optional `Reconciler` interface
  (`Reconcile(*engine.Plan)`). `cli/clean.go` runs it for every selected category
  after `Build` and again after an interactive selection, so a superset item
  suppresses its parts in both the preview and the executed plan.
- `containers` implements it: `docker system prune -a --volumes` supersedes the
  build-cache, dangling, unused and volume prunes, and `docker image prune -a`
  supersedes the dangling-only prune.

### Guards and fail-closed behavior

- `internal/distro.CheckContainerRuntime` probes `<binary> info` (docker/podman)
  read-only. An unreachable daemon or failed probe returns `Unknown`, so the
  item is skipped with a recorded reason rather than failing or escalating.
- User-data items re-check app absence at execution via `appAbsentGuard`: if the
  flatpak/snap inventory is unknown, or the app is now installed, the trash is
  stopped (fail closed).
- The snapd-idle guard (`SnapdProbe`/`CheckSnapdIdle`) was prototyped for
  `snap.cache` but removed with that item (see known gaps); it is not present in
  the landed code.

### User-data items

- `flatpak.userdata` (`~/.var/app/<id>`) and `snap.userdata` (`~/snap/<name>`)
  are trash-contract targets. Each is validated against an allow-listed root
  (`~/.var/app` / `~/snap`), ownership-checked, and snapshotted at discovery;
  `TrashAction` re-validates and re-checks the snapshot at the sink.
- Discovery only offers a directory when the installed-app inventory is known
  and does not contain the app; unknown inventory means no item is offered.

### Deviations and judgement calls

- **snap.cache dropped from v0.1.** The plan/spec item deletes
  `/var/lib/snapd/cache/*`, but snapd exposes no owner command for it. The first
  implementation shelled out a fixed `find ... -delete` under sudo, which
  bypasses `internal/safe` (no validation, no snapshot) and is exactly the
  generic delete primitive AGENTS.md forbids (I3, D7). It was removed rather
  than shipped. Reinstating it requires a validated, allow-listed primitive in
  `internal/safe` (Validator confined to the cache root + discovery-time
  snapshot), which is a separate change because `safe.RemoveTree` is
  deliberately purge-only.
- **k8s-evicted omitted.** It is report-only in v0.1 and the engine requires
  every target to carry a valid recovery contract and action; it is not a target
  yet.
- **flatpak.repair is `NeedsRoot=true`.** It repairs the system flatpak
  installation; a per-user repair is a separate item if it is ever wanted.
- **Docker/podman run unelevated.** They are treated as per-user (rootless or
  the docker group). If the socket is root-only the runtime guard skips the item
  with a reason instead of silently escalating.

### Known gaps

- snap revision sizing is wired through the generic `engine.Measure` path
  (`<name>_<rev>.snap` stat); it is covered by a test with a fixture file. There
  is no explicit `du` under a dedicated timeout beyond the shared size-probe
  path.
- snap cache sizing and cleanup are not implemented (see above).
- **Per-user commands do not drop privileges.** Carried from Plan 04: the owner
  runner executes per-user commands as the current process user, so under
  `sudo burrow` (SUDO_USER set) `flatpak uninstall --unused` would run as root.
  This is an `internal/owner/runner.go` concern, not a catalog one, and is
  tracked for the owner-package fix.

### Gate hardening

- The static gate now also flags delete-capable external commands in owner argv
  tables (`rm`, `shred`, `dd`, `truncate`, `unlink`, `wipefs`, `mkfs`, `srm`,
  `find -delete/-exec`, `xargs rm`, and `sh -c`). It inspects composite-literal
  `Argv` fields with fully literal string elements, in every package including
  `internal/owner`; the AST sink gate structurally cannot see inside a shelled
  command. Planted-violation tests cover `rm`, `find -delete`, and `sh -c`, plus
  a safe-argv control.
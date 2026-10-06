# Plan 05 — `containers` + `flatpak`/`snap`

**Status:** not started
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
# burrow

`burrow` (alias `bw`) is a Linux-native, terminal-first disk tool that does three
things:

1. **Analyze** — show where space went with a fast, interactive TUI.
2. **Clean** — reclaim space prescriptively by driving the *owner* of each kind of
   junk (package manager, journald, container runtime, flatpak/snap, language
   toolchains), with a mandatory preview, dry-run, whitelist, and an undo log.
3. **Purge** — delete rebuildable project artifacts (`node_modules`, `target`,
   virtualenvs, `__pycache__`, ...) with git-tracked-file protection.

It is an **orchestrator with a safety contract**, not a path-deleter.

> Status: v0.1 complete. Plans 01–07 have landed: the safety core and engine,
> analyze, clean (`system`/`dev`/`user-cache`/`downloads`/`containers`/
> `desktop-pkgs`), purge, the thin `status` snapshot, the Fedora (dnf) adapter,
> `--json` everywhere, shell completion, generated man pages, and
> checksum-verified release packaging. See [Development](#development) to build
> and test, and [Packaging and release](#packaging-and-release) to cut a release.

## Quickstart

```sh
# Build from source (or see Packaging and release for prebuilt binaries)
make build

# See what is reclaimable in the read-only TUI, without touching anything
./bin/burrow clean --dry-run

# Non-interactive (for scripts/CI): plain text, or --json for machine output
./bin/burrow clean --dry-run --plain

# Explore where your space went
./bin/burrow analyze ~

# Clean interactively: preview -> review -> confirm -> execute
./bin/burrow clean

# Reclaim rebuildable project artifacts
./bin/burrow purge --paths ~/Projects
```

`clean` and `purge` never mutate anything before the preview step, and every
mutation declares a recovery contract (see [Safety invariants](#safety-invariants)).
Undo a recoverable clean with `burrow history --restore <run_id>`.

## Why this exists

Linux users who want "understand my disk + clean it up safely" must assemble
`ncdu`/`gdu`/`dust` for analysis and then remember which of ~20 owner commands is
safe and what each reclaims (`apt clean`, `journalctl --vacuum`,
`docker system prune`, `flatpak uninstall --unused`, ...). Existing integrated
cleaners (BleachBit, Stacer, KDE Sweeper) are GUI-first and shallow on developer
and package-manager caches.

The missing product is the **integration plus the safety contract**, not another
scanner and not another `rm` script. `burrow` is the shape of
[tw93/Mole](https://github.com/tw93/mole) — a prescriptive, previewable cleaner —
implemented independently for Linux, over Linux's own commands.

## The central idea: delegate, never guess

On macOS, a cleaner can guess paths under `~/Library`. On Linux, the safe answer
is to let the **owner** of each kind of state clean it up: `apt` owns
`/var/cache/apt`, `journald` owns its journal, `docker` owns its images, `npm`
owns its cache. `burrow` never hand-deletes package-manager or OS state.

Every action therefore has a **recovery contract**, and the UI always states which
one applies:

- **`trash`** — move to the freedesktop Trash. Recoverable. Used for user caches,
  downloads, installers, and analyze deletions.
- **`owner`** — invoke the owner command. Permanent but re-downloadable /
  rebuildable. Used for package caches, journald, container prune, flatpak/snap,
  and language caches.

If a required condition cannot be proven (identity changed, owner is busy, Trash
unavailable, command timed out), `burrow` **skips that item and records why**. It
never silently downgrades to a permanent delete.

## Command surface

```
burrow analyze [path...]      # interactive disk explorer (read-only except 'd' trash)
burrow clean                  # categorized cleaner (preview -> confirm -> execute)
burrow purge                  # rebuildable project artifacts (node_modules, target, ...)
burrow status                 # one-shot system snapshot (thin)
burrow history                # read the operation journal; --restore <run_id>
burrow config                 # print effective config + resolved paths
burrow completion <shell>     # shell completion (bash/zsh/fish)
burrow docs                   # generate man pages into docs/man
burrow version
```

Global flags include `--dry-run`, `-y/--yes`, `--json`, `--plain`, `--only`,
`--exclude`, `--min-size`, `--config`, and `--log-level`. `clean` is TUI-first on
a terminal (including `--dry-run`, which is read-only); `--plain`, `--json`,
`--yes`, or a non-TTY stdout select the non-interactive path. Exit codes are
meaningful: `75` means a safety guard stopped an action, `124` a subprocess
timeout, `130` SIGINT.

## Safety invariants

These are the reason to trust the tool; every one has a test that fails if it is
violated (`internal/safe`, `safety_test.go`).

- **I1** — No mutation before preview. Discover/measure/filter/plan are read-only.
- **I2** — Every mutation declares a recovery contract (`trash` or `owner`),
  shown in preview and recorded in the journal.
- **I3** — No path is deleted unless it is inside an allow-listed cache root or
  produced by an owner command. There is no "delete this arbitrary path"
  primitive.
- **I4** — Owner commands are allow-listed by exact argv from the catalog; no
  dynamic command construction from user input.
- **I5** — Fail closed. Any uncertainty skips the item with a reason.
- **I6** — Never run the whole process as root. Elevate per-command via `sudo`,
  resolving the invoking user's `HOME`/XDG via `SUDO_USER`.
- **I7** — Privileged operations never traverse a user-mutable ancestor.

See `docs/burrow-spec.md` §7 for the full contract, including path validation and
identity snapshots (TOCTOU defense).

## Cleanup catalog (v0.1)

The `system` category on Debian/Ubuntu (apt/dpkg). Every mutating row is an
allow-listed owner command; discovery and measurement are read-only. High-risk
rows are always shown but never auto-selected.

| Item | Owner command | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `system.pkg-cache` | `apt-get clean` | owner | yes | apt/dpkg idle | low | on |
| `system.orphans` | `apt-get autoremove --purge` | owner | yes | apt idle | high | off |
| `system.residual-config` | `dpkg --purge <pkgs>` (pkgs from `dpkg -l` `^rc`) | owner | yes | dpkg idle | med | off |
| `system.old-kernels` | `apt-get autoremove --purge` | owner | yes | apt idle | high | off |
| `system.journald` | `journalctl --vacuum-size=<cfg>` (default 500M) | owner | yes | — | low | on |
| `system.rotated-logs` | `systemd-tmpfiles --clean` | owner | yes | — | med | off |
| `system.tmp` | `systemd-tmpfiles --clean` | owner | yes | — | low | off |
| `system.core-dumps` | trash user-owned core files | trash | no | — | low | off |
| `system.crash` | `systemd-tmpfiles --clean` | owner | yes | — | low | off |

Other distros are detected behind the `internal/distro` adapter interface. The
Fedora/RHEL family (dnf/rpm) is implemented (Plan 07); pacman/zypper/apk remain
detection stubs. The dnf package rows are:

| Item | Owner command | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `system.pkg-cache` (dnf) | `dnf clean all` | owner | yes | dnf idle | low | on |
| `system.orphans` (dnf) | `dnf autoremove -y` | owner | yes | dnf idle | high | off |
| `system.old-kernels` (dnf) | `package-cleanup --oldkernels --count=2` | owner | yes | dnf idle | high | off |

dnf has no residual-config concept, so `system.residual-config` is apt-only. The
journal, log, temp, and core-dump rows are shared across families.

### `containers` — docker and podman

Container pruning is an owner command. Volumes can hold data, not just cache, so
the volume and system-wide prunes are high risk and default OFF.

| Item | Owner command | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `containers.docker-build-cache` | `docker builder prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-dangling` | `docker image prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-unused` | `docker image prune -a -f` | owner | maybe | daemon up | med | off |
| `containers.docker-volumes` | `docker volume prune -f` | owner | maybe | daemon up | high | off |
| `containers.docker-all` | `docker system prune -a --volumes -f` | owner | maybe | daemon up | high | off |
| `containers.podman` | `podman system prune -f` | owner | maybe | podman up | med | off |

`containers.k8s-evicted` is report-only in v0.1 and has no action.

### `desktop-pkgs` — flatpak and snap

| Item | Action | Recovery | Root | Guard | Risk | Default |
|---|---|---|---|---|---|---|
| `flatpak.unused` | `flatpak uninstall --unused -y` | owner | per-user | — | low | on |
| `flatpak.repair` | `flatpak repair` | owner | maybe | — | low | off |
| `flatpak.userdata` | trash `~/.var/app/<id>` | trash | no | app not installed | med | off |
| `snap.old-revisions` | `snap remove --purge <name> --revision <rev>` | owner | yes | — | med | on |
| `snap.userdata` | trash `~/snap/<name>` | trash | no | not installed | med | off |

### `dev` — language/toolchain caches

Each item prefers its owner command and falls back to trashing a documented
cache root only when the owner command is unavailable. Owner caches are
per-user: they are resolved by asking the owner (`npm config get cache`,
`go env GOMODCACHE`, ...) immediately before acting and are never elevated.
Every dev item is low risk and default ON.

| Item | Action | Recovery | Guard | Risk | Default |
|---|---|---|---|---|---|
| `dev.npm` | `npm cache clean --force` | owner | npm idle | low | on |
| `dev.pnpm` | `pnpm store prune` | owner | pnpm idle | low | on |
| `dev.yarn` | trash `~/.yarn/cache`, `$XDG_CACHE_HOME/yarn` | trash | — | low | on |
| `dev.bun` | `bun pm cache rm` | owner | — | low | on |
| `dev.go-mod` | `go clean -modcache` | owner | go/gopls idle | low | on |
| `dev.go-build` | `go clean -cache` | owner | — | low | on |
| `dev.rust-registry-cache` | trash `$CARGO_HOME/registry/cache` (keep `registry/src`, `registry/index`, `git`, `bin`, `toolchains`) | trash | cargo/rustc idle | low | on |
| `dev.rustup-downloads` | trash `$RUSTUP_HOME/downloads` | trash | — | low | on |
| `dev.pip` | `pip3 cache purge` | owner | — | low | on |
| `dev.uv` | `uv cache prune` | owner | — | low | on |
| `dev.poetry.*` | trash `$XDG_CACHE_HOME/pypoetry/{artifacts,cache}` (keep `virtualenvs`) | trash | — | low | on |
| `dev.conda` | `conda clean --yes --index-cache --tarballs --logfiles` | owner | — | low | on |
| `dev.gem*` | trash `~/.gem/ruby/*/cache`, `~/.bundle/cache` | trash | — | low | on |
| `dev.rbenv` | trash `~/.rbenv/cache` | trash | — | low | on |
| `dev.cpan-build` | trash `~/.cpan/build` (keep `~/.cpan/sources`) | trash | — | low | on |
| `dev.nix` | `nix-collect-garbage --delete-older-than 30d` | owner | — | low | on |
| `dev.corepack` | `corepack cache clean` | owner | — | low | on |
| `dev.mise` | `mise cache clear` | owner | — | low | on |
| `dev.frontend-caches.*` | trash `$XDG_CACHE_HOME/{typescript,electron,node-gyp,vite,webpack,eslint,prettier,parcel-cache}` | trash | — | low | on |
| `dev.pycache.*` | trash `__pycache__` under the purge roots | trash | git-tracked check | low | on |
| `dev.docker-buildx` | trash `~/.docker/buildx/cache` | trash | — | low | on |
| `dev.cloud-cli.*` | trash `~/.kube/cache`, `~/.aws/cli/cache`, gcloud/Azure logs | trash | — | low | on |
| `dev.gh-cli` | `gh config clear-cache` | owner | — | low | on |

The Rust registry cache and rustup downloads are trashed (there is no cargo
owner command for them), so they carry the `trash` recovery contract rather than
the `owner` contract shown in spec §8.4. Keep rules (Cargo `registry/src`,
Poetry `virtualenvs`, CPAN `sources`) are asserted by tests.

### `user-cache` — XDG cache sweep

| Item | Action | Recovery | Risk | Default |
|---|---|---|---|---|
| `user-cache.app-caches.*` | trash each top-level `$XDG_CACHE_HOME` entry not owned by a dev item | trash | low | on |
| `user-cache.thumbnails` | trash `$XDG_CACHE_HOME/thumbnails` | trash | low | on |
| `user-cache.trash` | empty `$XDG_DATA_HOME/Trash` (irreversible) | owner | high | off |

A whitelisted path protects itself and its parent from the sweep, and a small
protected set (`thumbnails`, `burrow`, `dconf`, `keyrings`, `gvfs`, `pulse`,
`systemd`) is never swept because it holds settings or runtime state rather than
regenerable data.

### `downloads` — installer/content sweep

| Item | Action | Recovery | Risk | Default |
|---|---|---|---|---|
| `downloads.installers.*` | trash `~/` and `~/Downloads` installers older than `[downloads] installer_min_age` (default 30d) | trash | low | on |
| `downloads.old.*` | trash `~/Downloads` entries older than `old_min_age` (90d) and larger than `old_min_size` (100M) | trash | med | off |

### `purge` — rebuildable project artifacts (separate command)

`purge` is a distinct command, not a `clean` category. It permanently deletes
rebuildable build artifacts under the configured project roots, with git-tracked
protection:

| Artifact | Detection | Recovery | Guard | Default |
|---|---|---|---|---|
| `node_modules` | under a `[purge] paths` root | permanent (rebuildable) | git-tracked/nested-repo check, age filter | off |
| `target` | same | permanent (rebuildable) | git-tracked check, age filter | off |
| `.venv`, `venv` | same | permanent (rebuildable) | git-tracked check, age filter | off |
| `__pycache__` | same | permanent (rebuildable) | git-tracked check, age filter | off |
| `.next/cache`, `build`, `dist`, `.gradle`, `_cacache` | same | permanent (rebuildable) | git-tracked check, age filter | off |

The artifact list is pinned and extensible via `[purge] artifact_dirs`. A
candidate inside a git repo that is tracked (or whose git state is unknown) is
kept. See `docs/user-guide.md` and `burrow purge --paths`.

### Whitelist manager

`burrow clean --whitelist` runs an interactive add/remove loop against
`$XDG_CONFIG_HOME/burrow/whitelist`; comments and blank lines are preserved.
Non-interactively it prints the current entries.

## Architecture

```
cmd/burrow/         main() -> cobra root
internal/cli/       cobra commands, flag wiring, output formatting
internal/engine/    run pipeline: discover -> plan -> preview -> execute -> journal
internal/catalog/   cleanup catalog: category/item registries (data + small logic)
internal/action/    Action interface + TrashAction, OwnerCommandAction
internal/owner/     owner-command runners, timeouts, per-distro command tables
internal/distro/    distro detection + adapter interface (apt + dnf)
internal/trash/     native freedesktop Trash Spec writer (put/list/restore/empty)
internal/safe/      path validation, identity snapshot, guards, fail-closed helpers
internal/journal/   append-only JSONL operation journal + history reader
internal/whitelist/ whitelist load/match
internal/config/    TOML load + precedence + defaults
internal/scan/      gdu adapter (wraps pkg/analyze + pkg/fs) + our item model
internal/tui/       Bubble Tea models (analyze, clean preview, purge)
internal/metrics/   gopsutil wrappers (status; thin)
internal/i18n/      message catalog (EN only in v0.1; keys so it is translatable)
```

The pipeline invariant: steps from discovery through planning are pure and
read-only. Nothing mutates until the execute step, and `--dry-run` stops after the
preview.

## Key technology decisions

- **Go**, single static binary (`CGO_ENABLED=0`).
- **Scan engine:** [`github.com/dundee/gdu/v5`](https://github.com/dundee/gdu)
  (MIT), wrapped behind `internal/scan`. See `AGENTS.md` for the integration
  caveats.
- **TUI:** Bubble Tea + Lipgloss + Bubbles.
- **CLI:** spf13/cobra (shell completion + `cobra/doc` man-page generation).
- **Status metrics:** [`github.com/shirou/gopsutil/v4`](https://github.com/shirou/gopsutil),
  wrapped behind `internal/metrics` (thin, read-only).
- **Config:** TOML; whitelist is a separate newline-delimited file.
- **Trash:** native freedesktop Trash Spec implementation, no runtime dependency.
- **Packaging:** goreleaser (`CGO_ENABLED=0`, linux amd64/arm64, checksums, native
  packages) plus a checksum-verified `install.sh`.
- **v0.1 target:** Debian/Ubuntu + Fedora/RHEL + systemd, with a distro-adapter
  interface (`internal/distro`) designed so pacman/zypper/apk are data-only
  additions.

## Non-goals

Not a privacy cleaner (no cookie/password/shred/wipe). Not a package manager. Not
a system optimizer. No GUI. No background daemon or scheduler. No
distro-universal v0.1.

## Implementation plans

Work is broken into dependency-ordered plans in [`docs/plans/`](docs/plans/).
Start with [`docs/plans/README.md`](docs/plans/README.md) for the dependency graph,
execution waves, and per-plan ownership map. All seven plans have landed; the
audit, deviations, known gaps, and open findings are in
[`docs/status.md`](docs/status.md).

```
01 foundation + safety core  (blocking)
   ├─ 02 analyze                 ├─ 06 purge
   └─ 03 clean engine + system/apt
        ├─ 04 dev + user-cache + downloads
        └─ 05 containers + flatpak/snap
07 polish + status + dnf + packaging  (depends on 02-06)
```

## Development

Common tasks are wrapped in the `Makefile`. Run `make` (or `make help`) to list
every target.

### Prerequisites

- Go is pinned by `go.mod`: `go 1.26.0` with `toolchain go1.26.8`. With
  `GOTOOLCHAIN=auto` (the default), the first build downloads the exact toolchain,
  so the developer's ambient Go version does not matter. To avoid the download,
  pre-provision Go 1.26.8 and keep `GOTOOLCHAIN=local`.
- Builds are static: the Makefile exports `CGO_ENABLED=0`. The binary must not
  link libc.
- Optional gate tools: `golangci-lint` and `govulncheck`. Their targets skip with
  a warning when the tools are absent.
- `make integration` needs root plus `losetup`/`mkfs.ext4`/`mount`.

### Build and run

```sh
make build                       # static CLI -> bin/burrow
make build-all                   # compile every package
make run ARGS="version"          # run from source
make run ARGS="config"
make install                     # install into GOBIN/GOPATH
```

`make build`/`make install` stamp the version from `git describe`; override with
`make build VERSION=0.1.0`.

### Test

```sh
make test                        # unit + safety tests
make test-race                   # race detector
make fuzz                        # fuzz the path validator (30s)
make fuzz FUZZTIME=2m            # longer run
```

The safety regression suite (invariants I1–I7) lives in
`internal/safe/safety_test.go`; the static gate has its own tests in
`scripts/safety-gate/check_test.go`.

### Format, vet, lint

```sh
make fmt                         # format in place
make fmt-check                   # fail if anything needs formatting
make vet
make lint                        # golangci-lint (skips if not installed)
make vuln                        # govulncheck (skips if not installed)
```

### The gate

`make gate` runs `scripts/check.sh`: `gofmt`, `go vet`, `go test`, and the static
"no direct destructive sink" gate. `golangci-lint` and `govulncheck` run when
installed and are otherwise skipped with a warning.

```sh
make gate                        # canonical gate
make gate-strict                 # fail if lint/vuln tools are missing
make safety-gate                 # only the static destructive-sink gate
```

### Integration (cross-device trash)

Requires root; creates a loopback ext4 mount under `mktemp` and runs the
cross-device trash test. It never touches a real host path.

```sh
sudo make integration
```

### Container matrix

Runs the binary in `ubuntu:24.04`, `debian:12`, and `fedora:41` with the
`testbin/` stubs first on PATH, asserting the planned/executed owner argv,
dry-run purity, guard trips, whitelist exclusion, and exit codes. Requires
docker or podman; it never touches a real host path.

```sh
make matrix                      # requires a container runtime
BURROW_MATRIX=1 make gate        # gate form: skips with a reason if no runtime
```

CI (`.github/workflows/ci.yml`) runs `make gate-strict` and `make matrix`.

### Repository layout

See [Architecture](#architecture) for the package map. Package ownership and the
dependency graph are in `docs/plans/README.md`; agent working notes and the
non-negotiable safety rules are in `AGENTS.md`.

### Man pages and completion

```sh
make docs                        # regenerate docs/man (review the diff)
make run ARGS="completion bash"  # bash/zsh/fish completion scripts
```

Man pages are generated from the cobra command tree; never hand-edit them. The
gate regenerates them into a temp dir and fails if the committed pages are stale.

## Packaging and release

Releases are built by [goreleaser](https://goreleaser.com) from
`.goreleaser.yaml`: static linux amd64/arm64 binaries (`CGO_ENABLED=0`), tar.gz
archives, `.deb`/`.rpm`/`.apk`/`.pkg.tar.zst` packages, and a `checksums.txt`.
The version is stamped through `internal/journal.Version` via
`-ldflags -X`. goreleaser provisions the Go toolchain pinned in `go.mod`.

```sh
goreleaser release --clean       # build archives, packages, and checksums
make verify-static               # assert the binary has no dynamic loader deps
```

`install.sh` is checksum-verified and fail-closed: it downloads the archive and
`checksums.txt`, verifies SHA-256, and installs only on a match. A missing
checksum entry, a download failure, or a mismatch aborts without installing.

```sh
BURROW_VERSION=0.1.0 ./install.sh            # pin a version
BURROW_BINDIR=~/.local/bin ./install.sh      # install somewhere writable
```

See `docs/user-guide.md` for the day-to-day workflow (dry-run, whitelist,
preview/review, journal/undo, config).

## Reference documents

- `docs/burrow-spec.md` — the full product spec and locked decisions (ADRs).
- `docs/user-guide.md` — the user guide.
- `docs/man/` — generated man pages.
- `docs/mole-linux-portability.md` — why Mole cannot be ported and what a Linux
  build should reuse.
- `AGENTS.md` — working notes for coding agents.

## License

Apache-2.0. `burrow` is an independent implementation; it does not copy Mole's
GPL-3.0 source. See `AGENTS.md` for the provenance rules.

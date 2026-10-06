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

> Status: in progress. Plan 01 (foundation + safety core) has landed; the product
> spec and the remaining work are broken into dependency-ordered plans under
> `docs/plans/`. See [Development](#development) to build and test.

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
burrow completion <shell>     # shell completion
burrow version
```

Global flags include `--dry-run`, `-y/--yes`, `--json`, `--only`, `--exclude`,
`--min-size`, `--config`, and `--log-level`. Exit codes are meaningful: `75` means
a safety guard stopped an action, `124` a subprocess timeout, `130` SIGINT.

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

## Architecture

```
cmd/burrow/         main() -> cobra root
internal/cli/       cobra commands, flag wiring, output formatting
internal/engine/    run pipeline: discover -> plan -> preview -> execute -> journal
internal/catalog/   cleanup catalog: category/item registries (data + small logic)
internal/action/    Action interface + TrashAction, OwnerCommandAction
internal/owner/     owner-command runners, timeouts, per-distro command tables
internal/distro/    distro detection + adapter interface (apt first)
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
- **CLI:** spf13/cobra.
- **Config:** TOML; whitelist is a separate newline-delimited file.
- **Trash:** native freedesktop Trash Spec implementation, no runtime dependency.
- **v0.1 target:** Debian/Ubuntu + systemd, with a distro-adapter interface
  designed in from day one for dnf/pacman/zypper/apk.

## Non-goals

Not a privacy cleaner (no cookie/password/shred/wipe). Not a package manager. Not
a system optimizer. No GUI. No background daemon or scheduler. No
distro-universal v0.1.

## Implementation plans

Work is broken into dependency-ordered plans in [`docs/plans/`](docs/plans/).
Start with [`docs/plans/README.md`](docs/plans/README.md) for the dependency graph,
execution waves, and per-plan ownership map.

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

### Repository layout

See [Architecture](#architecture) for the package map. Package ownership and the
dependency graph are in `docs/plans/README.md`; agent working notes and the
non-negotiable safety rules are in `AGENTS.md`.

## Reference documents

- `docs/burrow-spec.md` — the full product spec and locked decisions (ADRs).
- `docs/mole-linux-portability.md` — why Mole cannot be ported and what a Linux
  build should reuse.
- `AGENTS.md` — working notes for coding agents.

## License

Apache-2.0. `burrow` is an independent implementation; it does not copy Mole's
GPL-3.0 source. See `AGENTS.md` for the provenance rules.
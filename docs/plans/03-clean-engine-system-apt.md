# Plan 03 — Clean engine, owner/distro layer, preview UI, `system`/apt catalog

**Status:** done (open findings F1/F3/F6 in docs/status.md)
**Milestone:** M2
**Depends on:** 01
**Blocks:** 04, 05, 07
**Parallel with:** 02

## Goal

Build the orchestration core: the run pipeline, the action/category/guard
contracts, the owner-command runner with distro adapters, the preview/review UI,
and the first real cleanup category — `system` on Debian/Ubuntu via apt/dpkg.
This is the plan that turns `burrow` from an explorer into a cleaner, and it
establishes the patterns every later catalog plan follows.

## Deliverables

```
internal/engine/            pipeline, Plan, Env, ordering, totals, summary
internal/action/            Action interface + TrashAction + OwnerCommandAction
internal/catalog/           Category/Target registry framework + system category
internal/catalog/system/    apt/dpkg items (spec §8.1)
internal/owner/runner.go    exec + timeout + dry-run + per-user vs sudo
internal/owner/timeouts.go  central deadline table (created in 01, used here)
internal/owner/commands.go  allow-listed argv tables (apt first)
internal/distro/apt.go      apt/dpkg detection + guards + item commands
internal/tui/clean.go       preview + review screens
internal/cli/clean.go       command + flags
testdata/clean/             fixtures + golden plan output
```

## Tasks

### 1. Core contracts (`internal/engine`, `internal/action`, `internal/catalog`)
- Implement the interfaces from spec §6.2 exactly: `Target`, `Action`, `Category`,
  `Guard`, `Recovery`, `Risk`, `Env`.
- `Action` implementations:
  - `TrashAction` (delegates to `internal/trash`, re-validates identity);
  - `OwnerCommandAction` (delegates to `internal/owner.Runner`, exact argv).
- Both must be idempotent and re-validate identity at execution time (spec §6.2).
- `Action.Run(ctx, dryRun)` must compute the same plan without mutating when
  `dryRun` is true.

### 2. The run pipeline (spec §6.3)
- Implement steps 1–9: resolve env, discover, measure, filter, plan, preview,
  execute, journal, summary.
- Invariant I1: steps 2–5 are pure/read-only; nothing mutates before step 7.
  `--dry-run` stops after step 6.
- Discover is parallelizable per category with a per-category timeout; collect
  errors as skips, not aborts.
- Execute: for each target, re-run `Guard.Check` -> re-validate identity ->
  `Action.Run`. A tripped guard skips with a recorded reason and does not abort
  the run. SIGINT => graceful stop; already-done items stay done.
- Journal every attempt (`ok`/`skipped`/`failed`/`dry-run` + reason + bytes).
- Summary: totals, freed space (`df` before/after), skipped-with-reason, journal
  path.

### 3. Owner runner + elevation (`internal/owner`)
- `Runner.Run(ctx, argv, opts)`:
  - exact argv from the catalog; no dynamic construction from user input (I4);
  - bounded timeout from `timeouts.go`; timeout => skip + reason (exit-124
    semantics, but continue the run);
  - `dry-run` prints the argv and returns without executing;
  - no shell interpolation; resolve binaries with `exec.LookPath` as the invoking
    user, then run the absolute path under `sudo --` when root is required;
  - per-user owner commands (npm/pnpm/go/etc. — used in Plan 04) run as the
    invoking user, never root;
  - never inherit the caller's `PATH` into a root command when avoidable.
- Distro detection from `/etc/os-release`; `internal/distro.Adapter` interface:
  `ID()`, `PackageManager()`, `LockFiles()`, `NeedsRoot()`, `PackageCacheAction()`,
  `OrphansAction()`, `ResidualConfigAction()`, `OldKernelsAction()`, `LockGuard()`,
  and `System() SystemSpec`.
  - Cross-plan note (Plan 07): Plan 07 added `System() SystemSpec` to this
    interface and rewrote `internal/catalog/system/system.go` to consume the
    `SystemSpec` data instead of calling apt-specific probes directly. This makes
    the `system` catalog distro-agnostic so dnf is a data-only addition. The
    file is Plan 03-owned; the change was landed by the Plan 07 integration pass.
    `SystemSpec` carries the package-cache command/path, the orphan probe and its
    parser, the residual-config hook, and the kernel probe/parser. The existing
    adapter methods are unchanged and remain the action boundary.

### 4. Guards (`internal/distro/apt.go`, `internal/safe`)
- Package-manager lock guard: probe `/var/lib/dpkg/lock-frontend` and
  `/var/lib/dpkg/lock` via `flock -n`, plus a live `apt`/`dpkg` process check.
  Tri-state; `Busy`/`Unknown` => stop.
- Container/toolchain guards are Plan 04/05; define the interface here.
- I7: privileged deletes refuse when the parent chain contains a user-writable dir.

### 5. `system` category, apt items (spec §8.1)
Implement the apt/dpkg rows with correct recovery contract, root flag, guard,
risk, and default:

| Item | Action argv | Recovery | Root | Risk | Default |
|---|---|---|---|---|---|
| `system.pkg-cache` | `apt-get clean` | owner | yes | low | on |
| `system.orphans` | `apt-get autoremove --purge` | owner | yes | high | off |
| `system.residual-config` | `dpkg --purge <pkgs>` (pkgs from `dpkg -l` `^rc`) | owner | yes | med | off |
| `system.old-kernels` | `apt-get autoremove --purge` | owner | yes | high | off |
| `system.journald` | `journalctl --vacuum-size=<cfg>` (default 500M) | owner | yes | low | on |
| `system.rotated-logs` | `systemd-tmpfiles --clean` + age-targeted rotated files | owner | yes | med | off |
| `system.tmp` | `systemd-tmpfiles --clean` | owner | yes | low | off |
| `system.core-dumps` | user-owned -> Trash; else `coredumpctl` rotate | mixed | maybe | low | off |
| `system.crash` | `systemd-tmpfiles --clean` + trash user-owned | mixed | yes | low | off |

- Detect + measure are read-only: `apt-get -s autoremove`, `dpkg -l`,
  `journalctl --disk-usage`, `du` of cache dirs, etc.
- `system.orphans` / `system.old-kernels`: high risk => default OFF, always shown,
  never auto-selected; they change installed software (spec §8.1 note).
- Distro adapter table must be data, so Plan 07's dnf adapter is additive.

### 6. Preview + review UI (`internal/tui/clean.go`, `internal/cli/clean.go`)
- Preview per spec §16: detected platform header, rows grouped by category with
  a header line, per-row checkbox/name/size/`[trash]`|`[owner]`/`(root)`/risk dot,
  footer totals and key hints.
- A second **review** screen listing every exact path/argv that will run.
- High-risk rows render red and are never auto-selected.
- After execution: per-item result lines + totals + `Freed X (df: A -> B)` +
  journal path.
- `clean` flags per spec §5.2: `--all`, `--no-owner`, `--permanent`,
  `--whitelist` (manager lands in Plan 04; parse the flag here).
- `-y/--yes` requires a preview unless `--dry-run`.

## Acceptance criteria

- Container matrix (spec §14.2) green for `ubuntu:24.04` and `debian:12` with
  stubbed apt in `testbin/`: preview -> execute produces exactly the planned argv
  set; `--dry-run` produces the plan with zero mutations.
- Held dpkg lock fixture -> guard trips, item skipped with reason, exit code 75
  when the guard is the only blocker.
- Whitelist (basic prefix match from Plan 01) excludes matching targets.
- Exit codes per spec §5.5 verified.
- Journal records one entry per attempted target; `run_id` frames the run.
- `scripts/check.sh` green.

## Risks & notes

- **This plan defines the interfaces other catalog plans consume.** Land it
  before 04/05 and keep `internal/catalog` framework changes backward compatible;
  catalog plans should add data, not reshape the framework.
- **apt/dpkg argv correctness** is high-stakes. Keep every argv in the allow-list
  table; never build a command from user input. `dpkg --purge` takes a package
  list derived from owner output, which must be validated against the parsed
  `^rc` set.
- **Sizing apt caches** can be slow; use the 30s size-probe timeout and render
  "unknown" on timeout.
- Do not run the whole tool as root (I6); all elevation is per-command via sudo.

## Provenance

Owner-command delegation, categorized preview, and the review screen are
behavioral ports of Mole's cleanup UX, reimplemented clean-room. Command list
cross-checked against spec §21.1. No Mole source copied.
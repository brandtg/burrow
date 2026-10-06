# Plan 01 — Foundation, safety core, CLI skeleton

**Status:** done
**Milestone:** M0
**Depends on:** none (blocking)
**Blocks:** 02, 03 (and transitively everything)
**Parallel with:** none

## Goal

Produce a buildable `burrow` binary that has the **safety contract proven before
any catalog work exists**. After this plan, the tool can resolve its environment,
read config, write and read an operation journal, move files to a freedesktop
Trash, validate paths, and refuse unsafe operations — with tests that fail if an
invariant is violated. No cleanup category ships here; the point is the spine.

This plan also resolves the one external risk the spec bets on: the gdu scan
engine (see "gdu spike" below).

## Deliverables

```
go.mod / go.sum                 module github.com/brandtg/burrow
LICENSE                         Apache-2.0
cmd/burrow/main.go              main() -> cobra root
internal/cli/                   root command, global flags, version/config/history
internal/config/                TOML load, precedence, defaults, effective-config print
internal/journal/               append-only JSONL writer + history reader
internal/safe/                  path validation, identity snapshot, guards, fail-closed
internal/trash/                 native freedesktop Trash (put/list/restore/empty)
internal/whitelist/             load + prefix match on resolved physical paths
internal/owner/                 runner skeleton, timeouts table, elevation resolution
internal/distro/                distro detection + adapter interface (apt only stub)
internal/engine/                Env type + package skeleton (pipeline lands in 03)
internal/scan/                  gdu adapter skeleton + spike result
internal/i18n/                  message catalog keys (EN only)
scripts/check.sh                fmt + vet + lint + vuln + test + static safety gate
scripts/safety-gate/            AST/grep gate: no direct os.Remove outside safe/trash/owner
.golangci.yml
testbin/                        stubbed owner commands (record argv, canned output)
testdata/safety/                fixtures for the invariant suite
```

## Tasks

### 1. Scaffold and toolchain
- Create module `github.com/brandtg/burrow`; add Apache-2.0 `LICENSE`.
- Decide and pin the Go toolchain. gdu v5.38.0 requires `go 1.26.0`; local is
  `go1.25.0` with `GOTOOLCHAIN=auto`. Pin the toolchain explicitly (e.g. a
  `toolchain go1.26.0` directive) so builds are reproducible; do not rely on a
  developer's ambient version.
- Add `golangci-lint`, `govulncheck` to `scripts/check.sh`.
- Add the static safety gate: a check that fails when `os.Remove`,
  `os.RemoveAll`, `os.Truncate`, or `syscall.Unlink` appear outside
  `internal/safe`, `internal/trash`, and `internal/owner`. This is the analogue
  of Mole's `audit_destructive_sinks.py` and enforces spec §14.5.

### 2. gdu spike (resolve before writing `internal/scan` in anger)
- Confirm `github.com/dundee/gdu/v5/pkg/analyze` is importable and that
  `analyze.CreateAnalyzer().AnalyzeDir(path, ignore, fileTypeFilter)` is callable
  from an external module.
- The signature takes `internal/common.ShouldDirBeIgnored` and
  `ShouldFileBeIgnored`. These types are **not nameable** outside gdu's module.
  Verify the workaround compiles: pass an untyped func literal
  `func(name, path string) bool { ... }` and `nil`/`func(string) bool`.
- Measure the import cost: confirm that importing `pkg/analyze` also compiles
  `stored.go` (badger) and `sqlite.go` (modernc/sqlite), i.e. those deps enter
  our binary. Record binary size and cold build time.
- **Decision gate:** if the spike fails or the cost is unacceptable, switch
  `internal/scan` to a `gdu --json` shell-out adapter. Record the chosen strategy
  and the evidence in `internal/scan/README.md`. Default remains the in-process
  import of pinned v5.38.0.
- Pin the exact gdu version in `go.mod`; wrap all gdu usage in `internal/scan`
  so a later fallback is a one-file change (spec D2 / risk table).

### 3. `internal/safe`
- `ValidateForDeletion(path string) error` rejecting, per spec §7.2:
  - non-absolute paths; `..`, empty segments, control chars, `//`;
  - the protected roots themselves (`/`, `/boot`, `/bin`, `/sbin`, `/lib*`,
    `/usr`, `/etc`, `/var`, `/var/lib`, `/var/log`, `/home`);
  - symlink roots for recursive deletion (`EvalSymlinks`; refuse if the physical
    path differs from the lexical path at the root);
  - anything under the configurable protected set (config + built-in);
  - trash-contract paths not owned by the invoking user.
- Identity snapshot: `Snapshot{ParentDev, ParentIno, TargetDev, TargetIno, Size}`
  captured before execution; `Matches()` re-stats and compares immediately before
  the sink. Mismatch => guard-stop (spec §7.3).
- Guard tri-state: `Safe` / `Busy` / `Unknown`; `Busy` and `Unknown` both stop.
- Table + fuzz tests with seeds: `..`, `//`, control chars, symlink roots,
  protected paths, non-absolute. Property: never returns ok for a path outside an
  allowed root.

### 4. `internal/trash` (native freedesktop spec, no third-party dep)
- Home trash at `$XDG_DATA_HOME/Trash/{files,info}`.
- Cross-filesystem: `$topdir/.Trash/$uid` if sticky+not-symlink, else
  `$topdir/.Trash-$uid`; create when needed.
- `.trashinfo` INI: percent-encoded absolute `Path`, `DeletionDate` local ISO.
- Collision naming: append `.N`; info filename must match the `files/` entry.
- Ops: `Put`, `List`, `Restore`, `Empty`.
- Fail-closed: if the move cannot complete, return an error; **never** fall back
  to `unlink` unless `--permanent` is set by the caller (spec §9, I5).
- `os.Rename` for same-device (compare `Stat_t.Dev`); copy+unlink only cross-device
  when the target fs allows it.
- Golden `.trashinfo` tests; round-trip put→list→restore; collision; cross-device
  via a loopback mount in the container matrix.

### 5. `internal/journal`
- Append-only JSONL at `$XDG_STATE_HOME/burrow/operations.jsonl`.
- Record schema exactly as spec §12. `run_id` (ULID) groups a run; `run_start` /
  `run_end` frame it.
- Journal write failure must warn, never abort cleanup.
- `history` reader: recent runs table (time, command, freed bytes, ok/skip/fail),
  `--json` raw records, `--run <id>` filter.
- Undo hook: `history --restore <run_id>` restores `trash`-contract items still
  present in Trash; `owner` items are explicitly non-restorable (state it).
- Test: schema validation; `run_id` grouping; restore only touches trash items.

### 6. `internal/config`
- TOML at `$XDG_CONFIG_HOME/burrow/config.toml`, parsed with
  `github.com/BurntSushi/toml`; schema per spec §13.2.
- Precedence: CLI flag > env (`BURROW_*`) > config file > built-in default.
- `burrow config` prints effective config + all resolved paths (config, whitelist,
  journal, cache/state dirs).
- `--config <path>` override.

### 7. `internal/whitelist`
- Newline-delimited file at `$XDG_CONFIG_HOME/burrow/whitelist`; `#` comments.
- Prefix match on resolved physical paths; a whitelisted path protects itself and
  its parent from the generic user-cache sweep (spec §13.1).
- Loader + matcher tests. (Interactive manager lands in Plan 04.)

### 8. Elevation + environment (`internal/owner`, `internal/engine.Env`)
- Resolve the invoking user via `SUDO_USER` then `os.Getuid` -> `user.LookupId`;
  compute that user's `HOME` and XDG dirs. **Do not** trust root's `$HOME`.
- I6: if `geteuid()==0` and `SUDO_USER` is unset, refuse with a clear message.
- `owner.Runner` skeleton: exec with a bounded timeout, `dry-run` awareness,
  no shell interpolation, per-user vs `sudo` routing (full command tables land in
  Plan 03). Add `internal/owner/timeouts.go` with the central table
  (quick-detect 5s, size probe 30s, owner-clean 10min, scan per-root 90s).
- Distro detection from `/etc/os-release`; `internal/distro` adapter interface
  with an apt adapter stub (real commands in Plan 03).

### 9. CLI skeleton (`internal/cli`, `cmd/burrow/main.go`)
- cobra root with global flags: `--dry-run`, `-y/--yes`, `--json`,
  `--only`, `--exclude`, `--min-size`, `--no-color`/`--color`, `--config`,
  `--log-level`, `-v/--verbose`, `--quiet`.
- Commands implemented now: `version`, `config`, `history`, `completion`.
  Stub `analyze`, `clean`, `purge`, `status` so the surface exists (they return a
  clear "not implemented" and a non-zero usage code).
- Exit codes per spec §5.5 defined as constants and used by the root error handler.

### 10. Test harness + safety regression suite
- `testbin/`: fake `apt`, `dpkg`, `docker`, `journalctl`, etc. that record argv and
  emit canned output; prepended to `PATH` in integration tests.
- `testdata/safety/`: fixtures for every invariant.
- `safety_test.go` covering I1–I7 with tests that **fail on violation**:
  - I1 no mutation before preview;
  - I2 every mutation declares a recovery contract;
  - I3 no deletion outside allow-listed roots / owner output;
  - I4 owner commands come from the catalog verbatim;
  - I5 fail-closed (trash unavailable never falls back to unlink);
  - I6 root without `SUDO_USER` refuses;
  - I7 privileged ops refuse when a user-writable ancestor exists.
- `scripts/check.sh` runs the whole gate.

## Acceptance criteria

- `go build ./...` and `scripts/check.sh` pass on a clean checkout.
- `burrow version`, `burrow config`, `burrow history` work; `history` reads a
  hand-written journal and renders the summary table and `--json`.
- Trash round-trip green: put -> list -> restore; collision naming; cross-device
  path via loopback mount.
- `ValidateForDeletion` fuzz + table tests green; property holds.
- Identity-swap test: create, snapshot, rename away, replace -> `guard-stop`.
- Safety regression suite I1–I7 green.
- Static safety gate fails when an `os.Remove` is planted outside the allowed
  packages, and passes on the clean tree.
- gdu spike resolved: either `internal/scan` scans a fixture in-process, or the
  shell-out fallback is implemented; the decision + evidence are committed.

## Risks & notes

- **gdu API leak (`internal/common`).** The biggest unknown. If the func-literal
  workaround does not compile, fall back to shell-out. Do not attempt to vendor or
  fork gdu's internal packages.
- **Go 1.26 auto-download.** First build may fetch a toolchain; CI must allow it
  or pre-provision the version. Pin explicitly.
- **Trash on headless/odd filesystems.** Fail closed; tests must assert refusal.
- **`SUDO_USER` in containers** may be unset; the refusal path must be tested.
- Do not implement any catalog item here — keep the foundation free of cleanup
  logic so the safety tests are unambiguous.

## Provenance

Behavioral port of Mole's safety contract (path validation, identity snapshot,
dry-run, whitelist, op-log, trash fail-closed), reimplemented clean-room. No Mole
source copied (Mole is GPL-3.0). Each file that mirrors a Mole concept carries a
short comment naming the concept and stating it is an independent implementation.

---

## Implementation notes (Plan 01)

Recorded after landing the plan. Acceptance criteria all pass; `scripts/check.sh`
is green (golangci-lint and govulncheck warn as not installed on the authoring
host — see "Gate tooling" below).

### Toolchain and dependencies

- `go.mod` pins `go 1.26.0` and `toolchain go1.26.8`. The first build fetches the
  toolchain (verified: it downloaded `go1.26.8`), matching the plan's warning.
- Direct deps: `github.com/dundee/gdu/v5 v5.38.0`, `github.com/spf13/cobra`,
  `github.com/BurntSushi/toml`, `github.com/oklog/ulid/v2`. Everything else is
  indirect, pulled in by gdu.
- `go mod tidy` removes unused requirements, so it must be re-run after adding
  imports; the module graph is fine (`go list -m all` succeeds).

### gdu spike result (in-process import retained)

Full evidence is committed in `internal/scan/README.md`. Summary:

- `analyze.CreateAnalyzer().AnalyzeDir(path, ignore, fileTypeFilter)` is callable
  from an external module. The `internal/common` types are not nameable, but an
  untyped func literal `func(name, path string) bool` and `nil` compile, exactly
  as the plan predicted.
- Importing `pkg/analyze` does pull in badger, modernc/sqlite, tcell/tview,
  logrus, xz, and natural (confirmed with `go list -deps ./internal/scan`).
- Measured: statically linked, ~4.9 MiB, cold build ~1.9 s, `go build -a` ~1.8 s.
  Decision: keep the in-process import; the shell-out fallback stays a one-file
  change inside `internal/scan`.

### Deviations and judgement calls

- **Protected roots: roots vs. subtrees.** Spec §7.2 lists the built-in protected
  directories "themselves"; subtree protection for `/etc`, `/usr`, `/var/lib`
  comes from the default `clean.protected_paths` config, which the engine merges
  into the validator. This keeps `/home` from blocking all user paths while still
  rejecting `/etc/passwd` under the default config. `internal/safe`'s built-in
  list protects the roots themselves plus `/lib*`; config-driven entries are
  prefix-protected.
- **gdu stats aggregation.** `AnalyzeDir` builds the tree but does not aggregate
  sizes; the caller must call `item.UpdateStats(fs.HardLinkedItems{})` at the
  root. `scan.Analyze` does this before building our `Node` tree. Hardlink
  de-duplication is not wired yet (empty `HardLinkedItems`); noted for Plan 02.
- **Journal run totals.** `run_end` records a summary byte count, but
  `journal.Group` sums freed bytes only from item records to avoid double
  counting the frame.
- **CLI testability.** `App.Run(args)` was added; the top-level `Execute()`
  delegates to it. This is what makes exit-code and error-output assertions
  possible without a subprocess.
- **Distro generality.** Per the project owner, the tool is built
  distro-agnostic and switches on `distro.Info.Family()`. Fedora (dnf) and
  Debian/Ubuntu (apt) paths are exercised; pacman/zypper/apk adapters exist as
  detection + lock-file stubs so later plans add only command tables. No
  Fedora-specific behavior is hard-coded without going through the family switch.
- **`internal/engine` scope.** Plan 03 owns the pipeline, but Plan 01 needed the
  Env plus the Target/Action/Category/Guard/Recovery/Risk contract (spec §6.2) so
  I2 is testable. The pipeline itself is not implemented here.
- **No catalog items** were added, as required.

### Gate tooling

- `scripts/check.sh` runs gofmt, `go vet`, `go test`, and the static safety gate
  unconditionally. `golangci-lint` and `govulncheck` are run when present and
  warned about otherwise; set `BURROW_REQUIRE_TOOLS=1` to make their absence
  fatal. This lets the gate pass on a clean checkout that lacks those binaries
  while still enforcing them in CI where they are installed.
- The static gate (`scripts/safety-gate`) is an AST checker, not grep: it maps
  import aliases and flags `os.Remove`, `os.RemoveAll`, `os.Truncate`, and
  `syscall.Unlink` outside `internal/safe`, `internal/trash`, `internal/owner`
  (and itself). It skips `_test.go` files. Verified end-to-end: planting an
  `os.Remove` in `internal/catalog` fails the gate; the clean tree passes. The
  gate has its own tests (`check_test.go`).

### Cross-device trash

- Same-device `Put`/`Restore` use `os.Rename`; cross-device falls back to
  copy-into-place then remove, never a bare unlink.
- The per-mount trash (`$topdir/.Trash/$uid` with sticky bit, else
  `$topdir/.Trash-$uid`) is implemented. It is exercised by an opt-in test
  (`TestCrossDevice`, gated on `BURROW_CROSS_DEVICE_DIR`) driven by
  `scripts/integration.sh`, which creates a loopback ext4 mount under `mktemp`
  and requires root. `scripts/check.sh` runs it only when
  `BURROW_INTEGRATION=1`. `List` currently reads the home trash only; per-mount
  listing is deferred and noted here so undo across filesystems is a known gap.

### Invariant test coverage (I1–I7)

`internal/safe/safety_test.go`:

- I1 — read-only pipeline (scan/validate/snapshot/whitelist/config) leaves a
  fixture tree byte-identical (name/mode/mtime/content digest).
- I2 — `engine.Target.Validate` rejects a mutation with no/invalid recovery
  contract or a mismatched action.
- I3 — allow-list validator rejects paths outside `Roots`; `WithinRoot` boundary
  property.
- I4 — owner argv is passed verbatim. `safety_test.go`
  `TestI4OwnerCommandsVerbatim` covers the dry-run plan; the real execution
  proof is `internal/owner/runner_test.go` `TestRunExecutesVerbatimWithoutShell`
  (a real `echo` with shell metacharacters, showing `exec.Command` never invokes
  a shell).
- I5 — `trash.Put` against an unavailable trash fails and leaves the source.
- I6 — `engine.Resolve` refuses euid 0 without `SUDO_USER`; non-root resolves.
- I7 — `safe.MutableAncestor` finds a user-writable ancestor and the resulting
  guard-stop maps to exit code 75.

### Known gaps / follow-ups for later plans

- Hardlink-aware sizing via `fs.HardLinkedItems` (Plan 02).
- `trash.List` covers only the home trash; per-mount trashes are not enumerated
  (affects `history --restore` for cross-filesystem items).
- `internal/engine` pipeline, catalog, action implementations, and TUI are out of
  scope here and land in Plans 02/03+.
- `golangci-lint`/`govulncheck` were not run on the authoring host; CI should run
  `scripts/check.sh` with `BURROW_REQUIRE_TOOLS=1`.
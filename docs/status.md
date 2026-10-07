# burrow v0.1 status, deviations, and next steps

Last reviewed: 2026-10-07, at commit `f801ec9` (Plan 07 merged to `main`).

This is the audit companion to the plan set. It answers three questions: how much
of the plan landed, where the code deviated and whether the deviation was an
improvement, and what is left before a user can lean on the tool. It is a living
document: update it when a finding is fixed.

## Verdict

v0.1 is functionally complete. All seven plans landed and `make gate` is green;
CI runs `make gate-strict` plus the container matrix; the binary is statically
linked and was exercised end-to-end on Fedora 44 (`version`, `config`, `status
--json`, `analyze --json`, `clean --dry-run`, `purge --dry-run`). The safety core
(I1-I6) is solid and genuinely tested, not just asserted in prose.

The direction is better than the plan in several places: the dnf adapter really
is a data-only addition via `SystemSpec`; the `--permanent` and trash paths
re-validate and snapshot at the sink; the container matrix, man-page freshness
check, static-binary check, and the `safe.RemoveTree` confinement gate are all
real. The gaps below are mostly polish and test depth.

Rough plan coverage (acceptance criteria met, not lines of code):

| Plan | Coverage | Caveat |
|---|---|---|
| 01 foundation + safety | ~95% | I7 helper exists but is never called |
| 02 analyze | ~90% | no 10 GB `<2s` benchmark; TUI delete path untested end-to-end |
| 03 clean engine + system/apt | ~95% | I7 unwired |
| 04 dev + user-cache + downloads | ~95% | some guards/paths untested |
| 05 containers + flatpak/snap | ~90% | `snap.cache` and `k8s-evicted` intentionally omitted |
| 06 purge | ~95% | test gaps only |
| 07 polish + dnf + packaging | ~95% | release artifacts not yet built/released |

## Deviations that are improvements (keep)

- **dnf is data-only.** Adding Fedora did not special-case the engine; the system
  catalog consumes a `SystemSpec` and the adapter supplies the data
  (`internal/distro/system.go`, `internal/catalog/system/system.go`). This is the
  D10 acceptance test passing.
- **Rust registry/rustup use the `trash` contract**, not `owner` as spec §8.4
  lists, because cargo/rustup have no cache-clean command for those subtrees.
  Self-consistent with the engine's action/recovery check.
- **`docker system df` sizing** leaves dangling images `SizeKnown=false` rather
  than overstating the saving (spec §8.8, "no fake savings").
- **Docker `system prune -a` supersession** is handled by a `Reconciler` hook
  (`internal/catalog/catalog.go`) so the preview and the executed plan agree.
- **snap.cache was dropped** rather than shipping a `find ... -delete` under sudo
  that bypasses `internal/safe`. Correct call: it was the one generic delete
  primitive the safety contract forbids.
- **Per-user commands drop privileges** under `sudo burrow` via
  `SysProcAttr.Credential` + env overlay (`internal/owner/runner.go`), closing the
  spec §7.6 gap. (Plan 05's "Known gaps" note still claims otherwise; fixed in
  that plan doc.)

## Findings (F1-F6)

### F1. `--permanent` is presented as recoverable — RESOLVED
`clean --permanent` switched a trash item to a permanent delete but the preview,
review line, and `--json` still read `Target.Recovery`, so an irreversible delete
said `[trash]` / `recovery: "trash"` with no warning (spec §5.1, §7.2).
Fixed: `engine.PlannedItem` carries `Permanent`; the preview/review render
`[delete]` / "permanently delete"; `--json` emits `recovery: "delete"` and
`permanent: true`; `clean` prints an explicit warning. Covered by
`TestRenderPermanentLabelsDelete`, `TestCleanJSONPermanentLabelsDelete`,
`TestExecutePermanentDeletesAndJournals`.

### F2. `TrashAction` fail-closed on a nil snapshot — RESOLVED
`TrashAction.Run` performed the identity check only when a snapshot existed. It
now refuses a present path with no snapshot (guard-stop), matching
`safe.RemoveTree`. An already-absent path is still an idempotent success.

### F3. I7 helper is dead code — OPEN (vacuous)
`safe.MutableAncestor` (`internal/safe/ancestry.go:24`) has no production
caller; only `safety_test.go` exercises it. Because the sole privileged
path-delete (`snap.cache`) was dropped, there is currently no root-owned path
delete to guard, so I7 is satisfied vacuously. Either wire it into any future
privileged path delete or delete the helper and record why I7 is not applicable.

### F4. Permanent-delete primitives are not actually singular — DOCUMENTED
`internal/trash/remove.go` defines `Remove()` = `os.RemoveAll` for the explicit
`--permanent` escape hatch. AGENTS.md now documents both primitives (purge-only
`safe.RemoveTree` and the `--permanent` `trash.Remove`) instead of claiming a
single one. The static gate's `RemoveTree` confinement still does not cover
`trash.Remove`; extending it is optional (the trash path is validated and
snapshotted).

### F5. `purge --yes` deletes before printing anything — RESOLVED
`purge --yes` on a non-TTY now prints the candidate table before executing, then
the summary. Covered by `TestPurgeYesPreviewsBeforeDeleting`.

### F6. Journal omitted the recovery contract for `clean` runs — RESOLVED
`appendRecord` now records `Recovery` for every item, and permanent deletes are
recorded as `action: "delete"` / `recovery: "delete"` so `history --restore`
never offers a permanently-deleted item (which would otherwise try to restore a
same-path trash entry from another run). Covered by
`TestExecutePermanentDeletesAndJournals`.

## Resolved UX/perf issues

- **gdu stderr noise.** gdu logged `level=info ... permission denied` to stderr
  during `analyze` (unreadable subtrees in a real home directory). `internal/scan`
  now raises gdu's logrus level to error, so the noise is gone while error-level
  logs stay; unreadable subtrees are still reported via `SizeKnown=false` and
  `scan_status=partial`.
- **analyze looked frozen while scanning.** Interactive `analyze` scanned
  synchronously before opening the TUI, so a cold scan showed nothing until it
  finished. The model now starts immediately in a loading state and runs the scan
  on a background goroutine, rendering a spinner, live item/size totals, and the
  current directory (`scan.Options` gained a `Progress` callback;
  `tui.AnalyzeConfig` gained `Load`).

## Known gaps (by design or deferred)

- **Catalog omissions:** `snap.cache` (no safe primitive) and
  `containers.k8s-evicted` (report-only, has no action). Documented in Plan 05.
- **`system.core-dumps` / `system.crash`** are trash-only / owner-only; the
  spec's "mixed" recovery is not fully realized (`internal/catalog/system/system.go`).
- **`trash.List` reads only the home trash**; `history --restore` misses
  cross-filesystem items. Documented in Plan 01.
- **`safe.RemoveTree` has no mount-boundary check**; a bind mount inside a purge
  root is traversed. Bounded by the configured roots.
- **`FreeBytes` always measures `/`** (`internal/engine/pipeline.go:305,318`), so
  the "df before/after" line is wrong for items on another filesystem.
- **Residual-config subset check is self-referential**
  (`internal/catalog/system/system.go:112-117` builds the allow set from the same
  list it validates); the real guard is `ValidatePackageName` inside the command
  builder.
- **`snap list --all` disabled detection** uses a whole-line `Contains("disabled")`
  rather than the Notes column (`internal/catalog/desktoppkgs/desktoppkgs.go:311`),
  a low-likelihood wrong-row path.
- **`dev.pycache` emits one row per `__pycache__` directory**, which swamps the
  preview (327 items / 51 GiB on the review host). Consider grouping per project.

## Test-coverage gaps

- No benchmark asserts the M1 "10 GB warm scan `<2s`" criterion (Plan 02).
- `analyze --json` `scan_status=partial` is tested at the scan layer only, not
  through the CLI.
- The analyze TUI `d`/`D` path and `history --restore` of an analyze run are not
  tested end-to-end (only `TrashExec` and `trash.Restore` separately).
- `purge --age`, `BURROW_PURGE_MAX_AGE`, and `purge --json` shape are untested
  (JSON is substring-smoke-tested, not golden).
- Non-cargo process guards (npm/pnpm/go/gopls) and the AWS/gcloud/Azure
  cloud-cli trash paths are untested.
- The container matrix runs in CI (`make matrix`) but not in the default
  `make gate` (it needs a runtime; gated behind `BURROW_MATRIX=1`).

## Recommended next steps

1. **Done:** F1 (permanent labeling + warning), F2 (fail-closed snapshot), F5
   (purge preview before `--yes`), F6 (journal recovery). Regression tests added.
2. **Reduce preview noise**: group `dev.pycache` per project; quiet gdu logging.
3. **Build and smoke a real release** (`goreleaser release --snapshot --clean`,
   `make verify-static`, run `install.sh` against the checksums) so Plan 07's
   packaging acceptance is proven, not just configured.
4. **Close the test gaps** above, prioritizing the analyze delete→restore loop and
   a scan benchmark on a generated fixture.
5. **Decide F3/F4**: remove or wire `safe.MutableAncestor` (I7 is vacuous today),
   and optionally extend the static gate to cover `trash.Remove`.
6. **Consider v0.2 scope**: pacman/zypper/apk adapters (data-only), snapshot
   deletion, per-mount trash listing, and a `--permanent` policy decision (spec
   §20 open question 7).

## Trying it

```sh
make build                       # -> bin/burrow (static)
./bin/burrow version
./bin/burrow config              # effective config + resolved paths
./bin/burrow status              # one-shot snapshot
./bin/burrow analyze             # interactive tree (read-only except d/D)
./bin/burrow clean --dry-run     # preview only
./bin/burrow clean               # interactive preview -> confirm -> execute
./bin/burrow purge --dry-run     # rebuildable project artifacts
./bin/burrow history             # journal; --restore <run_id> for trash items
```

Run `clean`/`purge` as yourself, never with a bare `sudo burrow` (I6); burrow
elevates individual owner commands itself.
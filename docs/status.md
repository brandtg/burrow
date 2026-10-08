# burrow v0.1 status, deviations, and next steps

Last reviewed: 2026-10-07, at commit `f801ec9` (Plan 07 merged to `main`); Plan 09
(curated cleanup) landed on top of Plan 08.

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
| 09 curated cleanup | ~95% | active-project guard is commit-recency only (no dirty-tree check) |

## Plan 09 — curated cleanup (landed)

v0.1 was safe but indiscriminate: a thumbnail cache and a 100 GB model cache
were both "low risk, default on", the XDG sweep was a catch-all, and purge
judged staleness from the artifact's own mtime. Plan 09 adds the missing
knowledge. The safety contract (I1-I7) is untouched.

- **Regeneration cost.** `engine.Target` gained `Regen`
  (`free`/`cheap`/`expensive`/`data`) and `Gated`, independent of `Risk`.
  Effective pre-selection is `DefaultOn && Regen <= cheap && Risk != high &&
  !Gated`; bulk selection uses the same predicate. `Regen`/`Gated` affect
  selection only and never change execution.
- **Curated XDG sweep.** `internal/catalog/usercache` keeps a cheap allowlist
  (pre-selected), classifies known-expensive caches (`huggingface`, `torch`,
  `ms-playwright`, `bazel`, `JetBrains`, ...) as `expensive` and off, and
  defaults unrecognized entries to off. `[user-cache] sweep_unknown = true`
  restores the old behavior.
- **Idle gating.** `[dev]`/`[user-cache] max_age` (default 30d) marks a
  recently-used cache `Gated` (shown, not pre-selected) with an evidence line.
- **Purge project activity.** `Git.LastCommit` plus `[purge] active_within`
  (default 30d) keep artifacts in a recently-committed repo. Per-type windows
  (`[purge] max_age_by_type`) give `node_modules` 30d, `target`/`.next/cache`
  7d, `__pycache__` none. Generic names (`build`, `dist`, `target`, `.gradle`)
  require an ecosystem marker.
- **Keep action.** `k` in the clean preview whitelists the row's path(s) and
  clears the selection; the choice persists to future runs.

Deviations/notes: the active-project guard uses commit recency only (a
repo-wide dirty-tree check would keep every artifact in a repo with one
unrelated edit, so it was left out); expensive caches stay on the `trash`
contract, which does not free space until the Trash is emptied (deliberate
recoverability trade-off, noted as a follow-up).

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
- **`analyze /` silently omitted same-filesystem subvolumes.** `one_file_system`
  defaulted to `true` and the CLI merged it with `||`, so `-x` could only turn
  the behavior on. On btrfs, `/` and `/home` share a filesystem but have distinct
  `st_dev`, so `/home` was skipped with no indication. The default is now `false`
  and an explicit `-x` overrides config (spec §13 precedence); `-x` still skips
  `/home` on such a host.
- **`analyze` under `/run/media` reported almost nothing (borg-backup bug).** The
  default ignore list ended in `/run` and matched by prefix, so scanning a path
  under `/run/media/<user>/<label>` pruned its entire subtree (a 1.7 TiB borg
  repo reported 449 MiB). `/run` is a tmpfs that hosts real mounts, so it is no
  longer ignored; the remaining pseudo-filesystems are `/proc,/dev,/sys`.
  Additionally, an ignore entry that is the scan root or an ancestor of it is now
  dropped, so a configured ignore can never prune the tree the user asked to
  scan.
- **One-file-system boundary is now mount-source aware and the default.** A scan
  stays on the backing store of its root, measured from `/proc/self/mountinfo` by
  mount source, so btrfs subvolumes on one device (`/`, `/home`) remain in scope
  while nested mounts on other stores are skipped and reported as
  `skipped_mounts` (JSON) / a header warning (TUI). Real filesystems warn; virtual
  mounts (tmpfs, proc, fuse, overlay) are skipped silently. `--cross-mounts`
  descends into them; `-x` forces the boundary on. Config default
  `analyze.one_file_system` is now `true`.
- **Reflinked copies are no longer counted per inode.** On btrfs/xfs, analyze
  re-attributes on-disk sizes with `FS_IOC_FIEMAP`: each physical extent counts
  once across the scanned tree and is split among the scanned files that
  reference it (spec §8.8). This fixes `cp --reflink`-ed `node_modules` being
  reported at N times their real size. Semantics: unique physical bytes
  referenced by the tree; extents shared with files outside the scanned root are
  still charged to the in-tree file, so scan a common parent for full dedup.
- **Partial scans were invisible in the TUI.** Permission-denied subtrees only
  set `scan_status=partial` in `--json`; the interactive header now shows
  `partial scan: N item(s) could not be read` (`scan.UnknownCount`), alongside
  the per-row `unknown` size.
- **`clean` preview was an unbounded plain-text dump.** Plan 08 makes the TUI the
  default view on a terminal, including `--dry-run` (read-only), adds `--plain`
  for non-interactive output, collapses large categories/groups, groups
  `dev.pycache` per project, and aggregates the review screen. This is a
  deliberate revision of spec §5.1 (previously "`--dry-run` … print the full plan
  and exit"); the spec and user guide are updated in the same change.

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
- **`dev.pycache` emitted one row per `__pycache__` directory**, which swamped
  the preview (327 items / 51 GiB on the review host). Resolved by Plan 08:
  `__pycache__` targets now carry a presentation group keyed to the top-level
  project under each purge root, and large categories/groups render collapsed.

## Test-coverage gaps

- No benchmark asserts the M1 "10 GB warm scan `<2s`" criterion (Plan 02).
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
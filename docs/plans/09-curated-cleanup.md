# Plan 09 — Curated cleanup (regeneration cost, idle gating, project activity)

**Status:** done
**Milestone:** M8 (post-v0.1 curation)
**Depends on:** 03, 04, 05, 06, 08
**Blocks:** —
**Parallel with:** —

## Goal

v0.1 is safe but indiscriminate. The catalog treats a 10 KB thumbnail cache and
a 100 GB HuggingFace model cache as the same "low risk, default ON" item, sweeps
every unclassified `$XDG_CACHE_HOME` entry by default, and decides artifact
staleness from the artifact's own mtime rather than whether the project is
active. The result is a cleaner that is willing to delete caches a person
actually uses and cannot tell an abandoned project from one under active work.

This plan adds the missing knowledge:

1. A **regeneration-cost** axis (`free`/`cheap`/`expensive`/`data`), separate
   from `Risk`. Only free/cheap targets are ever pre-selected or bulk-selected.
2. A **keep-by-default** generic XDG sweep: only a curated allowlist of cheap
   caches is pre-selected; unknown and expensive caches are shown but off.
3. **Idle gating** for dev and user caches: a cache touched within the idle
   window is shown but not pre-selected.
4. **Purge** learns **project activity** (git commit recency), **per-artifact
   age windows**, and **ecosystem markers** for generic directory names.
5. A **keep** action in the clean TUI that persists a path to the whitelist.

The safety contract (I1–I7) is untouched. Everything here is additive to
`engine.Target`, `engine.Options`, the catalog data, and config. No new
destructive primitive is introduced; every mutation still flows through
`trash`/`owner` and `safe.RemoveTree` exactly as before.

## Non-goals

- No new permanent-delete path. Expensive caches stay on the `trash` contract;
  they are simply no longer pre-selected. (A future plan can add an explicit
  owner-style permanent path for model caches if the trash-doesn't-free-space
  problem warrants it.)
- No daemon, scheduler, or background scanning.
- No change to the `clean`/`purge` `--json` schema beyond additive fields.
- No re-litigation of locked spec decisions.

## Decisions (locked for this plan)

- `Regen` is independent of `Risk`. `Risk` still means blast radius/safety;
  `Regen` means cost to rebuild. `Regen` gates *selection* only and never
  changes how an item executes.
- Zero value of `Regen` is `RegenFree` so existing targets keep working.
- Default selection becomes `DefaultOn && Risk != High && Regen <= Cheap &&
  !Gated`. Bulk selection (`a`, category/group toggle) uses the same predicate
  without requiring `DefaultOn`.
- The generic XDG sweep keeps a curated **allowlist** of cheap caches
  (`cheapCacheNames`), an **expensive** classification (`expensiveCacheNames`),
  and defaults every other entry to OFF. `[user-cache] sweep_unknown = true`
  restores the old aggressive pre-selection.
- Idle gating is **advisory and non-fatal**: a recently-used cache is shown with
  `Gated=true` and an evidence line, and can still be selected by hand. A
  missing/garbage mtime does not gate (nothing to prove).
- Purge's active-project guard is a **heuristic**, not a safety gate. It uses
  the owning repo's last commit time and, on any git error, contributes no keep
  reason (the tracked-set check remains the fail-closed protection). The
  per-artifact age filter remains fail-closed on unverifiable activity.
- Generic artifact names (`build`, `dist`, `target`, `.gradle`) require an
  ecosystem marker in the parent directory; without one the candidate is kept
  with a reason rather than silently deleted.

## Deliverables

```
internal/engine/types.go          Target.Regen, Target.Gated, AutoSelectable, Regen.String
internal/engine/pipeline.go       selection predicate; Options passthrough
internal/catalog/targets.go       TrashTargetOptions.Regen/Gated; Idle helper
internal/catalog/dev/dev.go       per-item Regen classification; idle gate
internal/catalog/usercache/*.go   allowlist/expensive/unknown classification; idle gate
internal/purge/artifact.go        marker table + requiresMarker
internal/purge/git.go             Git.LastCommit, ExecGit impl, cache
internal/purge/discover.go        per-type age, active-project guard, marker keep
internal/tui/clean.go             'k' keep key; [kept]/regen rendering
internal/tui/purge.go             age/active reason already rendered via KeepReason
internal/cli/clean.go             load whitelist manager; clean JSON regen/kept
internal/cli/purge.go             pass MaxAgeByType + ActiveWithin from config
internal/config/config.go         [dev], [user-cache]; Purge.MaxAgeByType/ActiveWithin
docs/burrow-spec.md               §8.4/§8.5/§11 + new classification note
docs/user-guide.md, README.md     document the new behavior and knobs
docs/status.md                    record plan 09 and superseded gaps
docs/plans/README.md              plan 09 row + dependency edge
testdata/clean/*.golden           regenerated (additive fields only)
```

## Tasks

### 1. Regeneration-cost dimension (`internal/engine`)
- Add `Regen` enum with `String()` (`free`/`cheap`/`expensive`/`data`).
- Add `Target.Regen Regen` and `Target.Gated bool`.
- Add `func (t Target) AutoSelectable() bool` =
  `!t.Gated && t.Risk != RiskHigh && t.Regen <= RegenCheap`.
- `Build`: `selected := t.DefaultOn && t.AutoSelectable()`.

### 2. Catalog plumbing (`internal/catalog`)
- Add `Regen` and `Gated` to `TrashTargetOptions` and set them on the target.
- Add `IdleWithin`/`IsRecent(path, within) (recent, known bool)` helper that
  stats the path's own mtime (cheap, no tree walk).

### 3. Dev classification + idle gate (`internal/catalog/dev`)
- Classify each item's `Regen` by id prefix:
  - `free`: frontend caches, `go-build`, yarn, gem, rbenv, cpan, docker-buildx,
    cloud-cli, pycache.
  - `cheap`: npm, pnpm, bun, pip, uv, poetry, mise, corepack, gh.
  - `expensive`: `go-mod`, `rust-registry-cache`, `rustup-downloads`, conda, nix.
- Apply `[dev] max_age` (default `30d`) as an idle gate at the end of
  `Discover`: a target with a path whose mtime is newer than the window gets
  `Gated=true` and an "active within …" evidence line. `Path == ""` (owner
  commands with no resolved cache root, e.g. conda/nix) is not gated.

### 4. User-cache sweep (`internal/catalog/usercache`)
- Keep `protectedNames` (never shown).
- Add `cheapCacheNames` (default ON, low risk, free/cheap): fontconfig,
  mesa_shader_cache, gstreamer-1.0, tracker3.
- Add `expensiveCacheNames` (default OFF, med risk, expensive): huggingface,
  torch, wandb, whisper, sentence-transformers, ms-playwright, puppeteer,
  chromium, google-chrome, cypress, bazel, bazelisk, JetBrains, pre-commit.
- Everything else: default OFF unless `[user-cache] sweep_unknown`, med risk,
  cheap regen.
- Apply the same idle gate to allowlisted/unknown entries.

### 5. Purge project activity, per-type age, markers (`internal/purge`)
- `Git` interface gains `LastCommit(ctx, repoRoot) (time.Time, error)`;
  `ExecGit` runs `git -C <root> log -1 --format=%ct`; `gitCache` caches it.
- `Options` gains `MaxAgeByType map[string]time.Duration` and
  `ActiveWithin time.Duration`.
- `classify`: effective window = `MaxAgeByType[pattern]` when the key is
  present, else `MaxAge`. An explicit `0` disables the age filter for that type.
- Active-project guard: when `ActiveWithin > 0`, the artifact is inside a repo,
  and the last commit is newer than the window, keep it with reason
  `active project (last commit <age> ago)`. Git errors contribute no keep.
- Markers: `requiresMarker(pattern)` + `hasMarker(parent, pattern)`; a missing
  marker sets a keep reason. Applied only to the built-in generic names.
- Config `[purge]`: `max_age_by_type` (map), `active_within`.

### 6. Keep action (`internal/tui`, `internal/cli`)
- Add `PlannedItem.Kept bool` (display + selection only).
- Add `CleanConfig.Whitelist *whitelist.Manager`.
- Bind `k`: on an item row with a path, add the path to the whitelist, save,
  set `Selected=false`, `Kept=true`, and a status line. Group/category rows keep
  every member path.
- Render `[kept]` in the item line; include `regen`/`kept` in the clean JSON.
- `runClean` loads the whitelist manager (best effort) and passes it in.

### 7. Config (`internal/config`)
- Add `[dev] max_age` (default `30d`).
- Add `[user-cache] sweep_unknown` (default `false`), `max_age` (default `30d`).
- Extend `[purge]` with `max_age_by_type` and `active_within` (default `30d`).
- Env overrides: `BURROW_DEV_MAX_AGE`, `BURROW_USER_CACHE_SWEEP_UNKNOWN`,
  `BURROW_PURGE_ACTIVE_WITHIN`.

### 8. Docs, goldens, tests
- Update spec §8.4/§8.5/§11 with the classification and new defaults; add a
  short §8.9 "regeneration cost and idle gating".
- Update README catalog sections and `docs/user-guide.md` config sample.
- Update `docs/status.md` and `docs/plans/README.md` (plan 09 row + `03/04/05 ->
  09` edges).
- Regenerate `testdata/clean/*.golden` (`BURROW_UPDATE_GOLDEN=1`), reviewing the
  diff (additive `regen`/`kept` only).
- Tests: `Regen.String`/`AutoSelectable`; dev idle gate gates a fresh cache and
  not an old one; user-cache allowlist/expensive/unknown selection; purge
  per-type age, active-project keep, and marker keep; TUI `k` writes the
  whitelist and clears selection; config defaults/overrides.

## Acceptance criteria

- A fresh `~/.cache/huggingface` is present in the preview as `expensive`,
  default OFF, and is never bulk-selected by `a`.
- A freshly-used dev cache is shown with an "active" evidence line and is not
  pre-selected; an idle one still is.
- `burrow purge` keeps a `node_modules` in a repo committed within
  `active_within`, and keeps a `build/` directory with no ecosystem marker.
- `burrow clean` `k` on an item persists the path to the whitelist and the item
  is no longer selected.
- Invariants I1–I7 tests and the static sink gate stay green; `make gate` green.

## Risks & notes

- Selection changes alter which items execute by default. The container matrix
  uses `--only system`, so it is unaffected; dev/purge tests that execute must
  force-select (the `runOnly` helper does).
- `Git` interface growth breaks any test double; `fakeGit` is updated in the
  same change.
- Idle gating uses the cache root's own mtime, not a full tree walk, to stay
  cheap. This is a heuristic; it is documented as advisory.
- Expensive caches remain on the `trash` contract, which does not free space
  until the Trash is emptied. That trade-off is deliberate (recoverability over
  immediate reclamation) and noted as a follow-up.

## Provenance

The keep/discard reasoning (model roots, browser downloads, cargo/poetry/cpan
keep-subtrees, active-project protection) is a clean-room behavioral port of
Mole's `lib/clean/dev.sh` and purge rules. No Mole source was copied (Mole is
GPL-3.0; burrow is Apache-2.0).
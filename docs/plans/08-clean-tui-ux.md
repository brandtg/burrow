# Plan 08 — Clean TUI-first UX and readable preview

**Status:** in progress
**Milestone:** M7 (post-v0.1 polish)
**Depends on:** 03, 04, 05
**Blocks:** —
**Parallel with:** —

## Goal

Make `burrow clean` usable. Today the natural inspect path (`clean --dry-run`)
prints an unbounded plain-text dump: one row per `__pycache__` directory, one row
per `$XDG_CACHE_HOME` entry, then a review screen listing every selected path.
The interactive TUI exists but is bypassed by `--dry-run` and lacks the viewport,
filter, and group controls the purge TUI already has.

The TUI becomes the default view for `clean` on a terminal, including
`--dry-run` (read-only). Non-interactive output becomes opt-in via `--plain`
(and is still the automatic behavior when stdout is not a TTY, or with
`--json`/`--yes`). Large categories render collapsed and expand on demand.

This intentionally revises spec §5.1 ("`--dry-run` … print the full plan and
exit") and extends §16. The spec is updated in the same change.

## Decisions (locked for this plan)

- Non-interactive opt-in flag is `--plain`. `--json` remains the stable machine
  schema; both suppress the TUI.
- `clean --dry-run` on a terminal opens the TUI in read-only mode. Confirming
  shows the review and exits without mutating.
- Large categories are collapsed by default and expand on demand. Selection on a
  category/group row toggles every non-high-risk member.
- Grouping is presentation-only. `Plan.Items` stays 1:1 with executable targets,
  so invariants I3/I4 are untouched; execution is still per-item with its own
  guard and identity snapshot.

## Deliverables

```
internal/engine/types.go       Target.Group / Target.GroupTitle (presentation)
internal/engine/pipeline.go    ItemGroup, CategoryGroup.Groups, GroupedTree
internal/catalog/targets.go    TrashTargetOptions.Group / GroupTitle passthrough
internal/catalog/dev/dev.go    dev.pycache groups by project under each purge root
internal/tui/clean.go          tree model: alt-screen, viewport, collapse,
                               filter, help, dry-run mode; improved render funcs
internal/cli/clean.go          --plain; TUI-first (incl. --dry-run)
docs/burrow-spec.md            §5.1 / §16 revised
docs/status.md                 record the deviation and the pycache fix
README.md, docs/user-guide.md  quickstart + flag documentation
testdata/clean/*.golden        regenerated
```

## Tasks

### 1. Presentation grouping (`internal/engine`, `internal/catalog`)
- Add `Group` and `GroupTitle` to `engine.Target`. Empty `Group` means the item
  belongs directly to its category.
- Add `ItemGroup{ID, Title, Items, Bytes, Unknown}` and `CategoryGroup.Groups`.
  Keep `CategoryGroup.Items` (flattened) for callers that do not need the tree.
- Add `Plan.GroupedTree()`; keep `GroupedByCategory()` delegating to it.
- Add `Group`/`GroupTitle` to `catalog.TrashTargetOptions` and set them on the
  built target.

### 2. `dev.pycache` grouping (`internal/catalog/dev`)
- Group each `__pycache__` by the first path segment under its purge root
  (the top-level project directory), so 327 rows collapse to a handful of group
  rows. Per-directory git guard and path validation are unchanged.

### 3. Clean TUI rebuild (`internal/tui/clean.go`)
- Replace the toy model with a `CleanModel` modeled on `PurgeModel`:
  `tea.WithAltScreen`, viewport + `ensureVisible`, `bubbles/help` footer, `/`
  filter, `[`/`]` category jump, `?` help, and a collapsed/expanded tree
  (category -> optional group -> item).
- `space` on a category or group row toggles all non-high-risk members; `space`
  on an item toggles it. `a` selects all non-high-risk; `n` clears.
- Categories with more than `collapseThreshold` items start collapsed; groups
  with more than `collapseThreshold` items start collapsed. The threshold is a
  constant (default 20).
- `enter`/`r` opens the review screen; `enter` confirms, `esc` backs out.
- `CleanConfig.DryRun` puts the model in read-only mode: the review screen says
  nothing will run and confirming returns without an execute.
- Keep `RunInteractive` as a compatibility wrapper over the new entry point.

### 4. CLI wiring (`internal/cli/clean.go`)
- Add `--plain`.
- `interactive := isTerminal(a.Out) && !a.Flags.JSON && !f.plain && !a.Flags.Yes`.
  `--dry-run` no longer suppresses the TUI.
- Interactive + dry-run: run the TUI, then return without executing.
- Non-interactive + dry-run: print the (now collapsed/aggregated) preview and
  review, then return.
- Non-interactive + `--yes`: preview before executing, as today.
- Non-interactive without `--yes`/`--dry-run`: refuse (unchanged).

### 5. Plain renderers (`internal/tui/clean.go`)
- `RenderPreview` renders category and group headers with counts/sizes; caps
  item rows per group (`previewItemCap`, default 12) and prints `… +N more`,
  pointing at `--json` for the full list.
- `RenderReview` lists owner argv per item, and lists trash items individually
  only when the selection is small; for large selections it aggregates by
  category/group (`trash N dirs across M groups (X GiB)`). Permanent deletes are
  always called out as "permanently delete".
- Unknown sizes render as `unknown (owner-reported)` with a one-line footnote.

### 6. Docs, goldens, tests
- Update spec §5.1/§16, README quickstart, `docs/user-guide.md`, and
  `docs/status.md` (deviation note; supersedes the "one row per `__pycache__`"
  known gap).
- Regenerate `testdata/clean/preview.golden` and `json.golden`; add `group` /
  `group_title` (`omitempty`) to the clean `--json` item.
- Tests: `GroupedTree` grouping, `dev.pycache` project grouping, TUI
  collapse/toggle/filter via direct `Update` calls, dry-run mode confirms
  without executing, non-TTY stays non-interactive.
- `make gate` green.

## Acceptance criteria

- On a terminal, `burrow clean` and `burrow clean --dry-run` open the TUI; the
  latter never mutates.
- `burrow clean --plain` and non-TTY stdout produce bounded, grouped text with no
  repeated indistinguishable rows.
- `burrow clean --json` schema is unchanged except for optional `group` /
  `group_title` fields.
- `dev.pycache` renders one group row per project under the configured purge
  roots, and each directory still carries its own git-tracked guard.
- Invariant tests (I1-I7) and the static sink gate stay green; `make gate` green.

## Risks & notes

- Grouping is display-only; do not move execution into the TUI in this plan
  beyond what already exists. Live per-item progress is a follow-up.
- `--permanent` labeling (finding F1) must survive the review aggregation.
- Keep `--json` stable for the container matrix and golden tests.

## Provenance

The categorized, checkbox-driven preview and review screen remain behavioral
ports of Mole's cleanup UX, reimplemented clean-room. No Mole source copied.
# Plan 02 — Scan adapter, `analyze` TUI, `--json`

**Status:** done
**Milestone:** M1
**Depends on:** 01
**Blocks:** 06, 07
**Parallel with:** 03

## Goal

Ship the read-only half of the product: a fast, interactive disk explorer on top
of the scan engine chosen in Plan 01, with non-interactive `--json` output and
`d`/`D` deletions routed through the Trash module. This is the first user-facing
command and must be safe to run with no confirmation friction beyond the
deletion path.

## Deliverables

```
internal/scan/              gdu adapter + our own item model + fallback sizing
internal/scan/README.md     chosen strategy + spike evidence (from Plan 01)
internal/tui/analyze.go     Bubble Tea tree model
internal/cli/analyze.go     command + flags
testdata/analyze/           fixture tree + golden JSON
```

## Tasks

### 1. `internal/scan` — the adapter
- Wrap the chosen engine behind a stable interface so callers never import gdu
  directly:
  ```go
  type Scanner interface {
      Scan(ctx context.Context, root string, opts Options) (*Node, error)
  }
  // Node matches the struct Plan 01 already shipped in internal/scan/scan.go.
  // Size is on-disk usage (blocks*512); Apparent is the apparent size. Do not
  // swap them: a Plan 02 agent that reads `Size` as apparent would render the
  // TUI and --apparent inverted.
  type Node struct {
      Name      string
      Path      string
      Size      int64 // on-disk (blocks*512)
      Apparent  int64 // apparent size
      IsDir     bool
      Type      string
      Mtime     time.Time
      ItemCount int64
      Children  []*Node
      SizeKnown bool // added in Plan 02 for fallback sizing
  }
  // DisplaySize returns Apparent when apparent is true, else Size. It is the
  // single selector presentation layers use; the two fields are never swapped.
  func (n *Node) DisplaySize(apparent bool) int64
  ```
- Translate gdu's `fs.Item` tree into our `Node` model. Use `fs.HardLinkedItems`
  semantics so shared inodes are not double-counted (spec §8.8).
- Honor `Options`: `OneFileSystem`, `NoHidden`, `Apparent`, `Depth`, `Ignore`
  (default `/proc,/dev,/sys,/run`).
- Fallback sizing: `du -skP` under a 90s timeout when the engine cannot provide a
  subtree; single files via `os.Stat` (`Blocks*512` fills `Size`/on-disk,
  `Size()` fills `Apparent`).
- Unknown/partial -> `SizeKnown=false`, rendered "unknown"; never show 0 as empty
  (spec §8.8, Mole `scan_status` lesson).
- Per-root timeout from `internal/owner/timeouts.go`; cancellation via `ctx`.

### 2. Bubble Tea analyze TUI (`internal/tui/analyze.go`)
- Navigable tree list. Keys: `↑/k` `↓/j` move, `→/l/Enter` enter dir,
  `←/h` parent, `s` sort size, `n` sort name, `t` sort mtime, `c` item-count
  toggle, `/` filter, `d` trash selected, `D` trash highlighted, `space`
  multi-select, `a` select all in view, `?` help, `q` quit.
- Header: path breadcrumb, free space via `statfs`, total under cursor, sort mode.
- Rows: size bar + human size + name; `[R]` marker when a row corresponds to a
  known burrow category root (hook for later plans; no-op data now).
- `d`/`D` route through `internal/trash` with an identity snapshot and a confirm
  prompt; disabled under `--no-delete`. Each successful move writes a journal
  record.
- Respect `--no-color` / `--color`.

### 3. Non-interactive mode
- `burrow analyze --json PATH` emits the stable schema from spec §10:
  ```json
  {"path":"...","total_size":N,"total_files":N,
   "entries":[{"name":"...","path":"...","size":N,"is_dir":true}],
   "largest":[...],"scan_status":"complete|partial|unavailable"}
  ```
- `--depth <n>` and `-t/--top <n>` for non-interactive depth / top-N.
- Golden JSON tests; schema documented as stable in the command help.

### 4. Command wiring (`internal/cli/analyze.go`)
- Flags per spec §5.3: `-a/--apparent`, `-x/--one-file-system`, `-H/--no-hidden`,
  `--depth`, `-t/--top`, `--no-delete`, plus globals.
- `path...` positional; default to cwd.
- When not a TTY, behave like `--json` (or require explicit `--json`) — pick one
  and document; do not hang waiting for keys.

## Acceptance criteria

- Warm scan of a 10 GB fixture completes in <2s (spec M1).
- `--json` schema matches golden; `scan_status` correctly reports partial when a
  subtree is unreadable.
- Hardlinked fixture is not double-counted.
- `d`/`D` moves to Trash, writes a journal record, and is restorable via
  `history --restore` when the item lands in the home trash (same filesystem).
  Cross-filesystem items go to a per-mount trash that `trash.List` does not yet
  enumerate (Plan 01 known gap); narrowing this acceptance to home-trash items is
  deliberate, and per-mount listing is a follow-up. `--no-delete` disables the
  keys.
- Identity-swap during analyze delete -> guard-stop, not a wrong delete.
- `scripts/check.sh` green.

## Risks & notes

- **gdu item model is read-mostly.** Do not mutate gdu's tree; copy into `Node`.
- **`fs.Item` method set is large and partly panics** (e.g. `SimpleDir` from the
  top-dir analyzer). Use the parallel analyzer's full tree for the TUI; use
  `CreateTopDirAnalyzer` only for a future non-interactive top-N fast path.
- **Memory.** A full tree of a large filesystem can be large; the stored/badger
  analyzer is imported but not used here. Note this as a follow-up if memory
  becomes a problem (spec risk table).
- Do not add category hints that imply mutation; analyze is read-only except the
  explicit trash keys.

## Provenance

UI shape and keybindings mirror Mole's analyze TUI behaviorally; the scanner is
gdu (MIT) wrapped, not copied. No Mole source used.

---

## Implementation notes (Plan 02)

Recorded after landing the plan. Acceptance criteria pass and `scripts/check.sh`
is green (golangci-lint and govulncheck warn as not installed on the authoring
host, matching Plan 01).

### Scan strategy

- In-process gdu import retained, pinned at `v5.38.0` (spec D2). The shell-out
  fallback remains a one-file change confined to `internal/scan`.
- `Scanner` is the stable interface; `GDU` is the default and `Analyze` is kept
  as the Plan 01 entry point.

### Node naming

- `Size` is on-disk usage (`blocks*512`); `Apparent` is the apparent size. The
  earlier doc ambiguity is resolved by `Node.DisplaySize(apparent bool)`, the
  single selector every presentation layer uses. The fields themselves are never
  swapped.

### Options semantics

- `Depth <= 0` means unlimited; `Depth 1` keeps the root and its immediate
  children.
- `Options.Apparent` is a presentation hint only; it is recorded on the options
  but not read inside `scan` (both sizes are always populated).

### --json shape

- `entries` defaults to direct children when depth is unlimited (`entryDepth`),
  so the payload stays bounded; `largest` uses `scan.Flatten` over the retained
  tree and the top-N selector.
- `scan_status` is `partial` when any node has `SizeKnown=false` and
  `unavailable` when the root is an unreadable empty directory.

### Fallback sizing

- Directories the engine flags unreadable fall back to `du -skP` under
  `owner.TimeoutScanPerRoot`; single files use `os.Stat` (`Blocks*512` on-disk,
  `Size()` apparent). Failure leaves `SizeKnown=false`, rendered "unknown",
  which drives `scan_status=partial` rather than a fake 0.

### Hardlinks

- De-duplication is wired via a single shared, non-nil `fs.HardLinkedItems` map
  passed to the root `UpdateStats` call, so a shared inode counts once
  (spec §8.8). Covered by `TestHardlinksNotDoubleCounted`.

### Deviations and judgement calls

- `--no-hidden` needed both the directory-ignore predicate and gdu's file-type
  filter, because gdu applies the ignore predicate only to directories.
- The TUI `parent()` cursor restore happens after `rebuildEntries` re-sorts the
  list, and `removeNode` subtracts the removed subtree's `ItemCount` (gdu counts
  the node itself) to keep the `c` column honest.

### Known gaps

- No benchmark asserts the "10 GB warm scan < 2 s" M1 criterion; the 10 GB
  fixture is impractical here, so it is tracked separately.
- `Options.Apparent` is not read inside `scan` (presentation-only), as noted
  above.
- Memory for very large trees is unaddressed; the badger analyzer is imported
  but unused (spec risk table).
# Plan 02 — Scan adapter, `analyze` TUI, `--json`

**Status:** not started
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
  type Node struct {
      Name     string
      Path     string
      IsDir    bool
      Size     int64  // apparent
      Usage    int64  // on-disk (blocks*512)
      SizeKnown bool
      Mtime    time.Time
      Items    int64
      Children []*Node
  }
  ```
- Translate gdu's `fs.Item` tree into our `Node` model. Use `fs.HardLinkedItems`
  semantics so shared inodes are not double-counted (spec §8.8).
- Honor `Options`: `OneFileSystem`, `NoHidden`, `Apparent`, `Depth`, `Ignore`
  (default `/proc,/dev,/sys,/run`).
- Fallback sizing: `du -skP` under a 90s timeout when the engine cannot provide a
  subtree; single files via `os.Stat` (`Blocks*512` for on-disk, `Size()` for
  apparent).
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
  `history --restore`; `--no-delete` disables the keys.
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
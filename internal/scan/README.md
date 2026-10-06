# internal/scan — gdu adapter and spike result

All gdu usage is confined to this package. Other packages consume burrow's own
`scan.Node` model, so replacing the in-process analyzer with a `gdu --json`
shell-out is a one-file change (spec D2, risk table).

## Spike: in-process import of `github.com/dundee/gdu/v5`

Pinned version: **v5.38.0** (MIT). Resolved: `go 1.26.0` is required, so
`go.mod` pins `go 1.26.0` and `toolchain go1.26.8`; builds do not rely on the
developer's ambient Go version.

Findings (measured on Fedora 44, Go 1.26.8, `CGO_ENABLED=0`):

1. **Importable from an external module.** `analyze.CreateAnalyzer()` and
   `analyze.CreateTopDirAnalyzer()` are exported and callable.
2. **The `internal/common` type leak is avoidable.** `AnalyzeDir` takes
   `common.ShouldDirBeIgnored` and `common.ShouldFileBeIgnored`, which cannot be
   named outside gdu's module. Passing an untyped func literal
   `func(name, path string) bool { ... }` and `nil` compiles cleanly. See
   `makeIgnore` in `scan.go`. Do not vendor or fork gdu internals.
3. **Import cost is real but accepted.** Importing `pkg/analyze` compiles the
   whole package, so these land in the binary even though only the in-memory
   analyzer is used:
   - `github.com/dgraph-io/badger/v4` (stored.go)
   - `modernc.org/sqlite` (sqlite.go)
   - `github.com/gdamore/tcell/v2`, `github.com/rivo/tview` (via internal/common)
   - `github.com/sirupsen/logrus`, `github.com/ulikunitz/xz`, `github.com/maruel/natural`
4. **Measured binary/build:** statically linked (`not a dynamic executable`),
   **~4.9 MiB** stripped of nothing, cold build (clean cache, `CGO_ENABLED=0`)
   **~1.9 s**, forced rebuild `go build -a` **~1.8 s**.
5. `AnalyzeDir` builds the tree but does **not** aggregate sizes; the caller
   must call `item.UpdateStats(fs.HardLinkedItems{})` at the root before reading
   `GetSize`/`GetUsage`. `Analyze` does this.

## Decision

**Keep the in-process import of pinned v5.38.0.** The func-literal workaround
compiles and the cost (~5 MiB static binary, ~2 s build) is acceptable for a
one-shot CLI. If the API ever stops compiling, switch to the `gdu --json`
shell-out adapter without changing any other package.

## Plan 02 additions

- `Scanner` is the stable interface; `GDU` is the default implementation and
  `Analyze` remains as the Plan 01 entry point.
- `Options` gained `Apparent` (a presentation hint) and `Depth`. `Node` gained
  `SizeKnown`.
- Hardlink de-duplication is wired: a single non-nil `fs.HardLinkedItems` map is
  shared across the root `UpdateStats` call, so a shared inode counts once
  (spec §8.8). Covered by `TestHardlinksNotDoubleCounted`.
- Fallback sizing: `du -skP` (bounded by `owner.TimeoutScanPerRoot`) for
  directories the engine flags unreadable, and `os.Stat` (`Blocks*512` on-disk,
  `Size()` apparent) for single files. Failure leaves `SizeKnown=false`, which
  renders "unknown" and drives `scan_status=partial` rather than showing 0.
- `--no-hidden` needs both the directory-ignore predicate and the file-type
  filter: gdu applies the ignore predicate only to directories, so hidden files
  are filtered by the third `AnalyzeDir` argument.
- Per-root deadline is `owner.TimeoutScanPerRoot` (90s); cancellation is via
  `ctx` and `analyzer.Cancel()`.

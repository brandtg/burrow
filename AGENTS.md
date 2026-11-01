# AGENTS.md — working notes for coding agents

This file is for agents (and humans) doing implementation. It complements
README.md (what the project is) and docs/user-guide.md (how to use it). The
2026-10-31 audit lives at docs/audit-2026-10-31.md.

## Read first

1. README.md — project intent and architecture.
2. docs/user-guide.md — the user-facing contract.
3. docs/audit-2026-10-31.md — the deep-dive that drove the current shape.

There is no spec, no plans directory, and no status doc; do not cite or
restore them.

## Current state

The product is one clean flow: bare `burrow` (or `burrow clean`) scans the
catalog, shows a grouped preview on the terminal, asks for confirmation, and
runs exactly what was confirmed. `analyze` is a read-only disk explorer.
`history --restore` undoes trash-contract runs from the JSONL journal.
`purge` is a hidden deprecated alias for `burrow clean --only projects`.

- Packages: internal/{action,catalog,cli,config,disk,distro,engine,journal,
  owner,safe,scan,trash,tui,whitelist} and cmd/burrow. Catalog subpackages:
  catalog/{containers,desktoppkgs,dev,downloads,projects,roots,system,usercache}.
- internal/engine holds the `Env`, the Target/Action/Category/Guard/Recovery/
  Risk contract, and the full run pipeline: Build = discover/measure/filter/
  plan (read-only), Execute = guard/re-validate/run/journal/summary.
  `engine.ApplyHooks` runs Reconciler/Warner hooks; `Options.Progress` and
  `ExecuteWithProgress` carry scan/execute feedback to the CLI.
- Recovery contracts: `trash`, `owner`, and `delete`. `delete` exists only for
  emptying the Trash — the one permanent hand mutation, journaled as
  `recovery: "delete"` and never offered by `history --restore`.
- The clean-only selection flags (`--only`, `--exclude`, `--min-size`,
  `--no-owner`, `--age`) are not persistent; `analyze` and `history` reject
  them. `[purge]` in config.toml is read as a deprecated alias of
  `[projects]`; the Go field is `Cfg.Purge`.
- catalog/roots enumerates the directories the clean flow can reclaim; the
  analyze TUI uses it for the `[R]` marker and the `burrow clean --only`
  hint. It is a separate package because the category packages import
  internal/catalog, which cannot import them back.

## Non-negotiable safety rules

These override convenience. Violating one is a bug even if tests pass.

- No mutation before the preview step. Discovery, measurement, filtering, and
  planning are read-only.
- Every mutation declares a recovery contract: `trash`, `owner`, or `delete`.
- Never delete an arbitrary path. Deletion is only inside allow-listed cache
  roots or produced by an owner command. There is no generic delete primitive
  and no `--permanent` flag.
- Owner commands come from the catalog by exact argv. Never build a command
  from user input, file contents, or discovered names. Package lists derived
  from owner output must be validated against the parsed set before use.
- Fail closed. If you cannot prove an action is safe right now, skip it and
  record why. Never downgrade a recoverable delete to a permanent one silently.
- Never run the whole process as root. If `geteuid()==0` and `SUDO_USER` is
  unset, refuse. Elevate per-command via `sudo -- <exact argv>`.
- Raw `os.Remove`, `os.RemoveAll`, `os.Truncate`, and `syscall.Unlink` are
  allowed only inside internal/safe, internal/trash, and internal/owner. The
  static AST gate (scripts/safety-gate, wired into scripts/check.sh and
  `make safety-gate`) enforces this; a planted `os.Remove` elsewhere must
  fail it.
- There is exactly one permanent mutation in the binary: emptying the Trash
  (RecoveryDelete). There is no `safe.RemoveTree`, no `trash.Remove`, and no
  way to delete an arbitrary path.

If a task seems to require breaking one of these, stop and flag it.

## gdu integration (read this before touching internal/scan)

The scan engine is github.com/dundee/gdu/v5 (MIT), pinned via the go.mod
toolchain. Four things to know:

1. `analyze.CreateAnalyzer()` and `analyze.CreateTopDirAnalyzer()` are the
   public constructors. `AnalyzeDir(path, ignore, fileTypeFilter)` takes types
   from gdu's `internal/common`, which an external module cannot name. Pass
   untyped func literals and nil instead of referencing those types.
2. Importing pkg/analyze compiles the whole package, including stored.go
   (badger) and sqlite.go (modernc/sqlite), so those dependencies land in the
   binary even though only the in-memory analyzer is used. Accepted; do not
   add more gdu packages than necessary.
3. `AnalyzeDir` builds the tree but does not aggregate sizes. The caller must
   call `item.UpdateStats(fs.HardLinkedItems{})` at the root before reading
   `GetSize`/`GetUsage`. A shared, non-nil map makes a shared inode count
   once. internal/scan does this.
4. There is **no** `gdu --json` shell-out. The real fallback when the engine
   cannot size a subtree is the `du -sk` repair path (internal/scan's
   `repair`/`duSize`). Do not claim the shell-out exists.

All gdu usage is confined to internal/scan. Other packages use the Node model.

## Known gaps

- internal/trash.List reads only the home trash, so `history --restore`
  misses items trashed on other mounts.
- `TestLockHeldViaProcLocks` (internal/distro) fails on hosts where
  /proc/locks does not report the process's own flocks (btrfs homes: stat's
  device is an anonymous subvolume device, /proc/locks lists the superblock
  device). The test skips itself there; do not "fix" it.

## Conventions

- Go, formatted with gofmt; lint with golangci-lint; vet; govulncheck.
- CGO_ENABLED=0; the binary must be statically linked.
- Do not add a dependency without checking what is already used and confirming
  it is justified. One size parser (config.ParseBytes), one disk-usage helper
  (internal/disk.Blocks512), one whitelist rule (prefix protection via
  whitelist.Set.Contains). Do not add second copies.
- Config lives at `$XDG_CONFIG_HOME/burrow/config.toml`; journal at
  `$XDG_STATE_HOME/burrow/operations.jsonl`; whitelist at
  `$XDG_CONFIG_HOME/burrow/whitelist`. Resolve the invoking user's HOME/XDG
  via SUDO_USER, never root's environment.
- Keep the tool distro-agnostic. Any distro-specific behavior keys off
  `distro.Info.Family()` via the internal/distro adapter. Fedora (dnf) and
  Debian/Ubuntu (apt) are exercised; pacman/zypper/apk are detection stubs.
- Version is stamped through `internal/journal.Version` via
  `-ldflags -X github.com/brandtg/burrow/internal/journal.Version=...`; the
  Makefile does this from `git describe`.

### Comments and provenance (intentional exception to the no-comments default)

Files that behaviorally port a concept from Mole (safety contract, keep/discard
reasoning for caches, install discipline) must carry a short provenance comment
naming the concept and stating it is an independent implementation. This is
required for GPL hygiene: Mole is GPL-3.0, burrow is Apache-2.0, and no Mole
source may be copied. Do not copy Mole code, comments, or function bodies; port
behavior only.

Do not add other comments unless they explain non-obvious safety reasoning.

## Build, test, and gates

Prefer the Makefile; `make help` lists targets. The canonical gate is
`make gate` (alias `make check`), which runs scripts/check.sh: gofmt check,
go vet, unit tests, the static "no direct destructive sink" AST gate,
govulncheck and golangci-lint (skip-if-missing unless
`BURROW_REQUIRE_TOOLS=1`), man-page freshness, and the static-binary check.

- `make gate` / `make check` — the canonical gate.
- `make gate-strict` — fails if golangci-lint/govulncheck are missing
  (`BURROW_REQUIRE_TOOLS=1`). CI runs this.
- `make doctor` / `make setup-tools` — check/install the external gate tools
  at the CI-pinned versions (`scripts/dev-tools.sh`).
- `make docs` — regenerate man pages into docs/man (the gate fails if stale;
  the repo must contain exactly the pages the generator emits).
- `make matrix` — container integration matrix (`scripts/matrix.sh`), requires
  docker/podman; the gate runs it only with `BURROW_MATRIX=1`.
- `make integration` — root-only loopback cross-device trash test. Not part
  of the default gate.
- `make verify-static`, `make test`, `make test-race`, `make fuzz`, `make fmt`,
  `make fmt-check`, `make vet`, `make lint`, `make vuln`, `make safety-gate`,
  `make build`, `make build-all`, `make run ARGS="..."`, `make install`,
  `make clean`, `make tidy`.

Raw equivalents, if you are not using make:

```
go build ./... && go test ./... && go vet ./... && gofmt -l .
go run ./scripts/safety-gate .   # exit 1 if a sink is outside safe/trash/owner
```

The static gate is an AST checker (not grep): it maps import aliases and flags
`os.Remove`, `os.RemoveAll`, `os.Truncate`, `syscall.Unlink/Unlinkat/Rmdir/
Ftruncate/Truncate`, and the `golang.org/x/sys/unix` equivalents outside
internal/safe, internal/trash, internal/owner, and itself. It skips `_test.go`
files. A planted `os.Remove` outside those packages must fail it. Known
limitation: a sink assigned to a variable first
(`remove := os.Remove; remove(p)`) is not detected.

## Testing expectations

- internal/safe: table + fuzz tests; property: never ok for a path outside an
  allowed root. Identity snapshot: simulate a swap and expect guard-stop.
- internal/trash: golden .trashinfo, round-trip, collision, cross-device.
- Distro adapters: fixture os-release + stub command table -> assert exact
  argv. internal/owner/commands_test.go covers every allow-listed argv row.
- Journal: schema, run_id grouping, restore only touches trash items.
- Integration matrix: containers with testbin/ stubs recording argv and
  emitting canned output. Never mutate a real host.
- Golden CLI and --json output (testdata/); review diffs, do not auto-accept.
- scan tests cover the deadline, the du -sk repair path, and
  Status/StatusReason.

## Commits

- Do not commit unless explicitly asked.
- When asked, keep the message concise and match the repo style. Stage only
  intended files. Never commit secrets or a journal/config containing real
  paths.

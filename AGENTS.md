# AGENTS.md — working notes for coding agents

This file is for agents (and humans) doing the implementation. It complements
README.md (what the project is) and docs/burrow-spec.md (the product contract).
Read all three before writing code.

## Read first

1. README.md — project intent and architecture.
2. docs/burrow-spec.md — the authoritative product spec and locked decisions.
3. docs/plans/README.md — dependency graph, execution waves, per-plan file
   ownership.
4. docs/status.md — current implementation status, known gaps, and open findings.
5. The plan you are executing, e.g. docs/plans/03-clean-engine-system-apt.md.

Do not re-litigate the locked decisions (spec §2) or the spec's stated decisions
unless you find concrete evidence they are wrong. If you do, say so explicitly
rather than silently deviating.

## Current state (v0.1 complete)

All seven plans (01-07) have landed and `make gate` is green. The tool builds
statically and works end-to-end. Read docs/status.md for the audit, deviations,
known gaps, and the open findings list (F1-F6) before changing anything.

- Packages: internal/{i18n,safe,trash,journal,config,whitelist,owner,distro,
  engine,scan,metrics,action,catalog,purge,tui,cli} and cmd/burrow.
- internal/engine holds the `Env`, the Target/Action/Category/Guard/Recovery/Risk
  contract (spec §6.2), `engine.Resolve`, and the full run pipeline
  (Build = discover/measure/filter/plan, Execute = guard/re-validate/run/journal/
  summary). `engine.Action` carries `Recovery() Recovery` beyond the §6.2
  signature so I2 is enforced at the action boundary; keep it.
- Catalog: `system` (apt + dnf via `distro.SystemSpec`), `dev`, `user-cache`,
  `downloads`, `containers`, `desktop-pkgs`. `purge` is a separate subsystem, not
  part of the catalog.
- CLI: `version`, `config`, `history`, `analyze`, `clean`, `purge`, `status`,
  `docs`, `completion` all work. `--json` is stable on clean/analyze/history/
  status; goldens live under testdata/.
- `internal/safe` built-in protected roots protect the roots themselves;
  config `clean.protected_paths` entries are prefix-protected (merged in by the
  engine). `/home` itself is protected but its subtrees are not.
- `internal/trash.List` reads only the home trash; per-mount trashes are not
  enumerated (so `history --restore` misses cross-filesystem items). Known gap.
- Hardlink-aware sizing is wired (one shared `fs.HardLinkedItems` map at the root
  `UpdateStats`; see gdu note 4).
- Invariant tests live in `internal/safe/safety_test.go`; the static gate has its
  own tests in `scripts/safety-gate/check_test.go`. The container matrix lives in
  `scripts/matrix.sh` + `scripts/matrix-case.sh`.

## Non-negotiable safety rules

These override convenience. Violating one is a bug even if tests pass.

- No mutation before the preview step (I1). Discovery, measurement, filtering, and
  planning are read-only.
- Every mutation declares a recovery contract: `trash` or `owner` (I2).
- Never delete an arbitrary path. Deletion is only inside allow-listed cache roots
  or produced by an owner command (I3). There is no generic delete primitive.
- Owner commands come from the catalog by exact argv. Never build a command from
  user input, file contents, or discovered names (I4). Package lists derived from
  owner output must be validated against the parsed set before use.
- Fail closed. If you cannot prove an action is safe right now, skip it and record
  why (I5). Never downgrade to a permanent delete silently.
- Never run the whole process as root. If `geteuid()==0` and `SUDO_USER` is unset,
  refuse. Elevate per-command via `sudo -- <exact argv>` (I6).
- Privileged deletes must not traverse a user-mutable ancestor (I7).
- Raw `os.Remove`, `os.RemoveAll`, `os.Truncate`, and `syscall.Unlink` are allowed
  only inside internal/safe, internal/trash, and internal/owner. A static gate
  (scripts/check.sh) enforces this. Purge deletes go through internal/safe.
- There are exactly two permanent-delete primitives, and both are deliberate:
  - `safe.RemoveTree` is purge-only: it may be called only from internal/purge
    (plus its definition in internal/safe). Trash-contract code must never call
    it, or it would silently downgrade a recoverable delete to a permanent one
    (I5). The static gate enforces this confinement, including inside
    internal/trash and internal/owner.
  - `trash.Remove` is the explicit `clean --permanent` escape hatch (spec §5.1,
    §7.4). It is reachable only when the user passes `--permanent` and the target
    has already been validated and snapshotted. A permanent item is labeled
    `[delete]` in the preview and journaled as `action: "delete"` /
    `recovery: "delete"`, never as trash, so it is never offered by
    `history --restore`.

If a task seems to require breaking one of these, stop and flag it.

## gdu integration (read this before touching internal/scan)

The scan engine is github.com/dundee/gdu/v5 (MIT), pinned to v5.38.0. Three
things to know:

1. `analyze.CreateAnalyzer()` and `analyze.CreateTopDirAnalyzer()` are the public
   constructors. `AnalyzeDir(path, ignore, fileTypeFilter)` takes types from
   gdu's `internal/common`, which an external module cannot name. Pass untyped
   func literals (`func(name, path string) bool { ... }`) and nil instead of
   trying to reference the types. If this ever stops compiling, do not vendor or
   fork gdu internals — switch internal/scan to the `gdu --json` shell-out.
2. Importing `pkg/analyze` compiles the whole package, including stored.go
   (badger) and sqlite.go (modernc/sqlite), so those dependencies land in the
   binary even though we only use the in-memory analyzer. This is accepted; do
   not add more gdu packages than necessary.
3. gdu v5.38.0 requires Go 1.26.0. The toolchain is pinned in go.mod
   (`go 1.26.0`, `toolchain go1.26.8`); do not rely on the developer's ambient Go
   version.
4. `AnalyzeDir` builds the tree but does **not** aggregate sizes. The caller must
   call `item.UpdateStats(fs.HardLinkedItems{})` at the root before reading
   `GetSize`/`GetUsage`. `internal/scan.Analyze` does this. Hardlink
   de-duplication is wired: one shared, non-nil `fs.HardLinkedItems` map is passed
   to the root `UpdateStats`, so a shared inode counts once (spec §8.8, Plan 02).

All gdu usage is confined to internal/scan. Other packages use our own Node/item
model. This keeps a shell-out fallback to a one-file change. The spike result and
measured cost are recorded in internal/scan/README.md.

## Conventions

- Go, formatted with gofmt; lint with golangci-lint; vet; govulncheck.
- CGO_ENABLED=0; the binary must be statically linked.
- Follow existing patterns in the codebase. Do not add a dependency without
  checking what is already used and confirming it is justified.
- Config lives at `$XDG_CONFIG_HOME/burrow/config.toml`; journal at
  `$XDG_STATE_HOME/burrow/operations.jsonl`; whitelist at
  `$XDG_CONFIG_HOME/burrow/whitelist`. Resolve the invoking user's HOME/XDG via
  SUDO_USER, never root's environment.
- Keep the tool distro-agnostic. Any distro-specific behavior (package-manager
  commands, lock paths, cache locations) must key off `distro.Info.Family()`
  (apt/dnf/pacman/zypper/apk) via the `internal/distro` adapter. Never hardcode a
  Fedora-, Debian-, or Arch-only assumption outside an adapter. Fedora (dnf) and
  Debian/Ubuntu (apt) are the exercised paths; the others are detection stubs
  until their plans land.
- Prefer editing existing files over creating new ones.
- Version is stamped through `internal/journal.Version` via
  `-ldflags -X github.com/brandtg/burrow/internal/journal.Version=...`; the
  Makefile does this from `git describe`.

### Comments and provenance (intentional exception to the no-comments default)

The usual "do not add comments" rule is overridden here for one purpose: files
that behaviorally port a concept from Mole (safety contract, keep/discard
reasoning for caches, purge git protection, install discipline) must carry a short
provenance comment naming the concept and stating it is an independent
implementation. This is required for GPL hygiene: Mole is GPL-3.0, burrow is
Apache-2.0, and no Mole source may be copied. Do not copy Mole code, comments, or
function bodies; port behavior only.

Do not add other comments unless they explain non-obvious safety reasoning.

## Build, test, and gates

Prefer the Makefile: run `make` (or `make help`) to list targets. The canonical
gate is `make gate` (alias `make check`), which runs `scripts/check.sh`:
gofmt check, `go vet`, unit tests, and the static "no direct destructive sink"
gate. `golangci-lint` and `govulncheck` run when installed and are skipped with a
warning otherwise.

- `make gate` — canonical gate (equivalent to `./scripts/check.sh`).
- `make gate-strict` — same, but fail if golangci-lint/govulncheck/goreleaser are
  missing (`BURROW_REQUIRE_TOOLS=1`). CI (`.github/workflows/ci.yml`) runs this.
- `make matrix` — container integration matrix (`scripts/matrix.sh`); requires
  docker/podman. CI runs it as a separate job. The gate runs it only when
  `BURROW_MATRIX=1` (skips with a recorded reason if no runtime).
- `make integration` — root-only loopback cross-device trash test
  (`BURROW_INTEGRATION=1 ./scripts/check.sh`). Not part of the default gate.
- `make docs` — regenerate man pages into docs/man (the gate fails if stale).
- `make verify-static` — assert the built binary has no dynamic loader deps.
- `make test`, `make test-race`, `make fuzz`, `make fmt`, `make fmt-check`,
  `make vet`, `make lint`, `make vuln`, `make safety-gate`, `make build`,
  `make build-all`, `make run ARGS="..."`, `make install`, `make clean`.

Raw equivalents, if you are not using make:

```
go build ./... && go test ./... && go vet ./... && gofmt -l .
go run ./scripts/safety-gate .   # exit 1 if a sink is outside safe/trash/owner
```

The static gate is an AST checker (not grep): it maps import aliases and flags
`os.Remove`, `os.RemoveAll`, `os.Truncate`, `syscall.Unlink/Unlinkat/Rmdir/
Ftruncate/Truncate`, and the `golang.org/x/sys/unix` equivalents outside
internal/safe, internal/trash, internal/owner, and itself. It also flags any
method call named `Truncate` (e.g. `(*os.File).Truncate`) and bare calls from a
dot-import of a forbidden package. It skips `_test.go` files. A planted
`os.Remove` outside those packages must fail it. Known limitation: a sink
assigned to a variable first (`remove := os.Remove; remove(p)`) is not detected,
and the method heuristic can false-positive on `bytes.Buffer.Truncate`.

Testing expectations (spec §14):

- internal/safe: table + fuzz tests; property: never ok for a path outside an
  allowed root.
- Identity snapshot: simulate a swap and expect guard-stop.
- internal/trash: golden .trashinfo, round-trip, collision, cross-device.
- Distro adapters: fixture os-release + stub command table -> assert exact argv.
- Journal: schema, run_id grouping, restore only touches trash items.
- Integration matrix: containers with testbin/ stubs recording argv and emitting
  canned output. Never mutate a real host.
- Golden CLI and --json output; review diffs, do not auto-accept.
- safety_test.go covers I1-I7 and fails if an invariant is violated.

## Plan-based workflow

- Work one plan at a time. Respect the dependency graph in docs/plans/README.md.
- A plan owns specific files (see the ownership map in that README). If you need
  to change a file owned by another plan, do not edit it inline: record it as a
  cross-plan dependency and let the owning plan land it.
- Keep the engine and catalog framework (Plan 03) backward compatible. Later
  catalog plans add data and small Discover functions; they do not reshape the
  framework.
- Update the Status column in docs/plans/README.md as you go.
- A plan is done only when its acceptance criteria pass and scripts/check.sh is
  green.

## Definition of done (v0.1, spec §19)

- analyze, clean, purge, history, config work on Ubuntu 24.04 and Debian 12.
- --dry-run, whitelist, preview+review, and the JSON journal are present and
  tested.
- Safety invariants I1-I7 have passing tests; container matrix green;
  govulncheck clean.
- No mutation path bypasses safe/trash/owner (static gate in CI).
- High-risk items ship OFF; nothing auto-runs; no daemon.
- README documents every catalog item with its owner command, recovery contract,
  and risk.

v0.1 status against this list, with the deviations and remaining gaps, is in
docs/status.md. The known open items are F3 (I7 helper is vacuous/dead code) and
F4 (the static gate does not cover `trash.Remove`); F1, F2, F5, and F6 are fixed.

## Commits

- Do not commit unless explicitly asked.
- When asked, keep the message concise and match the repo style. Stage only
  intended files. Never commit secrets or a journal/config containing real paths.
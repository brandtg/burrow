# Plan 06 — `purge` (rebuildable project artifacts)

**Status:** done
**Milestone:** M5
**Depends on:** 01, 02
**Blocks:** 07
**Parallel with:** 04, 05

## Goal

Ship `burrow purge`: find rebuildable build artifacts (`node_modules`, Rust
`target`, virtualenvs, `__pycache__`, etc.) across configured project roots and
delete them permanently — but only after proving they are not git-tracked and are
not themselves a repository. This ports Mole's highest-value dev feature and is
largely independent of the `clean` engine.

## Deliverables

```
internal/purge/            discovery, git protection, age filter, sizing
internal/tui/purge.go      grouped-by-project selection UI
internal/cli/purge.go      command + flags
internal/config/           [purge] paths, artifact_dirs, max_age
testdata/purge/            git repo fixtures (tracked, untracked, nested repo)
```

## Tasks

### 1. Artifact patterns
- v0.1 pinned list: `node_modules`, `target` (Rust), `.venv`/`venv`,
  `__pycache__`, `.next/cache`, `build`, `dist`, `.gradle`, `_cacache`.
- Extensible via `[purge] artifact_dirs` in config (spec §11, §13.2).
- Match by directory name at any depth within a project root; do not match a
  regular file.

### 2. Discovery
- Scan configured project roots (default `~/Projects`, `~/src`, `~/dev`, `~/code`,
  `~/work`; override via `purge_paths`).
- Bound depth (default 6). Prune heavy subtrees during the scan (do not descend
  into an artifact directory once matched; do not descend into `.git`).
- Read-only; no mutation during discovery.

### 3. Git protection (the crux)
- For any candidate inside a git repo, if it is git-tracked **or git state is
  unknown**, keep it. Unknown => keep (fail closed).
- Use **one `git ls-files -z` per repo per run** (bounded), not per candidate.
  Cache the tracked set keyed by repo root.
- Keep directories that are themselves a repo or contain a nested repo.
- Detect repo boundaries via `.git` directory/file; use `git -C <root> rev-parse
  --show-toplevel` to find the owning repo.
- Timeout git calls from the central table; timeout => treat as unknown => keep.

### 4. Age filter
- Default 7d of no file activity (mtime), overridable via `--age`.
- A candidate's age is the newest mtime within the artifact tree (so an active
  build is not purged).
- Cannot verify mtime => keep.

### 5. Action
- Permanent delete of the artifact directory (rebuildable; owner-ish contract).
  Confirm in preview; `--dry-run` supported.
- This is the one place a raw delete is legitimate; it must still route through
  `internal/safe` validation and the identity snapshot, and be recorded in the
  journal with `action: "purge"` and `recovery: "owner"`.
- `--yes` required when not a TTY.

### 6. UI + flags (`internal/tui/purge.go`, `internal/cli/purge.go`)
- Grouped by project, with artifact type + age + size.
- Keys per spec §10, plus `[`/`]` project jump, `/` search, `space`/`a` selection.
- Flags per spec §5.4: `--paths` (print configured roots; edit them in
  config.toml or via `BURROW_PURGE_PATHS`), `--age`, `--yes`, plus globals.

## Acceptance criteria

- git-tracked fixture artifact is **kept**.
- Untracked artifact is purged.
- Nested-repo fixture is kept.
- Unknown-git-state fixture is kept.
- Age filter: recently-modified artifact kept; stale artifact purged.
- `--dry-run` lists candidates, deletes nothing.
- `--paths` prints the configured roots (editing is via config/env; there is no
  in-tool editor in v0.1).
- Purge deletes are validated by `internal/safe` and journaled; the static safety
  gate still passes (delete flows through `internal/safe`, not raw `os.Remove` in
  the purge package).
- `scripts/check.sh` green.

## Risks & notes

- **Permanent deletion.** Unlike `clean`, purge does not use Trash by default
  (artifacts are rebuildable). The preview must be explicit about this, and the
  confirmation screen must list exact paths.
- **git ls-files cost.** On large repos this is the expensive part; cache per repo
  and bound the call. Never run per candidate.
- **Symlinks.** Do not follow symlinked artifact dirs out of the project root;
  validate the resolved path stays within the root.
- **Monorepos / workspaces.** A `node_modules` may be hoisted; keep nested-repo
  and tracked protections conservative.
- Independent of the `clean` catalog: do not couple purge items into
  `internal/catalog`; it has its own discovery and its own contract.

## Provenance

Behavioral port of Mole's purge rule (git-tracked-file protection, nested-repo
protection, age filter). Reimplemented clean-room. No Mole source copied.

---

## Implementation notes (Plan 06)

Recorded after landing the plan. Acceptance criteria pass and `scripts/check.sh`
is green (golangci-lint and govulncheck warn as not installed on the authoring
host, matching Plans 01/02).

### Discovery model

- `purge.Discover` walks each root with `filepath.WalkDir`, bounded by
  `MaxDepth` (default 6). A matched artifact directory is recorded and pruned
  (`SkipDir`), so a `node_modules` inside a `node_modules` is never reported
  twice. `.git` is skipped. Symlinks are not followed (WalkDir does not descend
  into them), and a symlinked artifact root never reaches `classify`.
- Roots that are missing, unreadable, or not directories are skipped with a
  warning through `Options.Warn`; the run continues. The CLI wires `Warn` to
  stderr, so a typo'd root is visible instead of silently ignored.

### Artifact match rule

- A single-segment pattern matches the directory base name at any depth
  (`node_modules`, `target`, ...). A multi-segment pattern matches a path suffix
  relative to the root (`.next/cache` matches `x/.next/cache`). Regular files
  never match; only directories are tested.

### Git protection

- The owning repository is found by walking up for a `.git` entry (directory or
  worktree/submodule file), then confirmed once with
  `git -C <marker> rev-parse --show-toplevel`. Tracked paths come from one
  `git ls-files -z` per repository per run; `gitCache` memoizes both the
  top-level lookup and the tracked set, so the call count is per repo, not per
  candidate.
- The tracked set indexes every directory containing a tracked file, so a
  candidate is classified in constant time. An artifact is kept when it is
  git-tracked, when git state is unknown (git missing, timeout, non-zero exit
  other than "not a repository"), or when the artifact tree contains a nested
  `.git` (detected during the sizing walk). All three are fail-closed.

### Age filter

- A candidate's age is the newest mtime anywhere in its tree, captured in the
  same single walk that sizes it. It is kept when the newest activity is newer
  than `MaxAge` (default 7d) and when the age cannot be verified (measurement
  failure or a zero mtime). `--age` overrides the config value; `--age 0`
  disables the filter.

### Deletion path

- Deletion goes through `safe.RemoveTree`, the single permanent-delete
  primitive. The validator is an allow-list bound to the resolved project roots
  (`RequireRoot`), with config `protected_paths` and the built-in protected
  roots merged in, plus an ownership check. `RemoveTree` re-runs the validator,
  re-checks the identity snapshot taken at discovery, and only then calls
  `os.RemoveAll`.
- The journal records `action: "purge"`, `recovery: "owner"`, the artifact type
  as `item`, the absolute path, and the on-disk bytes. `history` shows purge
  runs; `history --restore` never touches them (only `action: "trash"` records
  are restorable).
- The CLI requires `--yes` when stdout is not a terminal; on a TTY it opens the
  grouped-by-project selection model. `--dry-run` records `dry-run` rows and
  deletes nothing.

### Symlinked project roots

- `purgeRoots` resolves each configured root with `filepath.EvalSymlinks`
  (keeping the lexical form when a root does not exist yet) and uses that same
  resolved list for both discovery and the deletion allow-list. Without this,
  discovery reported artifacts at their physical path while the validator held
  the lexical symlink, so every artifact under a symlinked root was refused with
  "outside the allow-listed roots" (fail-closed but a silent no-op). Covered by
  `TestPurgeSymlinkedRoot`.

### RemoveTree confinement gate

- `scripts/safety-gate` gained a narrower rule beyond the general sink check:
  `safe.RemoveTree` may only be referenced from `internal/purge` (and is defined
  in `internal/safe`). The rule is evaluated even inside the packages allowed to
  call general destructive sinks (`internal/trash`, `internal/owner`), so a
  future trash-contract caller cannot silently downgrade to a permanent delete
  (invariant I5). Covered by `TestGateFlagsRemoveTreeOutsidePurge`,
  `TestGateFlagsRemoveTreeInAllowedSinkPackage`, `TestGateAllowsRemoveTreeInPurge`,
  and `TestGateFlagsAliasedRemoveTree`.

### Known gaps

- `--paths` prints the effective roots only; there is no in-tool editor in v0.1
  (edit `config.toml` or set `BURROW_PURGE_PATHS`).
- `safe.RemoveTree` has no cross-filesystem or mount-boundary check: it will
  traverse a bind mount inside an allowed root. Purge is user-owned and
  non-privileged, so the blast radius is bounded by the configured roots.
- The nested-repo rule protects an artifact that *is* or *contains* a repo. An
  artifact that merely *sits inside* a nested repo is governed by the ordinary
  tracked-file check; a nested repo's untracked artifacts are still purgeable.
- No benchmark covers purge over a very large monorepo; git cost is bounded per
  repo but the sizing walk is proportional to artifact contents.
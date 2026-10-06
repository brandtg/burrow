# Plan 06 — `purge` (rebuildable project artifacts)

**Status:** not started
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
- Flags per spec §5.4: `--paths` (print/edit configured roots), `--age`,
  `--yes`, plus globals.

## Acceptance criteria

- git-tracked fixture artifact is **kept**.
- Untracked artifact is purged.
- Nested-repo fixture is kept.
- Unknown-git-state fixture is kept.
- Age filter: recently-modified artifact kept; stale artifact purged.
- `--dry-run` lists candidates, deletes nothing.
- `--paths` prints/edits the configured roots.
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
# Plan 04 — `dev`, `user-cache`, `downloads` catalogs + whitelist manager

**Status:** done
**Milestone:** M3
**Depends on:** 03
**Blocks:** 07
**Parallel with:** 05, 06

## Goal

Add the developer/toolchain caches (the category that differentiates `burrow` from
BleachBit), the XDG user-cache sweep, the downloads/installer sweep, and the
interactive whitelist manager. Every item either drives its owner command or
trashes a documented cache root; nothing hand-deletes package-manager state.

## Deliverables

```
internal/catalog/dev/          language/toolchain items (spec §8.4)
internal/catalog/usercache/    XDG cache sweep (spec §8.5)
internal/catalog/downloads/    installer/content sweep (spec §8.6)
internal/owner/commands.go     additional per-user command tables
internal/whitelist/manager.go  interactive add/remove
internal/cli/clean.go          wire --whitelist
testdata/dev/                  fixtures + keep/discard assertions
```

## Tasks

### 1. `dev` category (spec §8.4)
Implement each row. Prefer the owner command; fall back to trashing the cache
root **only** when the owner command is unavailable and the root is a documented
cache. Preserve the keep/discard reasoning from Mole behaviorally, with a short
provenance comment per item:

| Item | Detect | Action | Rec | Guard | Default |
|---|---|---|---|---|---|
| `dev.npm` | `npm` on PATH; `npm config get cache` | `npm cache clean --force` | owner | npm idle | on |
| `dev.pnpm` | `pnpm` on PATH; `pnpm store path` | `pnpm store prune` | owner | pnpm install idle | on |
| `dev.yarn` | `~/.yarn/cache`, `~/.cache/yarn` | Trash contents | trash | — | on |
| `dev.bun` | `bun` on PATH; `bun pm cache` | `bun pm cache rm` | owner | — | on |
| `dev.go-mod` | `go env GOMODCACHE` | `go clean -modcache` | owner | go/gopls idle | on |
| `dev.go-build` | `go env GOCACHE` | `go clean -cache` | owner | Go documents concurrent-safe | on |
| `dev.rust-registry-cache` | `$CARGO_HOME/registry/cache` | Trash this subtree only | owner | cargo/rustc idle | on |
| `dev.rustup-downloads` | `$RUSTUP_HOME/downloads` | Trash contents | owner | — | on |
| `dev.pip` | `pip3 cache dir` | `pip3 cache purge` | owner | — | on |
| `dev.uv` | `uv` on PATH; `uv cache dir` | `uv cache prune` | owner | — | on |
| `dev.poetry` | `~/.cache/pypoetry/{artifacts,cache}` | Trash those two | trash | — | on |
| `dev.conda` | conda on PATH | `conda clean --yes --index-cache --tarballs --logfiles` | owner | — | on |
| `dev.gem` | `~/.gem/ruby/*/cache`, `~/.bundle/cache` | Trash contents | trash | — | on |
| `dev.rbenv` | `~/.rbenv/cache` | Trash | trash | — | on |
| `dev.cpan-build` | `~/.cpan/build` | Trash | trash | — | on |
| `dev.nix` | `nix-collect-garbage` present | `nix-collect-garbage --delete-older-than 30d` | owner | — | on |
| `dev.corepack` | `corepack` present; `$COREPACK_HOME` | `corepack cache clean` | owner | — | on |
| `dev.mise` | `mise` present; `mise cache path` | `mise cache clear` | owner | — | on |
| `dev.frontend-caches` | `$XDG_CACHE_HOME/{typescript,electron,node-gyp,vite,webpack,eslint,prettier,parcel-cache}` | Trash each | trash | — | on |
| `dev.pycache` | project roots: `__pycache__`, `*.pyc` | Trash `.pyc`-bearing dirs | trash | git-tracked check | on |
| `dev.docker-buildx` | `~/.docker/buildx/cache` | Trash | trash | — | on |
| `dev.cloud-cli` | `~/.kube/cache`, `~/.aws/cli/cache`, `~/.config/gcloud/logs`, `~/.azure/logs` | Trash | trash | — | on |
| `dev.gh-cli` | `gh` present | `env XDG_CACHE_HOME=<parent> gh config clear-cache` | owner | — | on |

**Keep rules that must be provable by test:**
- Cargo: keep `registry/src`, `registry/index`, `git`, `bin`, `toolchains`.
- Poetry: keep `virtualenvs`.
- CPAN: keep `~/.cpan/sources`.
- (Mole also reasons about HF/torch/W&B model roots; if surfaced, they must be
  kept, not trashed.)

**Process guards:** skip `cargo`/`rustc` registry prune while `cargo`/`rustc` is
running; skip npm/pnpm prune while an install is active (`pgrep`).

**Owner resolution:** caches are per-user; resolve paths by asking the owner
(`npm config get cache`, `go env GOMODCACHE`, etc.) immediately before acting, and
confirm the resolved path is inside an allowed parent (spec §7.3).

### 2. `user-cache` category (spec §8.5)
- `user-cache.app-caches`: each top-level entry under `$XDG_CACHE_HOME` not owned
  by a dev item is its own row; trash it unless whitelisted or in the protected
  set.
- `user-cache.thumbnails`: `$XDG_CACHE_HOME/thumbnails`, trash, low risk, default on.
- `user-cache.trash`: empty `$XDG_DATA_HOME/Trash` — irreversible; owner contract;
  **default OFF**, explicit.
- Each cache dir is an individual row so a user can keep specific ones; whitelist
  also covers this.

### 3. `downloads` category (spec §8.6)
- `downloads.installers`: `~/Downloads` and `~/` for
  `*.deb *.rpm *.AppImage *.iso *.pkg.tar.zst *.snap *.flatpakref` older than the
  configured age (default 30d) -> Trash.
- `downloads.old`: `~/Downloads` entries older than 90d and >100M -> list only,
  user picks, default OFF.
- Extensions/ages from config (`[downloads]`).

### 4. Whitelist manager (`internal/whitelist/manager.go`)
- `burrow clean --whitelist` interactive add/remove against the whitelist file.
- Prefix match on resolved physical paths; a whitelisted path protects itself and
  its parent from the generic user-cache sweep (spec §13.1).
- Tests: add/remove round-trip; matching protects parents; comments preserved.

## Acceptance criteria

- Each dev item's owner path **and** fallback path is tested with `testbin/`
  stubs: the planned argv is produced, or the documented cache root is trashed.
- Keep-subtree assertions pass: Cargo `registry/src`, Poetry `virtualenvs`,
  `~/.cpan/sources` survive a run that trashes the cache portions.
- Process guards: simulated running `cargo` -> `dev.rust-registry-cache` skipped.
- Whitelist: a whitelisted cache dir is excluded from `user-cache.app-caches`.
- `user-cache.trash` is default OFF and requires explicit selection.
- `scripts/check.sh` green.

## Risks & notes

- **Owner output is untrusted input.** Resolve-then-validate: the owner-reported
  path must be absolute, inside an allowed parent, and unchanged between
  discovery and execution.
- **Overlapping roots:** `dev.frontend-caches` overlaps `user-cache.app-caches`.
  De-duplicate so a path is not double-counted or trashed twice; dev items win.
- **XDG env may be unset** in non-login contexts; use defaults from Plan 01's
  resolver, not raw `$HOME`.
- **Fallback deletion is the riskiest path here.** It is trash-contract only and
  must go through `ValidateForDeletion` + identity snapshot.

## Provenance

Behavioral port of Mole's `lib/clean/dev.sh` keep/discard reasoning (Cargo
`registry/src`, Poetry `virtualenvs`, CPAN `sources`, HF/torch/W&B roots),
reimplemented clean-room with cited reasoning in comments. No Mole source copied.

## Implementation notes (Plan 04)

Recorded after landing the plan. Acceptance criteria pass and `scripts/check.sh`
is green (golangci-lint and govulncheck warn as not installed on the authoring
host, matching Plans 01/02).

### Owner command vs trash fallback

- Each item prefers its owner command. The fallback to trashing a documented
  cache root is used only when the owner binary is absent (`owner.Available`)
  and the root exists and passes `internal/safe` validation.
- Path-resolved items (npm, pnpm, bun, go, pip, uv, mise) go through
  `dev.resolvedOwner`: it probes the owner (`npm config get cache`,
  `go env GOMODCACHE`, ...), rejects a path outside `catalog.AllowedRoots`, and
  builds an `action.OwnerPathAction`. That action re-resolves the path at
  execution time and refuses when it changed or escaped the allowed parent
  (spec §7.3). Untrusted owner output is never used to build an argv.
- Trash targets are built by `catalog.NewTrashTarget`, which runs
  `safe.ValidateForDeletion` and takes an identity snapshot, so the fallback
  path is protected by the same I2/I3/TOCTOU machinery as every other trash
  item.

### Recovery-contract deviations

- `dev.rust-registry-cache` and `dev.rustup-downloads` carry the `trash`
  contract, not `owner` as spec §8.4 lists, because cargo/rustup have no
  cache-clean owner command for those subtrees. The plan's own action text
  ("Trash this subtree only") and the engine's action/target recovery
  consistency requirement make `trash` the only self-consistent reading. This is
  also recorded in the README.
- `dev.gh-cli` keeps the plain `gh config clear-cache` argv rather than the
  spec's `env XDG_CACHE_HOME=<parent> gh config clear-cache`. The runner now
  sets the invoking user's `XDG_CACHE_HOME` for every per-user command (see
  below), so gh clears the same directory discovery resolved without a dynamic
  argv (I4).

### Per-user commands under sudo (cross-plan fix)

- The review found that per-user owner commands ran with inherited root
  credentials under `sudo burrow`, violating spec §7.6. `owner.Runner` now
  carries `UserCredential` and `UserEnv`; when euid is 0 it sets
  `SysProcAttr.Credential` to the invoking user's uid/gid and overlays the
  invoking user's HOME/XDG on the child environment for `NeedsRoot == false`
  commands. `engine.Resolve` populates both from the resolved user. This is a
  fix in the Plan 03-owned `internal/owner`/`internal/engine` packages, taken
  here because Plan 04 is the first per-user-command consumer; the behavior
  change is additive and backward compatible. Covered by owner and engine tests.

### Keep rules

- Cargo: only `registry/cache` is targeted; `registry/src`, `registry/index`,
  `git`, `bin`, and `toolchains` are untouched by construction.
- Poetry: only `pypoetry/{artifacts,cache}`; `virtualenvs` is untouched.
- CPAN: only `~/.cpan/build`; `~/.cpan/sources` is untouched.
- These are asserted by `TestCargoKeepSubtrees`, `TestPoetryKeepsVirtualenvs`,
  `TestCPANKeepsSources`, and the `testdata/dev` fixture test.

### Overlap handling

- `dev.CacheRoots` lists the top-level XDG cache directories the dev category
  manages. `user-cache.app-caches` consults it and skips those entries, so dev
  items win and no path is double-counted or trashed twice.

### Known gaps

- `user-cache.app-caches` skips dev-owned cache names even when the dev tool is
  absent, so e.g. `~/.cache/pip` is not offered by the generic sweep when pip3
  is not installed. This is deliberate (dev owns the name) but means those
  directories are only swept once their tool appears.
- Under `sudo burrow`, discovery still resolves owner binaries through the
  process PATH (root's under sudo), so a per-user tool that is not on root's
  PATH falls back to trash or does not appear. The command itself now runs as
  the invoking user.
- `dev.npm`'s fallback prefers `~/.npm/_cacache` and only falls back to the
  whole `~/.npm` on older layouts.
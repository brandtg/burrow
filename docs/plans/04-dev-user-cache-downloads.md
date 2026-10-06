# Plan 04 — `dev`, `user-cache`, `downloads` catalogs + whitelist manager

**Status:** not started
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
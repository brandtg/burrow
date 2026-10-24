# burrow

`burrow` is a Linux-native, terminal-first disk-space tool with one flow:

```
burrow            # scan -> one grouped review screen -> confirm -> done
burrow analyze    # where did the space go (read-only)
```

The first question a cleaner should never ask is "which of the three commands
do I run?". There is one answer: `burrow`. It finds what is reclaimable, shows
it in one grouped preview, and runs exactly what you confirm. The scan engine
is hardlink- and reflink-aware, so a shared inode or a `cp --reflink` copy
counts once.

## The recovery story

Everything burrow touches by hand is recoverable; everything an owner command
does is permanent by nature and re-downloadable by definition. There is no
third category and no permanent-delete primitive in the binary.

- **Hand-deletes go to the Trash.** Caches, downloads, build artifacts — moved
  to the freedesktop Trash with a proper `.trashinfo` entry. A wrong guess
  costs disk space, not data. `burrow history --restore --run <id>` puts a
  run's trash items back.
- **Owner commands stay owner commands.** `dnf clean all`,
  `journalctl --vacuum-size=500M`, `docker builder prune -f`,
  `npm cache clean --force` — burrow runs the exact argv from its catalog,
  nothing else. These are not restorable, and the review screen says so.
- **The Trash itself is the last step, not a surprise.** Emptying it is a
  normal catalog row (`user-cache.trash`), default off, shown with its size.

## The four safety rules

1. **Preview first.** Discovery, measurement, filtering, and planning are
   read-only. Nothing mutates before you confirm, and `--dry-run` never
   mutates at all.
2. **Owner commands by exact argv.** Commands come from the catalog, never
   built from user input, file contents, or discovered names.
3. **Hand-deletes trash only.** A path burrow removes itself always goes to
   the Trash, inside a validated per-user allow-list. There is no generic
   "delete this path" anywhere.
4. **Fail closed.** If a condition cannot be proven right now — identity
   changed, the owner tool is mid-run, the cache cannot be measured — the item
   is skipped with the reason recorded, never downgraded to something riskier.

The process never runs as root; owner commands that need elevation are invoked
per-command via `sudo -- <exact argv>` with the invoking user's `HOME`/XDG
resolved through `SUDO_USER`.

## Command surface

```
burrow                        # the product: scan -> one grouped review -> confirm
burrow clean                  # the same flow, spelled out
burrow analyze [path...]      # read-only disk explorer
burrow purge                  # deprecated alias for: burrow clean --only projects
burrow history                # the operation journal; --restore --run <id>
burrow config                 # effective config + resolved paths
burrow completion <shell>     # bash/zsh/fish/powershell completion
burrow docs --dir <dir>       # regenerate the man pages (for packagers)
burrow version
```

`clean` runs the whole catalog. The categories, in preview order:

- **System** — package caches, journald vacuum, old kernels, orphans, tmp:
  every mutating row is an allow-listed owner command from the distro adapter
  (apt/dpkg and dnf/rpm ship; pacman/zypper/apk are data-only additions).
  High-risk rows (autoremove, volume prunes) are shown but never auto-selected.
- **Projects** — rebuildable build artifacts (`node_modules`, `target`,
  virtualenvs, `__pycache__`, ...) under the configured project roots, one
  row per project, collapsed by default. The policy is small: git-tracked
  files are kept, git state that cannot be determined is kept, a repo
  committed within `[purge] active_within` (default 30d) is kept, and an
  artifact untouched for `[purge] max_age` (default 7d) is a candidate.
- **Developer caches** — language and toolchain caches (npm, pnpm, yarn, bun,
  go, cargo, pip, uv, conda, nix, ...). Owner command when the tool exists;
  otherwise the documented cache root is trashed. Never pre-selected by
  default (see below).
- **App caches** — a curated XDG `$XDG_CACHE_HOME` sweep: a small allowlist of
  cheap caches is pre-selected; known-expensive and unrecognized entries are
  shown but off (`[user-cache] sweep_unknown` opts into the unknown ones).
- **Downloads** — old installers from `~/` and `~/Downloads`; an optional
  old-and-large sweep, default off.
- **Containers** — docker/podman prunes as owner commands, guarded on the
  daemon being up. Volume and system-wide prunes are high risk, default off.
- **Flatpak/Snap** — unused flatpak removal, disabled snap revisions, and
  leftover user-data dirs (trashed only when the app is not installed).

Selection flags: `--only`, `--exclude`, `--min-size`, `--no-owner`, plus
`--all` to include every category. In the preview, `space` toggles rows,
`a`/`n` select the auto-selectable set or none, `/` filters, `→`/`←` expand
and collapse, and `k` whitelists.

## Dev caches are never pre-selected

You actively use your toolchains, so nothing in the dev category is selected
until you ask for it. Every dev row is shown — size, recovery contract,
regeneration cost — but unselected, with the reason on the row: *dev caches
are not pre-selected by default*. Opt in per run (`--only dev`, or toggle rows
in the preview) or persist it:

```toml
[dev]
preselect = true   # restore regen-cost-driven pre-selection
```

With `preselect = true` the older policy applies: `free`/`cheap` caches are
pre-selected, `expensive` ones (Go module cache, Cargo registry, conda, Nix)
stay off, and a cache touched within `[dev] max_age` (default 30d) is marked
`,active` and left off. The idle gate and regen cost still bound bulk
selection (`a`) in every mode.

The same discipline drives the rest of the catalog: only free/cheap rows are
ever pre-selected, expensive rows are opt-in, and anything touched within its
idle window is left alone.

## Dry-run examples

```sh
# What would be reclaimed, in the read-only TUI (nothing runs, even there)
./bin/burrow --dry-run

# Plain text, for scripts and screenshots
./bin/burrow --dry-run --plain

# Machine-readable plan; every item carries status/argv/selected
./bin/burrow --dry-run --json | jq '.totals'

# One category, one item id
./bin/burrow --dry-run --plain --only projects
./bin/burrow --dry-run --json --only dev.npm

# Non-interactive execute needs --yes
./bin/burrow --yes --only system.pkg-cache --json
```

Exit codes: `0` ok, `1` error, `2` usage, `3` partial failure, `75` a safety
guard stopped an item, `124` a subprocess timeout, `130` interrupted.

## Whitelist

`k` in the preview adds the row's path (or the whole group/category) to
`$XDG_CONFIG_HOME/burrow/whitelist` — comments and blank lines preserved — and
clears its selection. Whitelisted paths protect themselves and their parent
from the app-cache sweep on every future run. Manage the file directly:

```sh
burrow clean --whitelist          # interactive add/remove loop on a terminal
burrow clean --whitelist --json   # print entries as JSON
```

## Config

Config lives at `$XDG_CONFIG_HOME/burrow/config.toml`; precedence is CLI flag
> `BURROW_*` env > file > default. `burrow config` prints the effective values
plus every resolved path; `--json` prints the same as JSON. The full set of
keys:

```toml
[general]
color = "auto"                  # auto|always|never
log_level = "info"

[clean]
default_categories = ["projects","dev","user-cache","desktop-pkgs","downloads"]
journald_max_size = "500M"
journald_max_age = ""           # either size or age; exactly one
min_size = "0"
protected_paths = ["/etc","/usr","/var/lib"]

[trash]
backend = "native"              # native|external
external_command = ""

[purge]                         # the Projects category
paths = ["~/Projects","~/src","~/dev","~/code","~/work"]
max_age = "7d"                  # untouched-for-this-long is a candidate
active_within = "30d"           # a repo committed within this window is kept

[dev]
max_age = "30d"                 # idle gate, active in every mode
preselect = false               # dev caches are not pre-selected by default

[user-cache]
sweep_unknown = false           # true pre-selects unrecognized app caches
max_age = "30d"

[analyze]
one_file_system = true          # stay on the root's backing store
ignore = ["/proc","/dev","/sys"]

[downloads]
installer_extensions = [".deb",".rpm",".AppImage",".iso",".pkg.tar.zst",".snap",".flatpakref"]
installer_min_age = "30d"
old_min_age = "90d"             # the old-and-large sweep, default off
old_min_size = "100M"
```

Matching `BURROW_*` environment variables exist for the common keys
(`BURROW_PURGE_PATHS`, `BURROW_PURGE_MAX_AGE`, `BURROW_PURGE_ACTIVE_WITHIN`,
`BURROW_DEV_MAX_AGE`, `BURROW_DEV_PRESELECT`, `BURROW_USER_CACHE_MAX_AGE`,
`BURROW_USER_CACHE_SWEEP_UNKNOWN`, `BURROW_MIN_SIZE`,
`BURROW_PROTECTED_PATHS`, `BURROW_TRASH_BACKEND`, `BURROW_COLOR`,
`BURROW_LOG_LEVEL`, `BURROW_ANALYZE_ONE_FILE_SYSTEM`).

## Journal and undo

Every mutating run appends JSONL records to
`$XDG_STATE_HOME/burrow/operations.jsonl`, grouped by `run_id`.

```sh
burrow history                  # recent runs
burrow history --json           # one JSON object per record
burrow history --run <id>       # filter to one run
burrow history --restore --run <id>   # trash items back out of the Trash
```

Owner-contract items in a run are reported but never restorable — they are the
owner's own command output, re-fetchable by definition.

## Analyze

`burrow analyze [path...]` is read-only: navigate with the arrow keys, see
sizes and the largest entries. It has no delete keys; the review flow is the
only way anything is removed. On a non-TTY, or with `--json`, it prints a
stable document (`path`, `total_size`, `entries`, `largest`,
`scan_status`, `skipped_mounts`). By default it stays on the path's backing
store; `--cross-mounts` descends into mounted filesystems, `-x` forces the
boundary on. Reflink extents count once.

## Architecture

```
cmd/burrow/         main() -> cobra root
internal/cli/       cobra commands, flag wiring, exit codes, man-page generator
internal/engine/    run pipeline: discover -> measure -> filter -> plan -> execute
internal/catalog/   the categories (system, projects, dev, user-cache,
                    downloads, containers, desktop-pkgs): data + small logic
internal/action/    Action implementations (Trash, OwnerCommand, OwnerPath)
internal/owner/     owner-command runners, timeouts, per-distro argv tables
internal/distro/    distro detection + adapter interface (apt, dnf)
internal/purge/     projects discovery: git-tracked check + activity windows
internal/trash/     native freedesktop Trash writer (put/list/restore/empty)
internal/safe/      path validation, identity snapshots, guards
internal/journal/   append-only JSONL journal + history/restore reader
internal/whitelist/ whitelist load/match
internal/config/    TOML load + precedence + defaults
internal/scan/      gdu adapter (hardlink/reflink-aware) + our item model
internal/tui/       Bubble Tea models (analyze, clean preview/review)
```

## Development

Go is pinned by `go.mod` (`go 1.26.0`, `toolchain go1.26.8`); builds are
static (`CGO_ENABLED=0`). Common tasks:

```sh
make build        # static CLI -> bin/burrow (version stamped from git describe)
make test         # unit tests
make test-race    # race detector
make fmt-check    # gofmt
make vet          # go vet
make lint         # golangci-lint (skips when not installed)
make vuln         # govulncheck (skips when not installed)
make gate         # canonical gate: fmt + vet + tests + static-binary check
make matrix       # container matrix (ubuntu/debian/fedora + testbin stubs)
```

Regenerate the man pages only after help-text changes, with
`go run ./cmd/burrow docs --dir docs/man`; the generated pages ship in the
native packages (`/usr/share/man/man1/`).

Releases are cut with goreleaser (`.goreleaser.yaml`): static linux
amd64/arm64 binaries, `.deb`/`.rpm`/`.apk`/`.pkg.tar.zst`, and a
`checksums.txt`. `install.sh` verifies SHA-256 before installing and aborts on
any mismatch.

## Non-goals

Not a privacy cleaner (no cookie/password/wipe). Not a package manager. Not a
system optimizer. No GUI. No background daemon or scheduler.

## License

Apache-2.0. burrow's shape is inspired by
[tw93/Mole](https://github.com/tw93/mole) and reimplemented clean-room for
Linux; no Mole (GPL-3.0) source is copied.

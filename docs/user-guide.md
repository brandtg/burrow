# burrow user guide

`burrow` finds where disk space went and reclaims it safely. One flow does the
work: a grouped preview of everything reclaimable, one review screen, one
confirm. Every mutation is either a **Trash move** (recoverable via
`burrow history --restore`) or an **allow-listed owner command** (permanent by
nature, re-downloadable by definition). Nothing mutates before the preview,
and any uncertainty skips the item and records why.

```
burrow                        # the product: scan -> one grouped review -> confirm
burrow analyze [path...]      # read-only disk explorer
burrow clean                  # the same flow, spelled out
burrow purge                  # deprecated alias for: burrow clean --only projects
burrow history                # the operation journal; --restore --run <id>
burrow config                 # effective config + resolved paths
burrow completion <shell>     # bash|zsh|fish|powershell
burrow docs --dir <dir>       # regenerate man pages (for packagers)
burrow version
```

Global flags: `--dry-run`, `-y/--yes`, `--json`, `--plain`, `--only`,
`--exclude`, `--min-size`, `--no-owner`, `--color`/`--no-color`, `--config`,
`--log-level`, `-v`, `--quiet`. Exit codes: `0` success, `1` error, `2` usage,
`3` partial failure, `75` a safety guard stopped an action, `124` a subprocess
timed out, `130` interrupted.

## Dry-run first

`--dry-run` runs discovery, measurement, and planning, renders the exact plan,
and exits without mutating anything. On a terminal it opens the same TUI as a
real run, in read-only mode:

```sh
burrow --dry-run                     # read-only TUI
burrow --dry-run --plain             # non-interactive plain text
burrow --dry-run --json | jq .       # machine-readable plan
burrow --dry-run --plain --only dev  # dev caches, every row shown with its reason
```

`clean` refuses to execute without confirmation: a non-interactive run must
pass `--yes` (or be a `--dry-run`):

```sh
burrow --yes --only system.pkg-cache --json
```

## Preview and review

On a terminal, the flow opens a preview TUI grouped by category, with a
sub-group level where it helps (Projects are one collapsed row per project).
Each row shows its size, recovery contract (`[trash]`/`[owner]`), a `(root)`
marker, regeneration cost (`free`/`cheap`/`expensive`/`data`, plus `,active`
when the cache was used recently), and risk. Whether a row starts selected
follows its category defaults; toggle anything before confirming.

Keys: `space` toggles a row/group/category, `a` selects every auto-selectable
row, `n` clears, `pgup`/`pgdn` (`ctrl+b`/`ctrl+f`) page, `g`/`G` jump to the
top/bottom, `/` filters, `→`/`←` expand and collapse, `K` keeps
(whitelists), `enter` moves to the review screen, `q` quits. After you
choose, the review screen lists the exact owner argv and trash paths; large
trash selections are summarized by group. Only then does anything execute.

`--plain`, `--json`, `--yes`, or a non-TTY stdout skip the TUI. `--plain`
prints a bounded preview (each group capped, with a pointer to `--json`).

## What gets selected by default

Only free/cheap rows are ever pre-selected. Expensive rows (model caches,
browser downloads, Go module cache, Nix store, conda) are shown but off.
A row touched within its idle window is marked `,active` and left off so you
do not churn something you are using.

Dev caches go one step further: **nothing in the dev category is
pre-selected, ever, by default** — you opt in per run (`--only dev` or by
toggling rows) or with `[dev] preselect = true`. With the opt-in, the
regen-cost and idle-gate policy applies again. Projects artifacts keep their
own policy: free/cheap artifacts (`__pycache__`, `_cacache`) are
pre-selected, expensive ones (`node_modules`, virtualenvs) are shown but off.

## Selecting what to clean

```sh
burrow --all                            # every category
burrow --only dev,system                # categories or item ids
burrow --only dev.npm                   # one item
burrow --exclude containers             # drop a category
burrow --min-size 100M                  # only items at/above a size
burrow --no-owner                       # trash user caches, skip owner commands
```
## Whitelist

The whitelist is a newline-delimited file at
`$XDG_CONFIG_HOME/burrow/whitelist` (default `~/.config/burrow/whitelist`).
Comments (`#`) and blank lines are preserved. A whitelisted path protects
itself and its parent from the app-cache sweep. `k` in the preview adds the
row's path (or every path in a group/category) and clears its selection, so
the choice persists to future runs.

```sh
burrow clean --whitelist            # interactive add/remove loop on a terminal
burrow clean --whitelist --json     # print entries as JSON
```

## Projects (the deprecated `purge` alias)

The Projects category finds rebuildable build artifacts (`node_modules`,
`target`, `.venv`, `__pycache__`, ...) under the configured project roots and
moves them to the Trash, one row per project. A candidate is kept when it is
git-tracked, when git state cannot be determined, when it contains a git
repository, when it belongs to an **active project** (the owning repo was
committed within `[purge] active_within`, default 30d), or when it has seen
activity within `[purge] max_age` (default 7d). With Trash recovery a wrong
guess costs disk space, not data, so there are no per-type windows or
name-shape heuristics.

```sh
burrow --dry-run --plain --only projects   # grouped per project
burrow purge --age 30d --dry-run           # --age overrides the fallback window
burrow purge --paths                       # print the configured roots
```

## Journal and undo

Every mutating run appends records to
`$XDG_STATE_HOME/burrow/operations.jsonl` (default
`~/.local/state/burrow/operations.jsonl`), grouped by `run_id`.

```sh
burrow history                    # table of recent runs
burrow history --json             # raw records, one JSON object per line
burrow history --run <run_id>     # filter to one run
burrow history --restore --run <run_id>
```

`--restore` moves the run's **trash-contract** items back from the freedesktop
Trash (matching each `.trashinfo`). Owner-contract items are permanent and
re-downloadable; they are reported but never restorable.

## Analyze

`burrow analyze [path]` opens an interactive read-only tree (arrow keys to
navigate). It has no delete keys — the review flow is the only deletion path
in burrow. When stdout is not a terminal, or with `--json`, it emits a stable
document:

```json
{"path":"...","total_size":N,"total_files":N,
 "entries":[{"name":"...","path":"...","size":N,"is_dir":true}],
 "largest":[...],"scan_status":"complete|partial|unavailable",
 "skipped_mounts":[{"path":"...","source":"...","fstype":"..."}]}
```

Flags: `-a/--apparent`, `-x/--one-file-system`, `--cross-mounts`,
`-H/--no-hidden`, `--depth <n>`, `-t/--top <n>`. `--json` accepts a single
path. An unreadable directory yields a `partial scan`: entries render as
`unknown` and `--json` reports `"scan_status": "partial"`. By default analyze
stays on the path's backing store (so btrfs subvolumes like `/` and `/home`
on one device stay in scope); a nested mount on another filesystem is skipped
and listed in `skipped_mounts`, and `--cross-mounts` descends into them
anyway. Reflinked copies (btrfs/xfs) count once.

## Config

Config lives at `$XDG_CONFIG_HOME/burrow/config.toml` (override with
`--config`). Precedence is CLI flag > `BURROW_*` environment > config file >
built-in default. `burrow config` prints the effective config plus every
resolved path; `--json` prints the same as JSON.

```toml
[general]
color = "auto"                  # auto|always|never
log_level = "info"

[clean]
default_categories = ["projects","dev","user-cache","desktop-pkgs","downloads"]
journald_max_size = "500M"      # or journald_max_age; exactly one
min_size = "0"
protected_paths = ["/etc","/usr","/var/lib"]

[trash]
backend = "native"              # native|external
external_command = ""

[purge]                         # the Projects category
paths = ["~/Projects","~/src","~/dev","~/code","~/work"]
max_age = "7d"                  # untouched-for-this-long is a candidate
active_within = "30d"           # keep artifacts in a recently-committed repo

[dev]
max_age = "30d"                 # idle gate for recently-used dev caches
preselect = false               # dev caches are not pre-selected by default

[user-cache]
sweep_unknown = false           # true pre-selects unknown app caches
max_age = "30d"

[analyze]
one_file_system = true
ignore = ["/proc","/dev","/sys"]

[downloads]
installer_min_age = "30d"       # plus installer_extensions, old_min_age, old_min_size
```
## Shell completion and man pages

```sh
burrow completion bash > /etc/bash_completion.d/burrow
burrow completion zsh  > "${fpath[1]}/_burrow"
burrow completion fish > ~/.config/fish/completions/burrow.fish
go run ./cmd/burrow docs --dir docs/man   # regenerate the shipped man pages
```

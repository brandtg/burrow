# burrow user guide

`burrow` (alias `bw`) finds where disk space went and reclaims it safely. Every
mutation is either a **Trash move** (recoverable) or an **allow-listed owner
command** (permanent but re-downloadable). Nothing mutates before the preview,
and any uncertainty skips the item and records why.

```
burrow                        # the product: scan -> one grouped review -> confirm
burrow analyze [path...]      # read-only disk explorer
burrow clean                  # alias for the no-arg flow
burrow purge                  # deprecated alias for: burrow clean --only projects
burrow history                # read the operation journal; --restore <run_id>
burrow config                 # effective config + resolved paths
burrow completion <shell>     # bash|zsh|fish completion
burrow version
```

Global flags: `--dry-run`, `-y/--yes`, `--json`, `--plain`, `--only`, `--exclude`,
`--min-size`, `--color`/`--no-color`, `--config`, `--log-level`, `-v`, `--quiet`.
Exit codes: `0` success, `1` error, `2` usage, `3` partial failure, `75` a safety
guard stopped an action, `124` a subprocess timed out, `130` interrupted.

## Dry-run first

`--dry-run` runs discovery, measurement, and planning, renders the exact plan,
and exits without mutating anything. Use it whenever you are unsure. On a
terminal it opens the same TUI as a real run, in read-only mode:

```sh
burrow clean --dry-run              # read-only TUI
burrow clean --dry-run --plain      # non-interactive plain text
burrow clean --dry-run --json | jq .
burrow purge --dry-run
```

Because `clean` refuses to execute without confirmation, a non-interactive run
must pass `--yes` (or `--dry-run`):

```sh
burrow clean --yes --only system.pkg-cache
```

## Preview and review

On a terminal, `clean` opens a preview TUI grouped by category (with an optional
sub-group level) showing each item's size, recovery contract
(`[trash]`/`[owner]`), `(root)` marker,
regeneration cost (`free`/`cheap`/`expensive`/`data`, plus `,active` when the
cache was used recently), and risk. Categories and groups with many items start
collapsed; `→`/`←` expand and collapse, `/` filters, and `space` on a category
or group toggles every auto-selectable member. High-risk, expensive, and
recently-active rows are never pre-selected; press `k` on a row to whitelist it
and keep it. After you choose, a **review** screen lists the exact owner argv
that will run (large trash selections are summarized by group). Only then does
anything execute.

The `regen` column reflects how costly an item is to rebuild. `free` and `cheap`
caches (thumbnails, frontend caches, package HTTP caches) are pre-selected;
`expensive` caches (model weights, browser downloads, Go module cache, Nix
store) are shown but off, because deleting them means a large re-download or a
long rebuild. A cache touched within the idle window (`[dev] max_age` /
`[user-cache] max_age`, default 30d) is marked `,active` and left off so you do
not churn something you are using.

`--plain`, `--json`, `--yes`, or a non-TTY stdout skip the TUI. `--plain` prints
a bounded preview (each group capped, with a pointer to `--json`) and requires
`--yes` to execute; `--json` emits a stable document with per-item `status`,
`argv`, and totals.

## Selecting what to clean

```sh
burrow clean --all                      # every category
burrow clean --only dev,system          # categories or item ids
burrow clean --only dev.npm             # one item
burrow clean --exclude containers       # drop a category
burrow clean --min-size 100M            # only items at/above a size
burrow clean --no-owner                 # trash user caches, skip owner commands
```

## Whitelist

The whitelist is a newline-delimited file at
`$XDG_CONFIG_HOME/burrow/whitelist` (default `~/.config/burrow/whitelist`).
Comments (`#`) and blank lines are preserved. A whitelisted path protects itself
and its parent from the generic `user-cache` sweep, so you can keep a cache you
care about. In the preview TUI, `k` on an item (or a group/category row) adds
its path(s) to the whitelist and clears the selection, so the choice persists to
future runs.

```sh
burrow clean --whitelist          # interactive add/remove loop on a terminal
burrow clean --whitelist          # otherwise: print the current entries
burrow clean --whitelist --json   # print as JSON
```

## Journal and undo

Every mutating run appends records to
`$XDG_STATE_HOME/burrow/operations.jsonl` (default
`~/.local/state/burrow/operations.jsonl`). Each run is grouped by `run_id`.

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
navigate). It never deletes anything; the only deletion path in burrow is the
clean flow's review screen. When stdout is not a terminal, or with `--json`, it
emits a stable document:

```json
{"path":"...","total_size":N,"total_files":N,
 "entries":[{"name":"...","path":"...","size":N,"is_dir":true}],
 "largest":[...],"scan_status":"complete|partial|unavailable",
 "skipped_mounts":[{"path":"...","source":"...","fstype":"..."}]}
```

Flags: `-a/--apparent`, `-x/--one-file-system`, `--cross-mounts`, `-H/--no-hidden`,
`--depth <n>`, `-t/--top <n>`. `--json` accepts a single path.

When a directory cannot be read (for example due to permissions), the scan is
partial: the interactive UI shows a `partial scan` warning, unreadable entries
render as `unknown`, and `--json` reports `"scan_status": "partial"`.

By default analyze stays on the backing store of the path you give it (measured
by mount source, so btrfs subvolumes such as `/` and `/home` on one device stay
in scope). A nested mount on another filesystem is skipped and listed in
`skipped_mounts` (the interactive UI shows a `skipped N mount(s)` warning);
scan that path separately. Pass `--cross-mounts` to descend into them anyway, or
`-x`/`--one-file-system` to force the boundary on.

On reflink-capable filesystems (btrfs, xfs) on-disk sizes are shared-extent
aware: reflinked copies (for example `cp --reflink`-ed `node_modules`) count once
across the scanned tree instead of once per copy. The total is the unique
physical bytes referenced by the tree, so scan a common parent to dedup copies
that share a source outside the scanned root.

## Purge

`burrow purge` removes rebuildable project artifacts (`node_modules`, `target`,
`.venv`, `__pycache__`, ...) under the configured project roots. A candidate is
kept when it is git-tracked, when git state cannot be determined, when it
contains a git repository, when its activity age cannot be verified, when it has
seen recent activity, when it belongs to an **active project** (the owning repo
was committed within `active_within`, default 30d), or when a generic name
(`build`, `dist`, `target`, `.gradle`) has no ecosystem marker beside it. Each
artifact type has its own inactivity window via `[purge] max_age_by_type`
(`node_modules` 30d, `target`/`.next/cache` 7d, `__pycache__` off, ...), with
`max_age` as the fallback. `--age <dur>` overrides the whole policy; `--paths`
prints the configured roots.

```sh
burrow purge --dry-run
burrow purge --age 30d --yes
burrow purge --paths
```

## Config

Config lives at `$XDG_CONFIG_HOME/burrow/config.toml` (override with
`--config`). Precedence is CLI flag > `BURROW_*` environment > config file >
built-in default. `burrow config` prints the effective config plus every
resolved path (config, whitelist, journal, cache, state, data, trash, distro,
package manager); `--json` prints the same as JSON.

```toml
[general]
color = "auto"          # auto|always|never
log_level = "info"

[clean]
default_categories = ["dev","user-cache","desktop-pkgs","downloads"]
journald_max_size = "500M"      # or journald_max_age; exactly one
min_size = "0"
protected_paths = ["/etc","/usr","/var/lib"]

[trash]
backend = "native"              # native|external
external_command = ""

[purge]
paths = ["~/Projects","~/src","~/dev"]
max_age = "7d"                  # fallback inactivity window
active_within = "30d"           # keep artifacts in a recently-committed repo
[purge.max_age_by_type]         # per-artifact overrides; "0" disables the filter
node_modules = "30d"
target = "7d"
"__pycache__" = "0"

[dev]
max_age = "30d"                 # recently-used dev caches are not pre-selected

[user-cache]
sweep_unknown = false           # true pre-selects unknown app caches (old behavior)
max_age = "30d"

[analyze]
one_file_system = true          # stay on the root's backing store; --cross-mounts overrides
ignore = ["/proc","/dev","/sys"]

[downloads]
installer_min_age = "30d"
```

## Shell completion and man pages

```sh
burrow completion bash > /etc/bash_completion.d/burrow
burrow completion zsh  > "${fpath[1]}/_burrow"
burrow completion fish > ~/.config/fish/completions/burrow.fish
man burrow             # after installing the generated docs/man pages
```
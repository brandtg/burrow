# burrow user guide

`burrow` (alias `bw`) finds where disk space went and reclaims it safely. Every
mutation is either a **Trash move** (recoverable) or an **allow-listed owner
command** (permanent but re-downloadable). Nothing mutates before the preview,
and any uncertainty skips the item and records why.

```
burrow analyze [path...]      # interactive disk explorer (read-only except 'd' trash)
burrow clean                  # categorized cleaner (preview -> confirm -> execute)
burrow purge                  # rebuildable project artifacts
burrow status                 # one-shot system snapshot (thin)
burrow history                # read the operation journal; --restore <run_id>
burrow config                 # effective config + resolved paths
burrow completion <shell>     # bash|zsh|fish completion
burrow docs                   # generate man pages into docs/man
burrow version
```

Global flags: `--dry-run`, `-y/--yes`, `--json`, `--only`, `--exclude`,
`--min-size`, `--color`/`--no-color`, `--config`, `--log-level`, `-v`, `--quiet`.
Exit codes: `0` success, `1` error, `2` usage, `3` partial failure, `75` a safety
guard stopped an action, `124` a subprocess timed out, `130` interrupted.

## Dry-run first

`--dry-run` runs discovery, measurement, and planning, renders the exact plan,
and exits without mutating anything. Use it whenever you are unsure:

```sh
burrow clean --dry-run
burrow clean --dry-run --json | jq .
burrow purge --dry-run
```

Because `clean` refuses to execute without confirmation, a non-interactive run
must pass `--yes` (or `--dry-run`):

```sh
burrow clean --yes --only system.pkg-cache
```

## Preview and review

On a terminal, `clean` shows a preview grouped by category with each item's
size, recovery contract (`[trash]`/`[owner]`, or `[delete]` under `--permanent`),
`(root)` marker, and risk. High risk rows are never pre-selected. After you
choose, a **review** screen lists the exact paths and argv that will run. Only
then does anything execute.

In a non-terminal (or with `--json`), `clean` prints the plan and requires
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
burrow clean --permanent                # trash-contract items deleted instead (warned)
```

`--permanent` is an explicit escape hatch; it never applies to owner commands. A
permanent trash-contract item is labeled `[delete]` in the preview and review, a
warning is printed, and the journal records `action: "delete"` /
`recovery: "delete"` so it is never offered by `history --restore`.

## Whitelist

The whitelist is a newline-delimited file at
`$XDG_CONFIG_HOME/burrow/whitelist` (default `~/.config/burrow/whitelist`).
Comments (`#`) and blank lines are preserved. A whitelisted path protects itself
and its parent from the generic `user-cache` sweep, so you can keep a cache you
care about.

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

`burrow analyze [path]` opens an interactive tree (arrow keys; `d`/`D` trash the
selected/highlighted entry; `--no-delete` makes it read-only). When stdout is not
a terminal, or with `--json`, it emits a stable document:

```json
{"path":"...","total_size":N,"total_files":N,
 "entries":[{"name":"...","path":"...","size":N,"is_dir":true}],
 "largest":[...],"scan_status":"complete|partial|unavailable"}
```

Flags: `-a/--apparent`, `-x/--one-file-system`, `-H/--no-hidden`,
`--depth <n>`, `-t/--top <n>`, `--no-delete`. `--json` accepts a single path.

## Purge

`burrow purge` removes rebuildable project artifacts (`node_modules`, `target`,
`.venv`, `__pycache__`, ...) under the configured project roots. A candidate
inside a git repository that is tracked (or whose git state is unknown) is kept.
`--age <dur>` (default `7d`) requires no recent file activity; `--paths` prints
the configured roots.

```sh
burrow purge --dry-run
burrow purge --age 30d --yes
burrow purge --paths
```

## Status

`burrow status` prints a one-shot CPU/memory/disk/network/process snapshot. It is
thin and read-only (no daemon, no monitoring), and `--json` emits a stable
schema. `--path <dir>` selects the filesystem to report disk usage for.

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
max_age = "7d"

[analyze]
one_file_system = true
ignore = ["/proc","/dev","/sys","/run"]

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
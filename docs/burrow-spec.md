# `burrow` — a prescriptive Linux disk-space + cleanup tool
## Product spec & implementation plan (v0.1 handoff)

**Status:** DRAFT for handoff to an implementation agent.
**Working name:** `burrow` (binary `burrow`, short alias `bw`). **Rename before v0.1** — see §20.
**Authoring context:** follows `~/workspace/docs/mole-linux-portability.md` (the Mole study). This doc is the buildable derivative of that analysis.
**Target reader:** an autonomous coding agent + a human reviewer. Everything the agent needs to start must be in this document; decisions are stated as decisions, not options, except where explicitly flagged (see §20).

---

## 0. One-paragraph summary

`burrow` is a Linux-native, terminal-first disk tool that does three things Mole does *and can't be ported to do*: (1) **analyze** where space went with a fast, interactive TUI; (2) **clean** prescriptively by driving the *owner* of each kind of junk (package manager, journald, container runtime, flatpak/snap, language toolchains) with a mandatory preview, dry-run, whitelist, and an undo log; (3) **purge** rebuildable project artifacts with git-tracked-file protection. It is an **orchestrator with a safety contract**, not a path-deleter. It reuses `gdu`'s scan engine and Bubble Tea for the UI, and shells out to distro owner commands for everything mutating. v0.1 targets **Debian/Ubuntu + systemd**, with a distro-adapter interface designed in from day one for dnf/pacman/zypper/apk.

---

## 1. Problem & product thesis

Linux users who want "understand my disk + clean it up safely" today must assemble: `ncdu`/`gdu`/`dust` (analysis), manual `apt clean` / `journalctl --vacuum` / `docker system prune` / `flatpak uninstall --unused` (cleanup), and remember which of ~20 owner commands is safe and what each reclaims. Existing integrated tools are GUI-first and shallow on developer/package caches (BleachBit, Stacer, KDE Sweeper).

**Thesis:** the missing product is the *integration + the safety contract*, not another scanner and not another `rm` script. Value = one opinionated TUI that (a) shows where space went, (b) offers categorized, previewable cleanup where every action is either a **freedesktop Trash move** (recoverable) or an **allow-listed owner command** (re-downloadable), and (c) never hand-deletes package-manager state.

**Why this wins over alternatives:** `ncdu` can't clean package state; BleachBit doesn't do dev-cache depth and is GUI-first; hand scripts have no preview/undo/log. `burrow` = Mole's UX shape + Linux's commands + Mole's safety discipline, reimplemented (not copied).

---

## 2. Locked decisions (ADRs)

Each is a decision, with rationale. The agent should not re-litigate these unless §20 says so.

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Language: Go** (single static binary, CGO_ENABLED=0). | Compose owner commands + a TUI + fast scanning. Same ecosystem as Mole's TUIs and gdu. No runtime deps. |
| D2 | **Scan engine: import `github.com/dundee/gdu/v5/pkg/analyze` + `pkg/fs`.** Do not write a scanner. | gdu is fast, proven, MIT-licensed (verify license in §21 before pinning), and exposes `analyze.CreateAnalyzer()` / `CreateTopDirAnalyzer()`, `fs.Item`, `HardLinkedItems` (confirmed: it is imported this way by `dundee/disk_usage_exporter`). |
| D3 | **TUI: Bubble Tea + Lipgloss + Bubbles** (Charm). | Same stack Mole uses; mature; good for list/table/key-driven views. |
| D4 | **CLI: `spf13/cobra`** (subcommands + flags + shell completion + man-page generation for free). | Standard, matches the `tool <verb>` surface. |
| D5 | **Config: TOML** at `$XDG_CONFIG_HOME/burrow/config.toml`, parsed with `github.com/BurntSushi/toml`. Whitelist is a separate newline-delimited file. | Human-editable; avoids a heavy config framework. |
| D6 | **Trash: implement the freedesktop Trash Spec natively.** Do not depend on `trash-cli`. | Trivial format; removes a runtime dep; enables the undo journal. Detect `gio`/`trash-cli` only as fallbacks. |
| D7 | **Cleanup = owner-command delegation, never path-deletion of package/OS state.** | The system owner defines "safe." This is the whole safety thesis. |
| D8 | **Two recovery contracts per item: `trash` (recoverable) or `owner` (re-downloadable, permanent).** UI/labels must state which. | Linux cleanup is not uniformly recoverable (vs Mole's always-Trash on macOS). |
| D9 | **Never run the whole tool as root.** Elevate per-owner-command via `sudo`, resolving the invoking user's `HOME`/XDG via `SUDO_USER`. | Mole's rule; avoids root-owned artifacts in the user's home. |
| D10 | **v0.1 scope: Debian/Ubuntu + systemd.** Other distros behind an adapter interface, implemented later. | Fragmentation is the top product risk; ship one family well. |
| D11 | **Every mutating run writes a JSON operation journal** (`history` reads it). | Auditability + undo source-of-truth; Mole's `operations.log` idea, stricter. |
| D12 | **Deletion of *user* files (analyze `d`/`D`, XDG cache sweep, installer sweep) routes through Trash and is refuse-on-unavailable.** | Mirror Mole `mole_delete` fail-closed. |
| D13 | **No background daemon, no scheduler, no auto-run in v0.1.** One-shot, reviewable. | Mole's own product discipline; avoids surprise writes. |
| D14 | **License: MIT or Apache-2.0** (independent implementation; no Mole source copied). | Mole is GPL-3.0; a clean reimplementation is free to pick permissive. |

---

## 3. Personas & primary use cases

- **P1 Dev laptop, disk full.** "I have 40 GB in node_modules/Go/Docker caches I can rebuild." → `burrow analyze`, `burrow clean --only dev,containers`, `burrow purge`.
- **P2 Long-lived desktop.** "Something ate my SSD." → `burrow analyze`, `burrow clean` (all categories, preview), `journalctl`/snap/flatpak reclaim.
- **P3 Headless server / CI box.** "`/var` is full, no GUI, cron-friendly." → `burrow clean --yes --json` (dry-run first), `burrow clean --only system`.
- **P4 Cautious user.** "Show me exactly what you'd delete and where." → `--dry-run`, preview screen, `burrow history`, whitelist.

Primary acceptance: P1–P4 all go from "disk full" to "understood + reclaimed safely" without ever running a raw destructive command or changing package-manager state by hand.

---

## 4. Non-goals (explicit)

1. **Not a privacy cleaner.** No cookie/password/history wiping, no shred, no free-space wipe. (BleachBit's turf; different product, different risk.)
2. **Not a package manager.** Never `remove`/`install` packages by default, except documented orphan/autoremove classes that are shown in preview and opt-in.
3. **Not a system-optimizer.** No DNS flush, no sysctl tuning, no service "cleanup," no `/proc/sys/vm/drop_caches` (a benchmark-only action that reclaims nothing durable).
4. **No distro-universal v0.1.** Debian/Ubuntu + systemd only; others behind adapters.
5. **No background monitoring / scheduling / daemon.**
6. **No GUI.** Terminal only (the `analyze` module may optionally add `--web` later by reusing gdu's web UI, non-goal for v0.1).
7. **No non-ext4/xfs/btrfs-specific features in v0.1** (no snapshot awareness; noted as §20 open question).

---

## 5. Command surface

Binary `burrow` (alias `bw`). Global flags apply to all commands.

```
burrow analyze [path...]      # interactive disk explorer (read-only except 'd' trash)
burrow clean                  # categorized cleaner (preview → confirm → execute)
burrow purge                  # rebuildable project artifacts (node_modules, target, …)
burrow status                 # one-shot system snapshot (CPU/mem/disk/net/process)   [thin, see §11]
burrow history                # read the operation journal
burrow config                 # print effective config + paths
burrow completion <shell>     # shell completion (cobra-generated)
burrow version
```

### 5.1 Global flags
```
--dry-run                 Never mutate. On a terminal, open the read-only preview TUI; otherwise print the plan and exit.
-y, --yes                 Skip the interactive confirm (requires a preview unless --dry-run).
--plain                   Non-interactive plain-text output instead of the TUI (auto when stdout is not a TTY, or with --json/--yes).
--json                    Machine-readable output (clean/analyze/history/status).
--only <list>             Comma list of categories or item IDs to include.
--exclude <list>          Comma list of categories/IDs to exclude.
--min-size <size>         Only report/act on items >= size (e.g. 10M, 1G).
--no-color / --color=auto Terminal color policy.
--config <path>           Override config file.
--log-level <lvl>         trace|debug|info|warn|error (default info; logs to journal dir).
-v, --verbose             Shorthand for --log-level debug, plus per-item detail.
--quiet                   Only errors.
```

### 5.2 `clean` flags
```
--all                     Include every category (still respects whitelist + guards).
--no-owner                Allow trashing user caches but skip all owner-command/system acts.
--permanent               For trash-contract items, delete permanently instead of Trash (explicit opt-in; prints a warning).
--whitelist               Manage the whitelist interactively (add/remove paths).
```

### 5.3 `analyze` flags
```
-a, --apparent            Show apparent size vs on-disk (default: on-disk, i.e. blocks*512).
-x, --one-file-system     Don't cross mount points.
-H, --no-hidden           Skip dotfiles.
--depth <n>               Non-interactive depth (with --json).
-t, --top <n>             Non-interactive top-N largest.
--no-delete               Disable the 'd'/'D' trash keys (read-only mode).
```

### 5.4 `purge` flags
```
--paths                   Print the configured project roots (edit them in config.toml or via BURROW_PURGE_PATHS).
--age <dur>               Only artifacts with no file activity in this window (default 7d).
--yes                     Non-interactive (required when not a TTY).
```

### 5.5 Exit codes
```
0   success (including "nothing to do")
1   generic error
2   usage error
3   a required action failed (partial)
75  aborted by a safety guard (owner active / lock held / path changed)   # Mole uses 75 for guard-stops
124 a subprocess timed out
130 interrupted (SIGINT)
```

---

## 6. Architecture

### 6.1 Layer map

```
cmd/burrow/                 main() → cobra root
internal/cli/               cobra commands, flag wiring, output formatting
internal/engine/            the run pipeline: discover → plan → preview → execute → journal
internal/catalog/           the cleanup catalog: category/item registries (data + small logic)
internal/action/            Action interface + implementations (TrashAction, OwnerCommandAction)
internal/owner/             owner-command runners + per-distro command tables
internal/distro/            distro detection + adapter interface (apt/dnf/pacman/zypper/apk)
internal/trash/             freedesktop Trash Spec writer (put/list/restore/empty)
internal/safe/              path validation, identity snapshot, guards, fail-closed helpers
internal/journal/           operation journal (append-only JSONL) + history reader
internal/whitelist/         whitelist load/match
internal/config/            TOML load + precedence + defaults
internal/scan/              gdu adapter (wraps pkg/analyze + pkg/fs) + our item model
internal/tui/               Bubble Tea models (analyze view, clean preview, purge view)
internal/metrics/           gopsutil wrappers (status; thin)
internal/i18n/              message catalog (EN only in v0.1; keys so it's translatable)
docs/                       man pages (generated), user guide
testdata/                   fixture filesystems + golden outputs
```

### 6.2 Core interfaces (Go signatures — the agent should treat these as the contract)

```go
package engine

// A Target is one concrete thing the tool might act on.
type Target struct {
    ID          string        // stable, e.g. "dev.npm-cache"
    Category    string        // "dev", "system", "containers", "desktop-pkgs", "user-cache", "downloads", "trash"
    Display     string        // human label: "npm cache"
    Path        string        // absolute, or "" for pure owner commands
    SizeBytes   int64         // measured (may be -1 if unknown, rendered "unknown")
    SizeKnown   bool
    Action      Action        // what execution does
    Recovery    Recovery      // RecoveryTrash | RecoveryOwner
    NeedsRoot   bool
    Guard       Guard         // pre-execution safety predicate
    Evidence    []string      // why this is safe (bundle ids, owner docs, git status)
    Risk        Risk          // RiskLow | RiskMed | RiskHigh  (drives default selection)
    DefaultOn   bool          // whether pre-selected in preview
}

// An Action performs the mutation. Implementations must be idempotent and
// must re-validate identity at the moment of execution (see §7).
type Action interface {
    Describe() string
    // Recovery declares the contract (RecoveryTrash | RecoveryOwner) so I2 can
    // be enforced at the action boundary. Addition landed in Plan 01
    // (internal/engine/types.go); Plan 03 must keep it.
    Recovery() Recovery
    // Run executes; dryRun must compute the same plan without mutating.
    Run(ctx context.Context, dryRun bool) (Result, error)
}

// A Category enumerates targets. Discovery must be side-effect-free.
type Category interface {
    ID() string
    Title() string
    Discover(ctx context.Context, env *Env) ([]Target, error)
    // PreviewNote is optional per-category guidance shown in the UI.
    PreviewNote() string
}

// A Guard fails closed: it returns an error (guard-stop, code 75) when it
// cannot PROVE the action is safe right now.
type Guard interface {
    Check(ctx context.Context, env *Env) error
}

type Recovery int // RecoveryTrash, RecoveryOwner
type Risk int     // RiskLow, RiskMed, RiskHigh

// Env carries resolved user/root context and external command runners.
type Env struct {
    User        *user.User   // the invoking user (from SUDO_USER if elevated)
    Home        string
    XDGCache, XDGConfig, XDGData, XDGState, XDGRuntime string
    Run         *owner.Runner // exec + timeout + dry-run awareness
    Whitelist   *whitelist.Set
    Cfg         *config.Config
}
```

### 6.3 The run pipeline

```
1. RESOLVE ENV      detect distro, systemd, resolve invoking user + XDG paths
2. DISCOVER         each selected Category.Discover() → []Target   (read-only, parallelizable, per-category timeout)
3. MEASURE          fill SizeBytes (du-style; gdu for trees, du for dirs, stat for files)  [bounded time]
4. FILTER           drop whitelisted paths, drop below --min-size, apply --only/--exclude
5. PLAN             build the ordered plan; annotate recovery/root/risk; compute totals
6. PREVIEW          render the plan (TUI table, or --json/plain) and get confirmation
7. EXECUTE          for each target: re-run Guard → re-validate identity → Action.Run
                    guards that trip → skip with reason (record), never abort the whole run
                    SIGINT → graceful stop; already-done items stay done
8. JOURNAL          append every attempt (ok/skip/fail + reason + bytes) to the journal
9. SUMMARY          totals, freed space (df before/after), skipped-with-reason, journal path
```

**Invariant:** steps 2–5 are pure/read-only. Nothing mutates before step 7. `--dry-run` stops after step 6.

---

## 7. Safety contract (the crux — read this twice)

This is what makes `burrow` safer than a shell script. Every rule below is testable (§14).

### 7.1 Invariants
- **I1 — No mutation before preview.** Discover/measure/filter/plan are read-only.
- **I2 — Every mutation has a declared recovery contract** (`trash` or `owner`), shown in preview and recorded in the journal.
- **I3 — No path is deleted that isn't either (a) inside an allow-listed user-cache root, or (b) produced by an owner command.** There is no "delete this arbitrary path" primitive.
- **I4 — Owner commands are allow-listed by exact argv** from the catalog. No dynamic command construction from user input.
- **I5 — Fail closed.** Any uncertainty (can't resolve identity, can't prove owner idle, Trash unavailable, timeout) → skip that target, record why, continue. Never downgrade to permanent delete silently.
- **I6 — Never run the whole process as root.** If `geteuid()==0` and `SUDO_USER` is unset, refuse with a clear message (the user must run it as themselves; the tool elevates internally).
- **I7 — Privileged operations never traverse a user-mutable ancestor** (Mole's `_mole_privileged_path_has_mutable_ancestor`). If a root-owned delete's parent chain includes a user-writable dir, skip + reason.

### 7.2 Path validation (package `internal/safe`)
`ValidateForDeletion(path)` must reject:
- non-absolute paths; paths containing `..`, empty segments, control chars, or `//`;
- `/`, `/boot`, `/bin`, `/sbin`, `/lib*`, `/usr`, `/etc`, `/var`, `/var/lib`, `/var/log`, `/home` **themselves** (only documented subpaths may be targeted, and those only via owner commands);
- symlink *roots* for recursive deletion (resolve with `EvalSymlinks`; if the physical path differs from the lexical path at the root, refuse — Mole's `clean_service_worker_cache` pattern);
- any path under a configurable protected set (§13);
- any path not owned by the invoking user for trash-contract items.

### 7.3 Identity snapshot (TOCTOU defense)
Before executing a trash-contract target, snapshot `{parent dev+ino, target dev+ino, optional size}`. Immediately before the sink, re-stat and compare. If changed → skip (`guard-stop`). This defeats "path swapped between discovery and deletion." Port of Mole's `_mole_snapshot_path_identity` / `_mole_path_matches_identity`.

For owner-command targets, the analogous check is: re-resolve the owner-reported root (e.g. `npm config get cache`, `go env GOMODCACHE`) immediately before running, and confirm it's still the same physical path and inside an allowed parent.

### 7.4 Recovery contracts
- **`trash`** — move to freedesktop Trash (§9). Recoverable. Used for user caches, downloads, installers, analyze deletions. Default for user-owned data.
- **`owner`** — invoke the owner command. Permanent but re-downloadable/rebuildable. Used for package caches, journald, container prune, flatpak/snap, language caches. Never routed through Trash (it isn't the user's file to recover; the owner regenerates it).

The preview and journal MUST label which contract applies to each item. `--permanent` overrides `trash`→delete only with an explicit warning.

### 7.5 Guards (`internal/safe`, `owner` lock detection)
Guards are tri-state predicates: `safe` / `busy` / `unknown`; `busy` and `unknown` both stop the action (fail closed).
- **Package-manager lock:** `dpkg` frontend lock (`/var/lib/dpkg/lock-frontend`), apt `/var/lib/dpkg/lock`, `dnf`/`zypper`/`pacman` lock files (per distro adapter). Detect via `flock -n` probe or checking for a live `apt`/`dpkg`/`dnf`/`pacman`/`zypper` process.
- **Container runtime:** docker daemon reachable? a `docker build` running? (owner command may still be fine, but record).
- **Toolchain process guards** (for lang caches): `pgrep -x cargo|rustc`, `pnpm`/`npm install`, `go` build. Mirror Mole: skip `cargo registry/cache` prune if `cargo`/`rustc` is running.
- **journald:** always fine to vacuum (it's journald's own command), but size/time bounds must be explicit.

### 7.6 Elevation model (package `internal/owner`)
- Resolve invoking user via `SUDO_USER`/`os.Getuid`→`user.LookupId`; compute that user's `HOME` and XDG dirs (do **not** trust root's `$HOME`).
- Owner commands needing root are run as `sudo -- <argv>` (exact argv from catalog), with a bounded timeout. No shell interpolation.
- Owner commands that are inherently per-user (npm/pnpm/go/nix as the user, user flatpak) run **as the invoking user**, never as root (Mole's rule: user state must not run with inherited root).
- Never inherit the caller's `PATH` into a root command if it can be avoided; resolve the binary via `exec.LookPath` as the invoking user, then run the absolute path under sudo.

### 7.7 Timeouts
Every external command gets a deadline from a central table (`internal/owner/timeouts.go`): quick-detect 5s, size probe 30s, owner-clean 10min, scan per-root 90s. A timeout → skip + reason + exit-124 semantics (but continue the run).

---

## 8. Cleanup catalog

The catalog is DATA (Go tables or embedded TOML) + a small `Discover` per category. Sizes are measured at runtime, never hard-coded. "Default" = pre-selected in preview. Risk drives default.

Catalog columns: **Item** (id) · **Detect** · **Action** · **Recovery** · **root?** · **Guard** · **Risk** · **Default**.

### 8.1 `system` — package managers & OS (needs distro adapter; root for most)

| Item | Detect | Action (owner cmd) | Rec | root | Guard | Risk | Def |
|---|---|---|---|---|---|---|---|
| `system.pkg-cache` | distro=apt | `apt-get clean` | owner | yes | apt/dpkg idle | low | on |
| | distro=dnf | `dnf clean all` | owner | yes | dnf idle | low | on |
| | distro=pacman | `paccache -r -k1` (if present) else `pacman -Sc --noconfirm` | owner | yes | pacman idle | med | on |
| | distro=zypper | `zypper clean --all` | owner | yes | zypper idle | low | on |
| | distro=apk | `apk cache clean` | owner | yes | — | low | on |
| `system.orphans` | apt: `apt-get -s autoremove` lists any | `apt-get autoremove --purge` | owner | yes | apt idle | **high** | **off** |
| | pacman: `pacman -Qtdq` nonempty | `pacman -Rns --noconfirm <orphans>` | owner | yes | pacman idle | **high** | **off** |
| | dnf: `dnf autoremove` | `dnf autoremove -y` | owner | yes | dnf idle | high | off |
| | zypper: `zypper packages --orphaned` | `zypper remove -y <orphans>` | owner | yes | zypper idle | high | off |
| `system.residual-config` | apt: `dpkg -l \| awk '/^rc/{print $2}'` | `dpkg --purge <pkgs>` | owner | yes | dpkg idle | med | off |
| `system.old-kernels` | apt: installed `linux-image-*` minus running | `apt-get autoremove --purge` (apt owns selection) | owner | yes | apt idle | **high** | off |
| `system.journald` | `systemctl is-active systemd-journald`; `journalctl --disk-usage` | `journalctl --vacuum-size=<cfg>` (default 500M) OR `--vacuum-time=<cfg>` | owner | yes | — | low | **on** |
| `system.rotated-logs` | `/var/log/**/*.{gz,1,old}` older than N days | **trash is wrong here** → move to system temp then unlink? No: use `systemd-tmpfiles --clean` + targeted age delete of rotated files only. | owner | yes | — | med | off |
| `system.tmp` | `/tmp`, `/var/tmp` entries older than 10d | `systemd-tmpfiles --clean` | owner | yes | — | low | off |
| `system.core-dumps` | `/var/lib/systemd/coredump/*`, `core.*` in cwd/home | Trash if user-owned; else journald-coredump vacuum (`coredumpctl` rotate) | mixed | maybe | — | low | off |
| `system.crash` | `/var/crash/*` | owner `systemd-tmpfiles --clean` + trash user-owned | mixed | yes | — | low | off |

> **Note on `system.orphans` / `old-kernels`:** high risk → default OFF, always shown, never auto-selected. They *are* valuable but they change installed software; keep them opt-in and clearly explained.

### 8.2 `containers`

| Item | Detect | Action | Rec | root | Guard | Risk | Def |
|---|---|---|---|---|---|---|---|
| `containers.docker-build-cache` | `docker` present + daemon up | `docker builder prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-dangling` | same | `docker image prune -f` | owner | maybe | daemon up | low | on |
| `containers.docker-unused` | same | `docker image prune -a -f` | owner | maybe | daemon up | med | off |
| `containers.docker-volumes` | same | `docker volume prune -f` | owner | maybe | daemon up | **high** | off |
| `containers.docker-all` | same | `docker system prune -a --volumes -f` | owner | maybe | daemon up | **high** | off |
| `containers.podman` | `podman` present | `podman system prune -f` (+ `--volumes` if opted) | owner | maybe | — | med | off |
| `containers.k8s-evicted` | `kubectl` present | report only in v0.1 (needs context; risky) | — | no | — | — | off |

Measure via `docker system df` and show the breakdown; only run the prune the user picked.

### 8.3 `desktop-pkgs`

| Item | Detect | Action | Rec | root | Guard | Risk | Def |
|---|---|---|---|---|---|---|---|
| `flatpak.unused` | `flatpak` present | `flatpak uninstall --unused -y` | owner | per-user | — | low | on |
| `flatpak.repair` | same | `flatpak repair` | owner | maybe | — | low | off |
| `flatpak.userdata` | `~/.var/app/*` with no installed app | Trash the `~/.var/app/<id>` dirs | **trash** | no | app not installed | med | off |
| `snap.old-revisions` | `snap list --all` shows `disabled` | `snap remove --purge <name> --revision <rev>` per disabled | owner | yes | — | med | on |
| `snap.cache` | `/var/lib/snapd/cache/*` | `snap` owns; delete cache files (root) | owner | yes | snapd idle | low | on |
| `snap.userdata` | `~/snap/<name>` for uninstalled snaps | Trash | trash | no | not installed | med | off |

### 8.4 `dev` — language/toolchain caches (the portable set; mostly per-user)

Prefer the **owner command**; fall back to Trash of the cache root only when the owner command is unavailable AND the root is a documented cache.

| Item | Detect | Action | Rec | Guard | Def |
|---|---|---|---|---|---|
| `dev.npm` | `npm` on PATH; `npm config get cache` | `npm cache clean --force` | owner | npm idle | on |
| `dev.pnpm` | `pnpm` on PATH; `pnpm store path` | `pnpm store prune` | owner | pnpm install idle | on |
| `dev.yarn` | `~/.yarn/cache`, `~/.cache/yarn` | Trash contents | trash | — | on |
| `dev.bun` | `bun` on PATH; `bun pm cache` | `bun pm cache rm` | owner | — | on |
| `dev.go-mod` | `go env GOMODCACHE` | `go clean -modcache` | owner | go/gopls idle | on |
| `dev.go-build` | `go env GOCACHE` | `go clean -cache` | owner | (Go documents concurrent-safe) | on |
| `dev.rust-registry-cache` | `$CARGO_HOME/registry/cache` | Trash/erase this subtree only; **keep `registry/src`, `registry/index`, `git`, `bin`, `toolchains`** | owner | cargo/rustc idle | on |
| `dev.rustup-downloads` | `$RUSTUP_HOME/downloads` | Trash contents | owner | — | on |
| `dev.pip` | `pip3 cache dir` | `pip3 cache purge` | owner | — | on |
| `dev.uv` | `uv` on PATH; `uv cache dir` | `uv cache prune` | owner | — | on |
| `dev.poetry` | `~/.cache/pypoetry/{artifacts,cache}` | Trash those two subdirs; **keep `virtualenvs`** | trash | — | on |
| `dev.conda` | conda on PATH | `conda clean --yes --index-cache --tarballs --logfiles` | owner | — | on |
| `dev.gem` | `~/.gem/ruby/*/cache`, `~/.bundle/cache` | Trash contents | trash | — | on |
| `dev.rbenv` | `~/.rbenv/cache` | Trash | trash | — | on |
| `dev.cpan-build` | `~/.cpan/build` | Trash; **keep `~/.cpan/sources`** | trash | — | on |
| `dev.nix` | `nix-collect-garbage` present | `nix-collect-garbage --delete-older-than 30d` | owner | — | on |
| `dev.corepack` | `corepack` present; `$COREPACK_HOME` | `corepack cache clean` | owner | — | on |
| `dev.mise` | `mise` present; `mise cache path` | `mise cache clear` | owner | — | on |
| `dev.frontend-caches` | `$XDG_CACHE_HOME/{typescript,electron,node-gyp,vite,webpack,eslint,prettier,parcel-cache}` | Trash each | trash | — | on |
| `dev.pycache` | project roots: `__pycache__`, `*.pyc` | Trash (only `.pyc`-bearing dirs) | trash | git-tracked check | on |
| `dev.docker-buildx` | `~/.docker/buildx/cache` | Trash | trash | — | on |
| `dev.cloud-cli` | `~/.kube/cache`, `~/.aws/cli/cache`, `~/.config/gcloud/logs`, `~/.azure/logs` | Trash | trash | — | on |
| `dev.gh-cli` | `gh` present; `gh` cache dir | `env XDG_CACHE_HOME=<parent> gh config clear-cache` | owner | — | on |

> Port these from Mole's `lib/clean/dev.sh` **behaviourally** (its reasoning about what to keep, e.g. Cargo `registry/src`, Poetry `virtualenvs`, `~/.cpan/sources`, HF/torch/W&B model roots) — but re-implement, and cite the reasoning in code comments.
>
> **Plan 09:** the `Def` column is the catalog author's intent; effective
> pre-selection is `DefaultOn && Regen <= cheap && !Gated`. Items classified
> `expensive` (`go-mod`, `rust-registry-cache`, `rustup-downloads`, `conda`,
> `nix`) are therefore shown but off. A cache touched within `[dev] max_age`
> (default 30d) is `Gated` and also left off. See §8.9.

### 8.5 `user-cache` — XDG cache sweep

The sweep is curated. Only a small allowlist of cheap caches is pre-selected;
known-expensive caches and unrecognized entries are shown but off.

| Item | Detect | Action | Rec | Def |
|---|---|---|---|---|
| `user-cache.app-caches` (cheap allowlist: `fontconfig`, `mesa_shader_cache`, `gstreamer-1.0`, `tracker3`) | entries under `$XDG_CACHE_HOME` | Trash the entry | trash | on |
| `user-cache.app-caches` (known-expensive: `huggingface`, `torch`, `ms-playwright`, `bazel`, `JetBrains`, ...) | entries under `$XDG_CACHE_HOME` | Trash the entry; classified `expensive` | trash | off |
| `user-cache.app-caches` (unrecognized) | entries under `$XDG_CACHE_HOME` | Trash the entry; classified `cheap` | trash | off |
| `user-cache.thumbnails` | `$XDG_CACHE_HOME/thumbnails` | Trash (regenerable; low risk) | trash | on |
| `user-cache.trash` | `$XDG_DATA_HOME/Trash` | **Empty** Trash (`trash-empty`) — irreversible; default OFF, explicit | owner | off |

Each individual cache dir is its own row so the user can keep specific ones; the
whitelist also covers this. `[user-cache] sweep_unknown = true` restores the old
pre-selection of unrecognized entries; `[user-cache] max_age` idle-gates a
recently-used entry.

### 8.6 `downloads` — installer/content sweep

| Item | Detect | Action | Rec | Def |
|---|---|---|---|---|
| `downloads.installers` | `~/Downloads`, `~/` : `*.deb *.rpm *.AppImage *.iso *.pkg.tar.zst *.snap *.flatpakref` older than 30d | Trash | trash | on |
| `downloads.old` | `~/Downloads` entries older than 90d > 100M | Trash (list only; user picks) | trash | off |

### 8.7 `trash` category
Empty Trash is NOT part of default `clean`; exposed as `user-cache.trash` (above) and `burrow trash empty`.

### 8.8 Size measurement
- Directory trees: gdu's analyzer (`analyze.CreateAnalyzer()`), fall back to `du -skP` under timeout.
- Single files: `os.Stat` (on-disk = `stat.Blocks*512` via `syscall.Stat_t`; apparent = `Size()`).
- Hardlinks: use `fs.HardLinkedItems` so shared inodes aren't double counted (gdu provides this).
- Unknown/partial → `SizeKnown=false`, render "unknown", never silently show 0 as empty (Mole's `scan_status` lesson).

### 8.9 Regeneration cost and idle gating (Plan 09)

Safe does not imply worth deleting. Every target carries a **regeneration cost**
`Regen` in addition to `Risk`:

| Regen | Meaning | Selection |
|---|---|---|
| `free` | regenerated automatically (thumbnails, frontend caches, `__pycache__`) | pre-selected |
| `cheap` | regenerated quickly, at most a small download (npm/pip/uv caches) | pre-selected |
| `expensive` | large download, network, or long rebuild (model weights, browser downloads, Go module cache, Nix store) | shown, **off** |
| `data` | not meaningfully regenerable | shown, **off** |

`Risk` still describes the blast radius of the deletion; `Regen` describes the
cost to rebuild. They are independent. Effective pre-selection is:

```
selected = DefaultOn && Regen <= cheap && Risk != high && !Gated
```

`Gated` marks a target a policy advises keeping (currently an idle-age window:
`[dev] max_age`, `[user-cache] max_age`, default 30d). A gated target is shown
with an evidence line and can still be selected by hand. `Regen` and `Gated`
affect selection only; they never change how a target executes, and the
recovery contract (I2) is untouched.

The generic XDG sweep is a curated allowlist rather than a catch-all, and
`purge` adds project-activity awareness (§11). A `trash`-contract expensive cache
still occupies space until the Trash is emptied; that trade-off (recoverability
over immediate reclamation) is deliberate and noted as a follow-up.

---

## 9. Trash module (freedesktop spec, native)

Implement the freedesktop.org Trash Specification (spec: `https://specifications.freedesktop.org/trash-spec/`).

- **Home trash:** `$XDG_DATA_HOME/Trash/` (default `~/.local/share/Trash/`) with `files/` and `info/`.
- **Cross-filesystem:** for a file on another mount, use `$topdir/.Trash/$uid/` if `.Trash` exists and has the sticky bit and is not a symlink; else `$topdir/.Trash-$uid/`. Create if needed.
- **Info file:** `info/<name>.trashinfo`, INI:
  ```
  [Trash Info]
  Path=<percent-encoded absolute original path>
  DeletionDate=<YYYY-MM-DDThh:mm:ss>
  ```
- **Name collisions:** if `files/<name>` exists, append `.N` or a suffix; the info filename must match the `files/` entry name.
- **Ops:** `Put(path)` (atomic rename within same fs; fall back to copy+unlink only if cross-device and target fs supports it), `List()`, `Restore(infoFile)`, `Empty()`.
- **Fail-closed:** if the Trash move cannot complete, return an error; the caller MUST abort that item, never fall back to `unlink` unless `--permanent` is set.
- Use `os.Rename` for same-device; detect device via `Stat_t.Dev`.
- Encode paths per spec (percent-encode, but keep `/`); decode for `Restore`.
- No third-party dep; ~300 lines + tests. Optionally honor `gio trash`/`trash-cli` if the user sets `trash.backend = "external"` in config (not default).

---

## 10. Analyze module

- **Engine:** gdu's `analyze.CreateAnalyzer()` / `CreateTopDirAnalyzer()`; item type `fs.Item`; ignore `/proc,/dev,/sys,/run` by default; honor `-x`, `-H`, `-a`.
- **UI (Bubble Tea):** a navigable tree list.
  - Keys: `↑/k` `↓/j` move, `→/l/Enter` enter dir, `←/h` parent, `s` sort size, `n` sort name, `t` sort mtime, `c` item count toggle, `/` filter, `d` trash selected, `D` trash highlighted, `space` multi-select, `a` select all in view, `?` help, `q` quit.
  - Header: path breadcrumb, free space (`statfs`), total under cursor, sort mode.
  - Rows: size bar + human size + name; `[R]` marker for already-known reclaimable (e.g., a category root).
  - **`d`/`D` route through `trash.Put`** with identity snapshot + confirm; disabled under `--no-delete`.
- **Non-interactive:** `burrow analyze --json PATH` emits a stable schema:
  ```json
  {"path":"...","total_size":N,"total_files":N,"entries":[{"name":"...","path":"...","size":N,"is_dir":true}],"largest":[...],"scan_status":"complete|partial|unavailable"}
  ```
- Reuse gdu rather than re-render its TUI, so we can fold in "this dir is a burrow category" hints later.

---

## 11. Purge module (project artifacts)

- **Artifact patterns (v0.1):** `node_modules`, `target` (Rust), `.venv`/`venv`, `__pycache__`, `.next/cache`, `build`, `dist`, `.gradle`, `_cacache`. Pinned list, extensible via config.
- **Discovery:** scan configured project roots (default `~/Projects`, `~/src`, `~/dev`, `~/code`, `~/work`; overridable via `purge_paths`). Bound depth (default 6). Prune heavy subtrees during scan.
- **Git protection (port Mole's rule):** for any candidate inside a git repo, if it is git-tracked (or git state is unknown), **keep it**. Use one `git ls-files -z` per repo per run (bounded), not per-candidate. Directories that are themselves a repo, or contain a nested repo, are kept.
- **Active-project guard (Plan 09):** keep a candidate whose owning repository saw a commit within `[purge] active_within` (default 30d), regardless of artifact age. The guard is advisory: a git error contributes no keep reason (the tracked-set check stays the hard protection).
- **Per-type age (Plan 09):** `[purge] max_age_by_type` gives each artifact its own inactivity window (`node_modules` 30d, `target`/`.next/cache` 7d, `__pycache__` disabled, ...); `max_age` (default 7d) is the fallback. `--age` overrides the whole policy.
- **Generic-name markers (Plan 09):** `build`, `dist`, `target`, and `.gradle` are only candidates when the parent holds an ecosystem marker (`package.json`, `Cargo.toml`, `pyproject.toml`, `go.mod`, `pom.xml`, `build.gradle`, `Makefile`, ...). Without one the candidate is kept.
- **Age filter:** default 7d of no file activity (mtime) for types without an override, overridable with `--age`; can't verify → keep.
- **Action:** **permanent delete** of the artifact dir (Owner contract-ish; rebuildable). Confirm in preview; `--dry-run` supported. (Artifacts are rebuildable so Trash is optional; default is permanent after explicit selection, matching Mole `purge`.)
- **UI:** grouped by project, with artifact type + age + size; keys per §10 with `space`/`a` selection, `[`/`]` project jump, `/` search.

---

## 12. Operation journal & `history`

- **Format:** append-only JSONL at `$XDG_STATE_HOME/burrow/operations.jsonl` (default `~/.local/state/burrow/operations.jsonl`).
- **Record schema:**
  ```json
  {"ts":"2026-10-05T12:00:00Z","run_id":"<ulid>","command":"clean","item":"dev.npm",
   "action":"owner","argv":["npm","cache","clean","--force"],"path":"",
   "status":"ok|skipped|failed|dry-run","bytes":12345,"reason":"","host":"h","burrow_version":"0.1.0"}
  ```
- **Rules:** one record per attempted target; `run_id` groups a run; a `run_start`/`run_end` pair frames runs (like Mole's `run_id`/`attribution`). Journal write failure must not abort cleanup but must warn.
- **`burrow history`:** table of recent runs (time, command, freed bytes, ok/skip/fail counts); `--json` emits raw records; `--run <id>` filters.
- **Undo:** `burrow history --restore <run_id>` restores all `trash`-contract items from that run that are still in Trash (via their `.trashinfo`). `owner`-contract items are never restorable (state that, don't imply otherwise).

---

## 13. Whitelist & config

### 13.1 Whitelist
- File: `$XDG_CONFIG_HOME/burrow/whitelist` (newline-delimited absolute paths/globs; `#` comments).
- Semantics (port Mole): a whitelisted path protects itself **and its parent** from the generic user-cache sweep. Matching is prefix-based on resolved physical paths.
- CLI: `burrow clean --whitelist` to manage interactively.

### 13.2 Config (`config.toml`)
```toml
[general]
color = "auto"          # auto|always|never
log_level = "info"

[clean]
default_categories = ["dev","user-cache","desktop-pkgs","downloads"]
journald_max_size = "500M"      # or use journald_max_age
journald_max_age  = ""          # e.g. "14d"; exactly one of size/age
min_size = "0"
protected_paths = ["/etc","/usr","/var/lib"]   # extra protections

[trash]
backend = "native"              # native|external
external_command = ""           # e.g. "trash-put" if backend=external

[purge]
paths = ["~/Projects","~/src","~/dev"]
artifact_dirs = ["node_modules","target",".venv","venv","__pycache__",".next/cache","build","dist",".gradle","_cacache"]
max_age = "7d"                  # fallback inactivity window
active_within = "30d"           # keep artifacts in a recently-committed repo
[purge.max_age_by_type]         # per-artifact overrides; "0" disables the filter
node_modules = "30d"
target = "7d"
"__pycache__" = "0"

[dev]
max_age = "30d"                 # idle gate for dev caches

[user-cache]
sweep_unknown = false           # true = pre-select unrecognized app caches
max_age = "30d"

[analyze]
one_file_system = false
ignore = ["/proc","/dev","/sys","/run"]

[downloads]
installer_extensions = [".deb",".rpm",".AppImage",".iso",".pkg.tar.zst",".snap"]
installer_min_age = "30d"
```
- Precedence: CLI flag > env (`BURROW_*`) > config file > built-in default.
- `burrow config` prints the effective config + resolved paths.

---

## 14. Testing strategy

### 14.1 Unit / property
- `internal/safe`: table + **fuzz** tests for `ValidateForDeletion` (seeds: `..`, `//`, control chars, symlink roots, protected paths, non-absolute). Property: never returns "ok" for a path outside an allowed root.
- Identity snapshot: simulate swap (create file, snapshot, rename it away, replace) → expect `guard-stop`.
- `internal/trash`: golden `.trashinfo` files; round-trip put→list→restore; collision naming; cross-device (use a loopback mount in the container matrix).
- Distro adapters: given a fixture `/etc/os-release` + stub command table, assert the right argv per distro.
- Journal: schema validates; `run_id` grouping; restore only touches `trash` items.

### 14.2 Integration matrix (containers, no real host mutation)
Run in Docker/podman with a **fixture filesystem** and **stubbed owner commands** (a `testbin/` dir prepended to PATH exposing fake `apt`, `docker`, `journalctl`, … that record their argv and emit canned sizes). Assert:
- preview → execute produces exactly the planned argv set;
- `--dry-run` produces the plan but zero mutations;
- guards trip correctly (simulate a held dpkg lock, a running `cargo`);
- whitelist excludes correctly;
- exit codes (§5.5).
Images: `ubuntu:24.04`, `debian:12`, `fedora:41`, `archlinux:latest`, `opensuse/leap` (last three for the future dnf/pacman/zypper adapters — mark xfail/skip until implemented).

### 14.3 Golden output
Snapshot CLI table output and `--json` for a fixed fixture; diffs must be reviewed, not auto-accepted blindly.

### 14.4 Safety regression suite
A dedicated `safety_test.go` + `testdata/safety/` covering every invariant I1–I7 with a test that **fails if the invariant is violated**, including: "no mutation happens before preview," "root without SUDO_USER refuses," "trash unavailable never falls back to unlink."

### 14.5 Lint/CI gates
`gofmt`, `go vet`, `golangci-lint`, `govulncheck`, unit tests, container matrix, and a **"no direct os.Remove of catalog paths"** static check (an AST/grep gate ensuring mutations only flow through `safe`/`trash`/`owner` — the analogue of Mole's `audit_destructive_sinks.py`).

---

## 15. Build, release, packaging

- `goreleaser` for linux amd64/arm64 static binaries (CGO_ENABLED=0), checksums, and a `curl | sh` installer that verifies checksums.
- Packages: `.deb`, `.rpm`, `.pkg.tar.zst`, and a Homebrew-free tap? No — Linux: distro packages + a GitHub release. Provide `install.sh` (pin-able, checksum-verified, fail-closed — mirror Mole's install discipline).
- Man pages generated from cobra (`burrow docs`/`doc.GenManTree`).
- Version stamped via `-ldflags -X`.

---

## 16. UX details (clean preview screen)

`clean` is TUI-first: on a terminal the preview TUI opens by default, including
for `--dry-run` (read-only — confirming shows the plan and exits without
mutating). `--plain`, `--json`, `--yes`, or a non-TTY stdout select the
non-interactive path. `--json` remains the stable machine schema.

- Top: detected platform (`Ubuntu 24.04 · apt · systemd · 118 GB free`).
- Rows are a collapsible tree: category header (`▸ Developer tools   2.4 GB / 6 items`), optional
  presentation sub-group, then item rows. Categories and groups with many items start collapsed;
  `→`/`←` expand and collapse, and `space` on a category or group toggles every non-high-risk member.
- Per row: checkbox · name · size · `[trash]`/`[owner]`/`[delete]` tag · `(root)` if elevating · risk dot.
- Footer: `Selected: 3.8 GB across 14 items · [space] toggle [a] all-safe [/] search [enter] review [q] quit`.
- A second "review" screen lists every exact owner argv that will run. Large trash selections are
  aggregated by group (`trash 327 item(s), 44.5 GB — <project>`); the full per-path list is available
  via `--json` (this is the "what will change" screen; Mole's product filter demands it).
- High-risk items render red and are never auto-selected.
- The non-interactive (`--plain`) preview caps item rows per group and points at `--json` for the full list.
- After execution: per-item result lines + totals + `Freed X (df: 118GB → 121GB)` + `journal: <path>`.

---

## 17. Phased plan (milestones with acceptance criteria)

**M0 — Skeleton + safety core.**
Deliver: repo layout, config loader, journal (write+history), trash module, `safe` package (validation + identity), elevation env resolution, `burrow version|config|history`.
Accept: safety_test.go I1–I7 green; trash round-trip green; `history` reads a hand-written journal.

**M1 — Analyze (read-only ship).**
Deliver: gdu adapter, Bubble Tea analyze TUI, `--json`, `d`/`D` via trash.
Accept: scans 10 GB fixture in <2s warm; `--json` schema stable; trash-from-analyze writes journal + is restorable.

**M2 — `clean` system category + apt adapter (Debian/Ubuntu).**
Deliver: engine pipeline (discover→plan→preview→execute→journal), preview/review UI, guards (apt/dpkg), apt items (§8.1).
Accept: container matrix §14.2 green for ubuntu/debian with stubbed apt; `--dry-run` mutates nothing; guard-cap test trips correctly.

**M3 — `clean` dev + user-cache + downloads.**
Deliver: §8.4/8.5/8.6 items, whitelist, owner runners for npm/pnpm/bun/go/pip/uv/poetry/conda/nix/etc.
Accept: each dev item's fallback/owner path tested; Cargo `registry/src` and Poetry `virtualenvs` provably kept; whitelist honored.

**M4 — containers + flatpak/snap.**
Deliver: §8.2/8.3, docker `system df` sizing, snap revisions, flatpak unused.
Accept: stubbed docker/snap/flatpak tests; high-risk items default OFF.

**M5 — `purge`.**
Deliver: §11 with git protection + age filter + UI.
Accept: git-tracked fixture kept; untracked artifact purged; nested-repo fixture kept.

**M6 — Polish + second distro.**
Deliver: `--json` everywhere, shell completion, man pages, `status` (thin), dnf adapter (Fedora container green), packaging + install.sh.
Accept: Fedora matrix green for dnf items; release artifacts build; `install.sh` checksum-verified.

---

## 18. Risks & mitigations

| Risk | Mitigation |
|---|---|
| Distro fragmentation explodes scope | Adapter interface + D10 (ship apt first); other distros are data, not code. |
| A guard misses a running owner → corrupt state | Fail-closed tri-state guards; lock files + process checks; container matrix simulates held locks. |
| "Clean" users expect = "free space" but some items are small | Accurate measurement + honest "unknown"; no fake savings. |
| High-risk items damage systems | Default OFF, red, explicit review screen; never auto-select. |
| Trash not available (headless) | Refuse (I5/D12); `--permanent` is the only override and is explicit. |
| gdu library API churn (v5) | Pin exact version; wrap in `internal/scan` so a shell-out fallback is a one-file change. |
| Copying Mole code (GPL contamination) | Independent implementation; behavioural port only; keep a provenance note per file. |

---

## 19. Definition of done (v0.1)

- `burrow analyze|clean|purge|history|config` work on Ubuntu 24.04 + Debian 12.
- `--dry-run`, whitelist, preview+review, and JSON journal are in from day one and covered by tests.
- Safety invariants I1–I7 have passing tests; container matrix green; `govulncheck` clean.
- No mutation path bypasses `safe`/`trash`/`owner` (static gate enforced in CI).
- High-risk items ship OFF; nothing auto-runs; no daemon.
- README documents every catalog item with its owner command, recovery contract, and risk.

---

## 20. Open questions (need a human decision; agent should implement defaults flagged here)

1. **Name/binary.** Working `burrow`/`bw` — confirm or replace before v0.1. (Check distro/npm/PyPI name collisions.)
2. **Default `journald` bound:** size (500M) vs age (14d)? (Default: size 500M.)
3. **Is `system.orphans` in scope at all** for a first release, given it removes packages? (Default: present but OFF.)
4. **Filesystem-specific extras** (btrfs subvolume/snapshot listing, zfs) — out of v0.1; confirm.
5. **`status` inclusion** — keep thin (gopsutil) or drop? (Default: implement minimal, non-default-selected.)
6. **Telemetry** — none; confirm there is no appetite for opt-in usage stats.
7. **`--permanent` on trash items** — keep as escape hatch, or remove to force Trash-always? (Default: keep, but warn + journal.)

---

## 21. Appendix

### 21.1 Exact owner-command reference (v0.1 relevant)
```
# Debian/Ubuntu (apt/dpkg)
apt-get clean                      # empty /var/cache/apt/archives
apt-get autoclean                  # remove only obsolete archives
apt-get autoremove --purge         # remove unneeded deps (+ config)  [HIGH RISK]
dpkg -l | awk '/^rc/{print $2}'    # residual-config package list
dpkg --purge <pkg...>              # purge residual config
journalctl --vacuum-size=500M      # bound journald (or --vacuum-time=14d)
systemd-tmpfiles --clean           # apply tmpfiles aging rules
# Fedora/RHEL (dnf)            dnf clean all ; dnf autoremove ; package-cleanup --oldkernels --count=2
# Arch (pacman)                paccache -rk1  (or pacman -Sc) ; pacman -Qtdq ; pacman -Rns <orphans>
# openSUSE (zypper)            zypper clean --all ; zypper packages --orphaned ; zypper remove <orphans>
# Alpine (apk)                 apk cache clean
# Containers                   docker builder prune -f ; docker image prune -f| -a -f ; docker volume prune -f ; docker system prune -a --volumes -f ; docker system df
# Flatpak/Snap                 flatpak uninstall --unused -y ; flatpak repair ; snap list --all (disabled revs) ; snap remove --purge <n> --revision <r> ; snap set system refresh.retain=2
# Dev toolchains               npm cache clean --force ; pnpm store prune ; bun pm cache rm ; go clean -modcache|-cache ; pip3 cache purge ; uv cache prune ; conda clean --yes --index-cache --tarballs --logfiles ; nix-collect-garbage --delete-older-than 30d ; corepack cache clean ; mise cache clear ; gh config clear-cache
```

### 21.2 Reference specs & sources
- freedesktop **Trash Specification**: https://specifications.freedesktop.org/trash-spec/
- freedesktop **XDG Base Directory Specification**: https://specifications.freedesktop.org/basedir-spec/ → `$XDG_DATA_HOME` (`~/.local/share`), `$XDG_CONFIG_HOME` (`~/.config`), `$XDG_STATE_HOME` (`~/.local/state`), `$XDG_CACHE_HOME` (`~/.cache`), `$XDG_RUNTIME_DIR`.
- **gdu** (scan engine; MIT — verify): https://github.com/dundee/gdu ; import `github.com/dundee/gdu/v5/pkg/analyze`, `.../pkg/fs`.
- **Bubble Tea / Lipgloss / Bubbles**: https://github.com/charmbracelet
- **gopsutil** (status metrics): https://github.com/shirou/gopsutil
- **cobra** (CLI): https://github.com/spf13/cobra
- Prior analysis: `~/workspace/docs/mole-linux-portability.md` (Mole study — architecture, safety contract, why a port fails).
- Behavioural reference for dev-cache keep/discard reasoning: Mole `lib/clean/dev.sh`, `lib/core/file_ops.sh` (read-only reference; do not copy source — GPL-3.0).

### 21.3 Go module layout (target tree)
```
github.com/<org>/burrow
├── cmd/burrow/main.go
├── internal/{cli,engine,catalog,action,owner,distro,trash,safe,journal,whitelist,config,scan,tui,metrics,i18n}
├── docs/            (man pages, guide)
├── testdata/        (fixtures, golden)
├── testbin/         (stubbed owner commands for integration)
├── scripts/         (check.sh: fmt+vet+lint+test+static-safety-gate)
├── .goreleaser.yaml
└── go.mod
```
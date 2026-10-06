# Mole (tw93/Mole) — what it is, portability to Linux, and a Linux feature-set recommendation

Date: 2026-10-05
Method: read the cloned repo at `~/workspace/research/mole-study/mole` (v1.57.0, commit `b8180ab`, GPL-3.0, 69.4k★), plus primary sources cited inline. Claims about mole code are traced to file:line; claims about Linux tooling are sourced.

---

## 1. TL;DR

- Mole is **not portable by recompiling**. The cleanup engine is ~53K lines of Bash hard-wired to macOS (`~/Library/*`, `/System`, `sw_vers`, `csrutil`, `launchctl`, `osascript`, Finder Trash API, `mdfind`, `tmutil`, BSD `stat -f`), and the Go disk scanner is literally `//go:build darwin`.
- The author's own answer to "port it" was a **rewrite**, not a port: the `windows` branch is a PowerShell reimplementation (WinMole), proposed in issue #273 as the base "to extend for Linux support later" — and that branch is now **2131 commits behind `main`** (abandoned/stale).
- The *approach* — prescriptive, previewable, categorized cleanup + a disk explorer + dry-run/whitelist/op-log — **does make sense on Linux**, but the center of gravity shifts: on Linux you delegate to **owner commands** (`apt`, `dnf`, `pacman`, `journalctl`, `docker`, `flatpak`, `snap`, `nix`) instead of guessing paths. Mole's macOS killer feature, **orphaned-app-data removal**, largely does not map (no bundle-id install footprint).
- **Linux is not actually missing a cleaner** — it's missing an *integrated, dev-cache-aware one*. `BleachBit` is the prescriptive cleaner (GUI-first, shallow on dev/package caches); `ncdu`/`gdu`/`dust` nail analysis. The gap is the unified opinionated TUI on top of both. That gap is real and worth building.
- **Recommendation:** don't port Mole's code; build a small orchestrator that (a) reuses `gdu`/Bubble Tea for analysis, (b) shells out to package-manager / system owner commands for cleanup, (c) ports Mole's *safety contract* (path validation, dry-run, whitelist, operation log, trash-backed undo), scoped to Debian/Ubuntu + systemd first. Name it something else (GPL + Mole's own trademark request).

---

## 2. What Mole actually is (verified from source)

**Stack:** a Bash router + per-command scripts + shared libs, plus two Go Bubble Tea TUIs.

```
mole              # router only: parses args, renders menu, dispatches
bin/analyze.sh clean.sh status.sh optimize.sh purge.sh uninstall.sh installer.sh ...
lib/core/         # safety, UI, file ops, op-log, app protection, timeouts  (~12.3K lines)
lib/clean/        # the cleanup targets                                  (~19.7K lines)
lib/optimize/     # bounded maintenance (DNS, Spotlight, DB vacuum, disks) (~3.2K lines)
lib/uninstall/    # app + leftover removal                               (~3.7K lines)
lib/ui/ lib/manage/
cmd/analyze/      # Go: disk explorer TUI (Bubble Tea)                    (~888K dir)
cmd/status/       # Go: live health dashboard (gopsutil)                 (~
```
README + AGENTS.md confirm: `mo clean | uninstall | optimize | analyze | status | purge | installer`.

**Size:** ~53,283 lines of shell + ~26,787 lines of Go (excluding tests). 100+ distinct `clean_*` target functions.

**Safety model (the genuinely good part, and the part worth stealing):**
- All deletion routed through `lib/core/file_ops.sh` → `mole_delete` (Trash + permanent, with forensic `operations.log`). Raw `rm -rf` requires a `# SAFE: <reason>` annotation enforced by a CI audit script.
- Per-path **identity snapshots** (parent inode + target inode + optional sha256) re-checked at the deletion sink to defeat symlink/path swaps mid-run.
- Path validation, protected-path rules, app-bundle protection (bundle-ID globs), a **whitelist** (`~/.config/mole/whitelist`), and global `--dry-run`.
- Trash mode "fails closed": if the Trash move fails it refuses to fall back to permanent delete (`file_ops.sh:2509-2560`).
- Owner-command delegation is already a first-class pattern (Go module cache via `go clean -modcache`, npm via `npm cache clean`, uv via `uv cache prune`, nix via `nix-collect-garbage`), with process-state guards so it won't prune a live toolchain.

---

## 3. Can it be ported to Linux? — No, not by porting. Evidence:

**3.1 The engine is macOS-targeted.** macOS-only API usage across `lib/` + `bin/`:

| API | refs | API | refs |
|---|---|---|---|
| `mdfind` (Spotlight) | 82 | `launchctl` | 9 |
| `plutil` | 51 | `diskutil` | 8 |
| `stat -f` (BSD stat) | 28 | `sw_vers` | 6 |
| `osascript` | 24 | `mdutil` / `csrutil` / `pmset` | 3 / 2 / 1 |
| `tmutil` (Time Machine/APFS snapshots) | 21 | `getconf DARWIN_USER_DIR` | 1 |

Clean targets are macOS nouns: `clean_xcode_*` (5 fns), `clean_homebrew`, `clean_finder_metadata`, `clean_ds_store_tree`, `clean_time_machine_failed_backups`, `clean_local_snapshots`, `clean_apple_silicon_caches`, `clean_orbstack`, `clean_orphaned_app_data`, `clean_group_container_caches`, etc. The privilege model assumes macOS **SIP + TCC privacy prompts** (`MOLE_ERR_SIP_PROTECTED`, "macOS denied Trash access… move the item to Trash in Finder").

**3.2 The Go scanner is build-tagged darwin.** `cmd/analyze/scanner.go` line 1 is `//go:build darwin`; it uses `syscall.Stat_t` + `stat.Blocks*512` and shells to `du`/`find`/`mdfind`. `cmd/status/metrics_*` are Darwin-specific (battery/SMC/Bluetooth/GPU). Only `gopsutil` (the metrics dependency) is cross-platform.

**3.3 The author already answered this.** Issue #273 ("Add Windows support — Cross-platform Mole") by contributor `bhadraagada` says the Windows port "mirrors Mole's architecture" and is "**easy to extend for Linux support later**." It shipped as the `windows` branch = **PowerShell 5.1 rewrite** (`bin/*.ps1`, Pester tests), 74 commits ahead / **2131 commits behind** `main` as of this read → effectively dead. Conclusion: even the maintainer treats cross-platform as a **platform-layer rewrite that reuses the architecture and Go TUIs**, not a recompile.

**3.4 What IS portable (the reusable ~10%):** the router pattern; `file_ops.sh`'s safety contract (validation, identity snapshot, dry-run, whitelist, op-log, trash fail-closed); the categorical preview UX; the Bubble Tea TUI shell; the purified cross-platform dev-cache targets (npm/pnpm/yarn/bun, uv/pip, poetry, go, rust, gem, cpan, conda, nix, frontend `~/.cache`s, cloud CLIs). These are exactly the targets that already key off `$HOME`/XDG paths.

**Verdict:** a "port" means rewriting the platform layer (targets + privilege + Trash + maintenance) — i.e. ~90% of the shell engine and the scanner build tag. That is a rewrite with a familiar UI, not a port.

---

## 4. Does Mole's *approach* make sense on Linux? — Mostly yes, with 5 structural differences

1. **"Where did my space go" is already solved.** `ncdu`, `gdu`, `dust`, `dua`, plus GUIs `baobab`/`qdirstat`. Analysis is table stakes, not a differentiator — but folding cleanup into the same view (Mole's move) is still a real UX win.
2. **Cleanup on Linux = orchestrate owner commands, not guess paths.** The safe, correct action for a package cache is `apt clean` / `pacman --sync --clean` / `dnf clean all`; for logs `journalctl --vacuum-*`; for containers `docker system prune`; for flatpak/snap/nix their own GC. This is *safer* than macOS path-guessing because the system owner defines "safe." Mole already models this (see §2) — the same discipline scales better on Linux.
3. **No single `~/Library`; use XDG.** Roots are `$XDG_CACHE_HOME` (~/.cache), `$XDG_CONFIG_HOME` (~/.config), `$XDG_DATA_HOME` (~/.local/share), `$XDG_STATE_HOME` (~/.local/state), `$XDG_RUNTIME_DIR`. Per the freedesktop XDG Base Directory spec. Cache cleanup therefore becomes "sweep XDG dirs, app-aware," plus toolchain-specific roots.
4. **Recoverability is two-tier, not one.** User caches → move to **freedesktop Trash** (`trash-cli`'s `trash-put`, or `gio trash`) = recoverable. System/pkg caches → owner prune = *not* recoverable but re-downloadable. Mole's single "always Trash" contract (`mole_delete` default = recoverable) does not hold for the highest-value Linux targets (`/var/cache/apt`, journald). Label each item by recovery contract instead.
5. **Mole's killer feature doesn't translate: orphaned app data.** On macOS an app is a bundle + scattered `~/Library` state keyed by bundle ID, so leftovers after uninstall are a real, findable category. On Linux the **package manager owns the install** and removes its files; it "by design does not touch users' home directories," so the leftover problem is *weaker and vaguer* (residual config after `apt remove` without `--purge`, `flatpak`/`snap` user data, docker/podman stubs in `~/.config`/`~/.local/share`). There is a version of this, but it is a minor tier, not the headline.

**Biggest product risk:** distro/init fragmentation (apt vs dnf vs pacman vs zypper; systemd vs not; desktop vs headless). Scope v1 to **one family (Debian/Ubuntu) + systemd**.

---

## 5. The Linux landscape today (what already exists)

| Tool | Class | Strengths | Gap |
|---|---|---|---|
| `ncdu` | analyzer | ncurses, ubiquitous, delete-to-trash | analysis only |
| `gdu` | analyzer | Go, fast, JSON out, `--web`, `--top`, SQLite persist | analysis only |
| `dust` | analyzer | Rust, instant tree of big dirs | analysis only |
| `dua` | analyzer | parallel, interactive | analysis only |
| `baobab` / `qdirstat` | analyzer GUI | treemaps | GUI, analysis only |
| **BleachBit** | **prescriptive cleaner** | cross-plat, XML cleaner defs, `--list-cleaners/--preview/--clean/--preset/--all-but-warning`, GUI+CLI, shred/wipe-free-space | GUI-first; shallow on dev-toolchain & pkg-manager caches; no unified disk-map+clean view; server/headless CLI is awkward |
| KDE Sweeper | cleaner | KDE-integrated | KDE/GUI only |
| Stacer | cleaner GUI | dashboards | GUI, stale |
| owner commands | the real cleanup | `apt clean/autoclean/autoremove`, `deborphan`, `journalctl --vacuum`, `paccache`, `docker system prune`, `flatpak uninstall --unused`, `snap set system refresh.retain`, `nix-collect-garbage` | scattered, no unified preview/undo/log |

**So:** Linux is not missing a cleaner. It's missing **one opinionated, terminal-native TUI** that unifies (a) a fast disk explorer, (b) categorized owner-command-backed cleanup with preview, and (c) dry-run / whitelist / operation-log / trash-undo — i.e. mole's *shape* over Linux's *commands*.

---

## 6. Recommended Linux feature set (if you build one)

Design principle: **be an orchestrator with a safety contract, not a deleter.** Every mutating action is either a freedesktop Trash move (recoverable) or an allow-listed owner command (re-downloadable). Never hand-delete package-manager state. Dry-run from day one.

### Tier 0 — Foundation (MVP spine; borrowed directly from Mole's contract)
1. Single binary, subcommands: `analyze | clean | purge | status | history`. (Router pattern.)
2. **Analyze TUI**: du-tree, top-N largest, size-on-disk vs apparent, sort/filter, `d` → move to Trash. Build on `gdu`'s Go core/libs or shell to it; don't rewrite a scanner badly.
3. **Categorized clean with a preview screen**: each category is a plugin emitting rows `path | size | action | recovery-contract`, aggregated with checkboxes; nothing runs without confirmation.
4. **Global `--dry-run`**, **whitelist** (`~/.config/<tool>/whitelist`), **operation log** (JSON, `history` to read), **Trash-backed undo** where applicable.
5. **Owner-command delegation layer** with a per-command process-guard (won't prune a live `apt`/`docker build`/`cargo`).
6. **Trash abstraction**: `trash-cli` → `gio trash` → refuse (fail closed, mirroring `mole_delete`).

### Tier 1 — The Linux high-value targets (the differentiators vs BleachBit)
- **Package caches**: `apt clean`/`autoclean`, `dnf clean all`, `pacman -Sc`/`paccache -r -k1`, `zypper clean`.
- **Orphans / unused**: `apt autoremove --purge`, `deborphan`, `dnf autoremove`, `pacman -Qtdq`; residual config (`dpkg -l | grep '^rc'`).
- **Logs / kernels**: `journalctl --vacuum-size/-time`, old rotated `/var/log`, old kernels via `apt autoremove --purge`.
- **Containers**: `docker system df` + prune (build cache / dangling images / volumes), podman, k8s evicted pods.
- **Flatpak/Snap**: `flatpak uninstall --unused` + `flatpak repair`; old snap revisions (`snap set system refresh.retain=2`, remove disabled revisions), snap cache.
- **Dev toolchain caches** (port near-verbatim from Mole; they're already XDG-based): npm/pnpm/yarn/bun, uv/pip, poetry, **Go (`GOMODCACHE`/`GOCACHE` via `go clean`)**, rust (`cargo registry/cache` + `rustup` downloads — keep `registry/src`, mirroring Mole's own reasoning), gem/rbenv, CPAN build, conda, `nix-collect-garbage`, and frontend caches (`~/.cache/{typescript,electron,node-gyp,vite,webpack,eslint,prettier,…}`).
- **XDG cache sweep** (`$XDG_CACHE_HOME`) with per-app awareness + whitelist (thumbnails, etc.).
- **Content junk sweep**: Trash, plus old installers in `~/Downloads`/`~` (`.deb/.rpm/.AppImage/.iso/.pkg.tar.zst`), with age filter.

### Tier 2 — Parity / polish
- **Purge project artifacts** (`node_modules`, `target`, `.venv`, `__pycache__`, `.next`, `build`, `dist`) across configured project roots, with git-tracked-file protection → ports ~1:1 from Mole and is high value for devs.
- `status` via `gopsutil` (CPU/mem/disk/net/process) — **low priority on Linux** (btop/htop/btm exist); ship thin or skip.
- `--json` output for automation; shell completion; quick-launcher integration.

### Explicit non-goals (mirroring Mole's own discipline)
- No system-path guessing — delegate to owner commands.
- No "privacy cleaner" (cookies/passwords/shred/wipe-free-space) — that's BleachBit's turf; different product.
- No distro-universal v1 — ship Debian/Ubuntu + systemd, then add families behind adapters.
- No auto-scheduled background daemon in v1 — one-shot, reviewable, logged.

### Reuse map (don't build)
- **Analyzer**: `gdu` (Go, JSON, `--web`) or `dust`/`ncdu`. Bubble Tea for TUI (same lib Mole uses).
- **Trash**: `trash-cli` / `gio trash` (freedesktop Trash Spec).
- **Owner commands**: apt / dnf / pacman / zypper / journalctl / docker / flatpak / snap / nix.
- **Safety contract**: port `file_ops.sh`'s ideas (path validation, identity snapshot, dry-run, whitelist, op-log, trash fail-closed) — reimplemented, not copied, for GPL hygiene.

---

## 7. Ranked call

1. **Build the "Linux mole" as a thin orchestrator, not a port** — reuse `gdu` + Bubble Tea, drive owner commands, port the safety contract. Scope: Debian/Ubuntu + systemd; commands `analyze`/`clean`/`purge`; ship dry-run + whitelist + op-log + trash-undo from commit 1. This is the only option that fills the actual gap (integrated, dev-aware, previewable) rather than re-treading BleachBit or `ncdu`.
2. **If you just want the result now and won't maintain a tool: don't build.** `BleachBit` (prescriptive blanket clean) + `gdu`/`ncdu` (analysis) covers ~80% today. The missing 20% — unified categorized view, dev-cache depth, undo/log — is the whole reason to build.
3. **Do not fork Mole's tree.** GPL-3.0 (a fork stays GPL) + its README asks forks to "give it a different name and credit Mole as the source." Forking 53K lines of macOS Bash to reach Linux is strictly more work than a focused rewrite.

**License note if you build:** MIT/Apache-2.0 for your own tool is fine (Mole's GPL does not infect an independent reimplementation); just don't copy its source.
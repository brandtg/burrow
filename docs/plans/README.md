# burrow — implementation plan index

This directory holds the phased implementation plans for `burrow`. Each plan is a
self-contained unit of work with its own goal, deliverables, tasks, and acceptance
criteria. Plans are numbered in dependency order; some can be executed in parallel.

Source of truth for product decisions: `docs/burrow-spec.md`.
Background / rationale: `docs/mole-linux-portability.md`.

Working name: `burrow` (binary `burrow`, alias `bw`).
License: Apache-2.0.
Target v0.1 platform: Debian/Ubuntu + Fedora/RHEL + systemd (pacman/zypper/apk
behind the same adapters, detection stubs until their plans land).

---

## Dependency graph

```
                          ┌─────────────────────────────┐
                          │ 01 foundation + safety core │   (blocking)
                          └──────────────┬──────────────┘
                                         │
                    ┌────────────────────┴────────────────────┐
                    │                                         │
          ┌─────────▼──────────┐                   ┌──────────▼───────────────────┐
          │ 02 analyze         │                   │ 03 clean engine + system/apt │
          └─────────┬──────────┘                   └──────────┬───────────────────┘
                    │                                         │
                    │                            ┌────────────┴────────────┐
                    │                            │                         │
                    │                  ┌─────────▼──────────┐   ┌──────────▼─────────────┐
                    │                  │ 04 dev + user-cache│   │ 05 containers +        │
                    │                  │    + downloads     │   │    flatpak/snap        │
                    │                  └─────────┬──────────┘   └──────────┬─────────────┘
                    │                            │                         │
          ┌─────────▼──────────┐                 │                         │
          │ 06 purge           │                 │                         │
          └─────────┬──────────┘                 │                         │
                    │                            │                         │
                    └──────────────┬─────────────┴──────────┬──────────────┘
                                   │                        │
                          ┌────────▼────────────────────────▼────────┐
                          │ 07 polish + status + dnf + packaging    │
                          └─────────────────────────────────────────┘
```

Machine-readable form (one edge per line, `A -> B` means B depends on A):

```
01 -> 02
01 -> 03
02 -> 06
03 -> 04
03 -> 05
02 -> 07
03 -> 07
04 -> 07
05 -> 07
06 -> 07
```

---

## Plan table

| Plan | File | Milestone | Depends on | Blocks | Parallel with | Status |
|------|------|-----------|------------|--------|---------------|--------|
| 01 | `01-foundation-safety-core.md` | M0 | — | all | — | done |
| 02 | `02-analyze.md` | M1 | 01 | 06, 07 | 03 | done |
| 03 | `03-clean-engine-system-apt.md` | M2 | 01 | 04, 05, 07 | 02 | done |
| 04 | `04-dev-user-cache-downloads.md` | M3 | 03 | 07 | 05, 06 | done |
| 05 | `05-containers-desktop-pkgs.md` | M4 | 03 | 07 | 04, 06 | done |
| 06 | `06-purge.md` | M5 | 01, 02 | 07 | 04, 05 | done |
| 07 | `07-polish-distro-packaging.md` | M6 | 02, 03, 04, 05, 06 | — | — | done |

All seven plans landed and `make gate` is green. The audit, deviations, known
gaps, and the open findings (F1-F6) are tracked in `docs/status.md`; read it
before making further changes.

---

## Execution waves (for orchestrating parallel agents)

Run one wave at a time; within a wave the plans are independent and can run
concurrently, subject to the shared-file notes below.

- **Wave 0:** Plan 01. Everything blocks on it. Do not start anything else.
- **Wave 1:** Plan 02 ∥ Plan 03.
- **Wave 2:** Plan 04 ∥ Plan 05 ∥ Plan 06.
  - Plan 06 only requires Plan 02, so it may start in Wave 1b (as soon as 02
    merges) if you want to pull it forward. It has no dependency on 03/04/05.
- **Wave 3:** Plan 07.

A plan is "mergeable" only when its acceptance criteria pass and `scripts/check.sh`
is green on the branch.

---

## Shared-file / merge-conflict map

Agents working in parallel must not both edit the same files. Ownership per plan:

| Path / package | Primary owner | Notes |
|---|---|---|
| `internal/safe`, `internal/trash`, `internal/journal`, `internal/config`, `internal/whitelist` | 01 | 04 consumes whitelist; coordinate changes |
| `internal/scan` | 01 (spike) then 02 | Plan 06 reads `internal/scan`; treat as frozen after 02 |
| `internal/engine`, `internal/action`, `internal/catalog` (framework) | 03 | 04/05 add catalog *data* + small Discover funcs |
| `internal/owner`, `internal/distro` | 03 | 04/05/07 add command tables / adapters |
| `internal/tui` | split by file | `analyze.go` (02), `clean.go` (03), `purge.go` (06). Do not edit another plan's model file. |
| `internal/cli` | additive | each plan adds its own command file; root wiring touched once in 01 |
| `cmd/burrow/main.go` | 01 | leave alone after 01 |
| `internal/metrics` | 07 | new package |
| `testdata/`, `testbin/` | 01 creates; others add subdirs | avoid editing shared fixtures; add new ones |

**Rule for parallel agents:** if a change requires touching another plan's owned
file, stop and record it as a cross-plan dependency rather than editing it. The
integration owner (Plan 01 for foundation, Plan 03 for engine) lands the change.

---

## Status legend

`not started` · `in progress` · `blocked` · `in review` · `done`

Update the Status column as work proceeds.
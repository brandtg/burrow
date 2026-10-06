# Plan 07 — Polish, `status`, dnf adapter, packaging

**Status:** not started
**Milestone:** M6
**Depends on:** 02, 03, 04, 05, 06
**Blocks:** none
**Parallel with:** none (integration plan)

## Goal

Close out v0.1: make output machine-consumable everywhere, add shell completion
and man pages, ship a thin `status`, add the second distro family (dnf/Fedora) to
prove the adapter abstraction, and produce reproducible release artifacts with a
checksum-verified installer.

## Deliverables

```
internal/cli/*.go            --json on clean/analyze/history/status; completion; docs cmd
internal/metrics/            gopsutil wrappers
internal/cli/status.go       thin status command
internal/distro/dnf.go       dnf adapter + items
.goreleaser.yaml             static linux amd64/arm64, checksums
install.sh                   checksum-verified, fail-closed
scripts/check.sh             extended: man-page gen check, goreleaser config lint
README.md                    catalog table: owner cmd, recovery, risk per item
docs/                        generated man pages + user guide
```

## Tasks

### 1. `--json` everywhere
- Ensure `clean`, `analyze`, `history`, `status` all support `--json` with stable,
  documented schemas. No human-formatted text on stdout when `--json` is set.
- Golden tests for each schema.

### 2. Shell completion + man pages
- `burrow completion <shell>` (cobra-generated) for bash/zsh/fish.
- `burrow docs` -> `doc.GenManTree` generating man pages into `docs/`.
- CI check that generated man pages are up to date.

### 3. `status` (thin)
- `internal/metrics` wrappers over `github.com/shirou/gopsutil`.
- CPU/mem/disk/net/process snapshot, one-shot, non-default-selected (spec §11).
- `--json` supported. Keep it thin; this is explicitly not the differentiator.

### 4. dnf adapter (proves the abstraction)
- Implement `internal/distro/dnf.go` against the Plan 03 `Adapter` interface.
- Items per spec §8.1: `dnf clean all`, `dnf autoremove -y`,
  `package-cleanup --oldkernels --count=2`.
- Guards: dnf lock/process detection.
- **This is the acceptance test for D10's adapter design**: if adding dnf requires
  touching the engine or catalog framework, that is a design bug — fix the
  framework, not the adapter.

### 5. Packaging + release
- `.goreleaser.yaml`: `CGO_ENABLED=0`, linux amd64/arm64, checksums, version
  stamped via `-ldflags -X`.
- Packages: `.deb`, `.rpm`, `.pkg.tar.zst`, plus GitHub release archives.
- `install.sh`: pin-able version, checksum-verified, fail-closed (mirror Mole's
  install discipline). No `curl | sh` without verification.
- Verify the static binary has no dynamic loader deps (`ldd` -> not a dynamic
  executable).

### 6. Documentation
- README catalog table documenting **every** item with its owner command, recovery
  contract, and risk (spec §19 DoD).
- User guide covering dry-run, whitelist, preview/review, journal/undo, config.

## Acceptance criteria

- Fedora container matrix green for dnf items (spec §14.2, M6).
- `--json` schemas golden and stable for all commands.
- Completion works in bash/zsh/fish; man pages generated and CI-verified.
- `status` renders and `--json` emits.
- Release artifacts build for linux amd64/arm64; checksums produced;
  `install.sh` verifies checksums and fails closed on mismatch.
- `govulncheck` clean; `scripts/check.sh` green.
- README documents every catalog item.

## Risks & notes

- **Adding dnf must be data-only.** If it is not, refactor the framework and
  update Plans 03/04/05 accordingly rather than special-casing dnf.
- **goreleaser + Go 1.26 toolchain.** Ensure the release pipeline provisions the
  pinned toolchain.
- **Man pages are generated**; never hand-edit them, and keep them out of the
  golden diff unless regenerated intentionally.
- `status` is optional per spec §20.5; ship thin and non-default-selected.

## Provenance

Installer discipline mirrors Mole's checksum-verified, fail-closed install
behavior, reimplemented clean-room. No Mole source copied.
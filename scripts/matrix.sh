#!/usr/bin/env bash
# scripts/matrix.sh — container integration matrix (spec §14.2).
#
# Builds the static binary and runs it inside distro containers with testbin/
# first on PATH and a fixture home. Each case asserts the planned/executed argv
# set, dry-run purity, guard trips, whitelist exclusion, and exit codes. It
# never mutates a real host path.
#
# Usage: scripts/matrix.sh [--allow-skip]
#   --allow-skip  exit 0 with a recorded reason when no container runtime exists
#                 (used by the gate); without it, a missing runtime is fatal.
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
ALLOW_SKIP=0
[[ "${1:-}" == "--allow-skip" ]] && ALLOW_SKIP=1

runtime="${BURROW_RUNTIME:-}"
if [[ -z "$runtime" ]]; then
  if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
    runtime=docker
  elif command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1; then
    runtime=podman
  fi
fi
if [[ -z "$runtime" ]]; then
  if [[ "$ALLOW_SKIP" == "1" ]]; then
    echo "matrix: no usable container runtime (docker/podman); skipping with recorded reason" >&2
    exit 0
  fi
  echo "matrix: no usable container runtime (docker or podman); aborting" >&2
  exit 1
fi

work="$(mktemp -d /tmp/burrow-matrix.XXXXXX)"
trap 'rm -rf "$work"' EXIT

echo "matrix: building static binary with $runtime"
CGO_ENABLED=0 go build -o "$work/burrow" ./cmd/burrow

# image -> family. pacman/zypper/apk images are skipped until those adapters land.
images=(
  "ubuntu:24.04 apt"
  "debian:12 apt"
  "fedora:41 dnf"
)

failed=0
for entry in "${images[@]}"; do
  read -r image family <<<"$entry"
  echo "matrix: running $image ($family)"
  # Run as the image's `nobody` (uid 65534): it has a passwd entry, so
  # os/user resolves, and it is unprivileged. XDG_* are pointed at /tmp by the
  # case script so the resolved dirs are writable.
  if "$runtime" run --rm \
    --user 65534:65534 \
    -e "BURROW_TESTBIN_LOG=/tmp/argv.log" \
    -v "$work/burrow:/usr/local/bin/burrow:ro,z" \
    -v "$ROOT/testbin:/opt/burrow-testbin:ro,z" \
    -v "$ROOT/scripts/matrix-case.sh:/opt/matrix-case.sh:ro,z" \
    "$image" \
    /bin/sh /opt/matrix-case.sh "$family"; then
    echo "matrix: $image ok"
  else
    echo "matrix: $image FAILED" >&2
    failed=1
  fi
done

if [[ "$failed" -ne 0 ]]; then
  echo "matrix: FAILED" >&2
  exit 1
fi
echo "matrix: all images passed"
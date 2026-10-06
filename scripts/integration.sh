#!/usr/bin/env bash
# scripts/integration.sh — optional container/host integration matrix.
#
# Requires root. Creates a loopback ext4 filesystem and runs the cross-device
# trash test against it, then unmounts and cleans up. This never touches a real
# host path: the mount point is created under a private mktemp directory.
set -euo pipefail

cd "$(dirname "$0")/.."

if [[ "${EUID:-$(id -u)}" -ne 0 ]]; then
  echo "integration: must run as root (loopback mount)" >&2
  exit 1
fi
for tool in losetup mkfs.ext4 mount umount; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "integration: missing required tool: $tool" >&2
    exit 1
  fi
done

work="$(mktemp -d /tmp/burrow-integration.XXXXXX)"
img="$work/disk.img"
mnt="$work/mnt"
loop=""

cleanup() {
  if mountpoint -q "$mnt" 2>/dev/null; then umount "$mnt" || true; fi
  if [[ -n "$loop" ]]; then losetup -d "$loop" 2>/dev/null || true; fi
  rm -rf "$work"
}
trap cleanup EXIT

dd if=/dev/zero of="$img" bs=1M count=32 status=none
mkfs.ext4 -q "$img"
mkdir -p "$mnt"
loop="$(losetup --find --show "$img")"
mount "$loop" "$mnt"

echo "integration: running cross-device trash test against $mnt"
BURROW_CROSS_DEVICE_DIR="$mnt" go test ./internal/trash -run TestCrossDevice -count=1 -v

echo "integration: ok"
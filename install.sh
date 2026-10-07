#!/usr/bin/env bash
# install.sh — checksum-verified, fail-closed burrow installer.
#
# Every artifact is verified against the release checksums.txt before anything
# is installed. A missing checksum, a download failure, or a mismatch aborts
# without touching the destination. There is no "curl | sh" shortcut here.
#
# Env:
#   BURROW_VERSION   pin a version (e.g. 0.1.0 or v0.1.0); default: latest
#   BURROW_REPO      owner/repo (default: brandtg/burrow)
#   BURROW_BINDIR    install directory (default: /usr/local/bin)
#
# Provenance: the checksum-verified, fail-closed install discipline mirrors
# Mole's installer behavior, reimplemented clean-room. No Mole source copied.
set -euo pipefail

REPO="${BURROW_REPO:-brandtg/burrow}"
VERSION="${BURROW_VERSION:-}"
BINDIR="${BURROW_BINDIR:-/usr/local/bin}"

err() { printf 'install: %s\n' "$*" >&2; exit 1; }

for tool in curl tar uname mktemp install sed head grep tr; do
  command -v "$tool" >/dev/null 2>&1 || err "missing required tool: $tool"
done
if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
  err "neither sha256sum nor shasum is available; refusing to install unverified"
fi

sha256_verify() {
  local dir="$1" file="$2"
  if command -v sha256sum >/dev/null 2>&1; then
    ( cd "$dir" && sha256sum -c -- "$file" )
  elif command -v shasum >/dev/null 2>&1; then
    ( cd "$dir" && shasum -a 256 -c -- "$file" )
  else
    err "neither sha256sum nor shasum is available; refusing to install unverified"
  fi
}

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
[[ "$os" == "linux" ]] || err "unsupported OS: $os (burrow ships linux only)"
machine="$(uname -m)"
case "$machine" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) err "unsupported architecture: $machine" ;;
esac

if [[ -z "$VERSION" ]]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"v\{0,1\}\([^"]*\)".*/\1/p' \
    | head -n1)"
  [[ -n "$VERSION" ]] || err "could not determine the latest version; set BURROW_VERSION"
fi

semver="${VERSION#v}"
tag="v${semver}"
asset="burrow_${semver}_${os}_${arch}.tar.gz"
base="${BURROW_BASE_URL:-https://github.com/$REPO/releases/download/$tag}"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

curl -fsSL -o "$tmp/$asset" "$base/$asset" || err "download failed: $asset"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || err "download failed: checksums.txt"

grep -F " $asset" "$tmp/checksums.txt" >"$tmp/selected.txt" || true
[[ -s "$tmp/selected.txt" ]] || err "no checksum entry for $asset; refusing to install"

sha256_verify "$tmp" "selected.txt" || err "checksum verification failed; refusing to install"

tar -xzf "$tmp/$asset" -C "$tmp"
[[ -f "$tmp/burrow" ]] || err "archive did not contain the burrow binary"

install -d "$BINDIR" 2>/dev/null || err "cannot create $BINDIR (try BURROW_BINDIR=~/.local/bin)"
install -m 0755 "$tmp/burrow" "$BINDIR/burrow"
printf 'installed burrow %s to %s\n' "$semver" "$BINDIR/burrow"
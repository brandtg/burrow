#!/usr/bin/env bash
# dev-tools.sh - verify (or install) the external lint/audit tools the gate uses.
#
# BURROW_REQUIRE_TOOLS=1 ./scripts/check.sh fails if any of these are missing.
# CI pins the same versions in .github/workflows/ci.yml; keep the two in sync.
#
# Usage:
#   scripts/dev-tools.sh            # report status; exit 1 if anything is missing
#   scripts/dev-tools.sh --install  # install missing tools at the pinned versions
set -euo pipefail

GOLANGCI_LINT_VERSION="v2.14.0"
GOVULNCHECK_VERSION="v1.8.0"
GORELEASER_VERSION="v2.18.2"

INSTALL=0
for arg in "$@"; do
  case "$arg" in
    --install|-i) INSTALL=1 ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) echo "unknown argument: $arg" >&2; exit 2 ;;
  esac
done

GOBIN_DIR="$(go env GOPATH)/bin"

version_of() {
  local out first
  out="$("$1" --version 2>/dev/null || true)"
  if [[ -z "$out" ]]; then out="$("$1" -version 2>/dev/null || true)"; fi
  first="$(printf '%s\n' "$out" | head -1)"
  if [[ "$first" != *[0-9]* ]]; then
    # Some tools print a banner before the version line.
    first="$(printf '%s\n' "$out" | grep -m1 '[0-9]' || printf '%s' "$first")"
  fi
  printf '%s' "$first"
}

# name|install-spec
tools=(
  "golangci-lint|github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}"
  "govulncheck|golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}"
  "goreleaser|github.com/goreleaser/goreleaser/v2@${GORELEASER_VERSION}"
)

missing=0
for entry in "${tools[@]}"; do
  IFS='|' read -r name spec <<<"$entry"
  if command -v "$name" >/dev/null 2>&1; then
    printf 'ok      %-14s %s\n' "$name" "$(version_of "$name")"
    continue
  fi
  missing=1
  if [[ "$INSTALL" == "1" ]]; then
    echo "installing $name ($spec)"
    go install "$spec"
    printf 'ok      %-14s installed -> %s\n' "$name" "$GOBIN_DIR/$name"
  else
    printf 'missing %-14s -> go install %s\n' "$name" "$spec"
  fi
done

if [[ ":$PATH:" != *":$GOBIN_DIR:"* ]]; then
  echo
  echo "note: $GOBIN_DIR is not on PATH; add it to use installed tools:"
  echo "  export PATH=\"$GOBIN_DIR:\$PATH\""
fi

if [[ "$missing" == "1" && "$INSTALL" != "1" ]]; then
  echo
  echo "install them with: scripts/dev-tools.sh --install   (or: make setup-tools)"
  exit 1
fi

echo
echo "gate tools ready; run 'make gate'"
#!/usr/bin/env bash
# scripts/check.sh — the canonical burrow gate.
#
# Runs: gofmt check, go vet, unit tests, and the static-binary check.
#
# Env:
#   BURROW_INTEGRATION=1     run the root-only integration script
#   BURROW_MATRIX=1          run the container matrix
set -euo pipefail

cd "$(dirname "$0")/.."
FAIL=0

step() { printf '\n==> %s\n' "$1"; }
ok()   { printf 'ok   %s\n' "$1"; }
warn() { printf 'warn %s\n' "$1"; }
fail() { printf 'FAIL %s\n' "$1"; FAIL=1; }

step "gofmt"
UNFORMATTED="$(gofmt -l . | grep -v '^$' || true)"
if [[ -n "$UNFORMATTED" ]]; then
  fail "gofmt: files need formatting:"
  printf '%s\n' "$UNFORMATTED"
else
  ok "gofmt"
fi

step "go vet"
if go vet ./...; then ok "go vet"; else fail "go vet"; fi

step "go test"
if go test ./...; then ok "go test"; else fail "go test"; fi

step "static binary"
STATIC_TMP="$(mktemp -d)"
if CGO_ENABLED=0 go build -o "$STATIC_TMP/burrow" ./cmd/burrow; then
  if command -v ldd >/dev/null 2>&1; then
    LDD_OUT="$(ldd "$STATIC_TMP/burrow" 2>&1 || true)"
    if grep -qE 'not a dynamic executable|statically linked' <<<"$LDD_OUT"; then
      ok "static binary"
    else
      fail "binary is dynamically linked"
      printf '%s\n' "$LDD_OUT"
    fi
  else
    warn "ldd not available; skipping static check"
  fi
else
  fail "static build failed"
fi
rm -rf "$STATIC_TMP"

if [[ "${BURROW_INTEGRATION:-0}" == "1" ]]; then
  step "integration matrix"
  if [[ -x scripts/integration.sh ]]; then
    if scripts/integration.sh; then ok "integration"; else fail "integration"; fi
  else
    warn "scripts/integration.sh not present; skipping"
  fi
fi

if [[ "${BURROW_MATRIX:-0}" == "1" ]]; then
  step "container matrix"
  if [[ -x scripts/matrix.sh ]]; then
    # --allow-skip records a reason and passes when no container runtime is
    # available; run `make matrix` directly to require it.
    if scripts/matrix.sh --allow-skip; then ok "container matrix"; else fail "container matrix"; fi
  else
    warn "scripts/matrix.sh not present; skipping"
  fi
fi

if [[ "$FAIL" -ne 0 ]]; then
  printf '\nGATE FAILED\n'
  exit 1
fi
printf '\nGATE PASSED\n'

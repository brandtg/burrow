#!/usr/bin/env bash
# scripts/check.sh — the canonical burrow gate.
#
# Runs: gofmt check, go vet, golangci-lint (if installed), govulncheck (if
# installed), unit tests, the static "no direct destructive sink" gate, and
# (optionally) the container integration matrix.
#
# Env:
#   BURROW_REQUIRE_TOOLS=1   fail if golangci-lint/govulncheck are missing
#   BURROW_INTEGRATION=1     run the container integration matrix
set -euo pipefail

cd "$(dirname "$0")/.."
ROOT="$(pwd)"
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

step "golangci-lint"
if command -v golangci-lint >/dev/null 2>&1; then
  if golangci-lint run ./...; then ok "golangci-lint"; else fail "golangci-lint"; fi
elif [[ "${BURROW_REQUIRE_TOOLS:-0}" == "1" ]]; then
  fail "golangci-lint not installed"
else
  warn "golangci-lint not installed; skipping (set BURROW_REQUIRE_TOOLS=1 to enforce)"
fi

step "govulncheck"
if command -v govulncheck >/dev/null 2>&1; then
  if govulncheck ./...; then ok "govulncheck"; else fail "govulncheck"; fi
elif [[ "${BURROW_REQUIRE_TOOLS:-0}" == "1" ]]; then
  fail "govulncheck not installed"
else
  warn "govulncheck not installed; skipping (set BURROW_REQUIRE_TOOLS=1 to enforce)"
fi

step "goreleaser config"
if command -v goreleaser >/dev/null 2>&1; then
  if goreleaser check; then ok "goreleaser config"; else fail "goreleaser config"; fi
elif [[ "${BURROW_REQUIRE_TOOLS:-0}" == "1" ]]; then
  fail "goreleaser not installed"
else
  warn "goreleaser not installed; skipping config lint (set BURROW_REQUIRE_TOOLS=1 to enforce)"
fi

step "go test"
if go test ./...; then ok "go test"; else fail "go test"; fi

step "static safety gate"
if go run ./scripts/safety-gate "$ROOT"; then ok "safety gate"; else fail "safety gate"; fi

step "man pages"
MAN_TMP="$(mktemp -d)"
if go run ./cmd/burrow docs --dir "$MAN_TMP" >/dev/null 2>&1; then
  if diff -r docs/man "$MAN_TMP" >/dev/null 2>&1; then
    ok "man pages up to date"
  else
    fail "man pages are stale; run 'make docs' and review the diff"
    diff -r docs/man "$MAN_TMP" || true
  fi
else
  fail "man page generation failed"
fi
rm -rf "$MAN_TMP"

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
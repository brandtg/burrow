#!/bin/sh
# scripts/matrix-case.sh — in-container assertions for the integration matrix.
#
# Runs inside a distro image as an unprivileged user, with the burrow binary at
# /usr/local/bin/burrow and the stubbed owner commands at /opt/burrow-testbin
# first on PATH. It never touches a real host path. Invoked by scripts/matrix.sh
# with the distro family ("apt" or "dnf") as $1.
set -eu

family="${1:?family required}"

export PATH="/opt/burrow-testbin:$PATH"
export HOME=/tmp/burrow-home
export XDG_CONFIG_HOME="$HOME/.config"
export XDG_DATA_HOME="$HOME/.local/share"
export XDG_STATE_HOME="$HOME/.local/state"
export XDG_CACHE_HOME="$HOME/.cache"
mkdir -p "$HOME" "$XDG_CONFIG_HOME/burrow" "$XDG_DATA_HOME" "$XDG_STATE_HOME" "$XDG_CACHE_HOME"

burrow=/usr/local/bin/burrow
log="${BURROW_TESTBIN_LOG:-/tmp/argv.log}"

fail() { printf 'matrix-case[%s]: FAIL: %s\n' "$family" "$*" >&2; exit 1; }
ok() { printf 'matrix-case[%s]: ok: %s\n' "$family" "$*"; }

case "$family" in
  dnf)
    CACHE_LOG="dnf clean all"
    CACHE_JSON='"dnf","clean","all"'
    ORPHANS_JSON='"dnf","autoremove","-y"'
    KERNELS_JSON='"package-cleanup","--oldkernels","--count=2"'
    CACHE_PATH="/var/cache/dnf"
    PROC="dnf"
    MUTATION_RE='dnf clean all|dnf autoremove -y|oldkernels'
    ;;
  apt)
    CACHE_LOG="apt-get clean"
    CACHE_JSON='"apt-get","clean"'
    ORPHANS_JSON='"apt-get","autoremove","--purge"'
    KERNELS_JSON='"apt-get","autoremove","--purge"'
    CACHE_PATH="/var/cache/apt/archives"
    PROC="apt-get"
    MUTATION_RE='apt-get clean|apt-get autoremove --purge|dpkg --purge'
    ;;
  *)
    fail "unknown family $family"
    ;;
esac

# The JSON pretty-prints argv as an array of tokens; strip whitespace so the
# planned argv can be matched as a contiguous sequence.
compact() { printf '%s' "$1" | tr -d ' \n\t'; }

# --- (b) dry-run plans the exact argv and mutates nothing ---
: >"$log"
out="$("$burrow" clean --dry-run --json --only system 2>/dev/null)" || fail "dry-run exited non-zero"
compact_out="$(compact "$out")"
echo "$compact_out" | grep -Fq "$CACHE_JSON" || fail "planned argv missing '$CACHE_LOG'"
if grep -Eq "$MUTATION_RE" "$log"; then
  fail "dry-run executed a mutating command: $(cat "$log")"
fi
ok "dry-run plans '$CACHE_LOG' and mutates nothing"

# dnf-only items are deterministic under the stubs; apt orphan/kernel discovery
# depends on the host's real dpkg/apt state, so only assert them for dnf.
if [ "$family" = "dnf" ]; then
  echo "$compact_out" | grep -Fq "$ORPHANS_JSON" || fail "planned argv missing dnf autoremove"
  echo "$compact_out" | grep -Fq "$KERNELS_JSON" || fail "planned argv missing package-cleanup"
  ok "dry-run plans the dnf orphans and old-kernels argv"
fi

# --- (a) execute calls exactly the planned argv ---
: >"$log"
"$burrow" clean --yes --json --only system.pkg-cache >/dev/null 2>&1 || fail "execute pkg-cache failed"
grep -Fq "$CACHE_LOG" "$log" || fail "execute did not call '$CACHE_LOG': $(cat "$log")"
ok "execute ran '$CACHE_LOG'"

# --- (d) whitelist excludes the package cache ---
printf '%s\n' "$CACHE_PATH" >"$XDG_CONFIG_HOME/burrow/whitelist"
out="$("$burrow" clean --dry-run --json --only system 2>/dev/null)" || fail "dry-run with whitelist failed"
if compact "$out" | grep -Fq "$CACHE_JSON"; then
  fail "whitelist did not exclude '$CACHE_LOG'"
fi
: >"$XDG_CONFIG_HOME/burrow/whitelist"
ok "whitelist excluded '$CACHE_LOG'"

# --- (c) the guard trips on a simulated running owner ---
cp /bin/sleep "/tmp/$PROC"
"/tmp/$PROC" 30 &
owner_pid=$!
sleep 1
set +e
"$burrow" clean --yes --json --only system.pkg-cache >/dev/null 2>&1
guard_code=$?
set -e
kill "$owner_pid" 2>/dev/null || true
wait "$owner_pid" 2>/dev/null || true
[ "$guard_code" -eq 75 ] || fail "expected exit 75 on a running $PROC, got $guard_code"
ok "guard tripped (exit 75) on a running $PROC"

# --- (e) exit codes ---
set +e
"$burrow" clean --only system.pkg-cache >/dev/null 2>&1
usage_code=$?
set -e
[ "$usage_code" -eq 2 ] || fail "expected exit 2 without --yes, got $usage_code"
ok "exit code 2 for an unconfirmed non-interactive clean"

printf 'matrix-case[%s]: all assertions passed\n' "$family"
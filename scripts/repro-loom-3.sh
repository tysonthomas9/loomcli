#!/usr/bin/env bash
# scripts/repro-loom-3.sh — LOOM-3 reproduction.
#
# Verifies that `loom workspace ops diagnose --json` does NOT report
# `daemon_not_running` when a `loom daemon` process is alive but was
# launched from a cwd whose `.loom/` runtime dir is not the desktop
# data dir.
#
# Pre-fix: exits 1 (bug reproduced).
# Post-fix: exits 0 (registered-daemon detection catches it).
#
# Environment:
#   LOOM_BIN  — path to the loom binary (defaults to ./loom relative to repo root)

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOOM_BIN="${LOOM_BIN:-$ROOT/loom}"
WS_KEY="${WS_KEY:-LOOM3REPRO}"

if [[ ! -x "$LOOM_BIN" ]]; then
  echo "loom binary not found/executable at $LOOM_BIN" >&2
  echo "  (set LOOM_BIN or build: go build -o ./loom ./cmd/loom)" >&2
  exit 2
fi
for tool in jq mktemp; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool missing: $tool" >&2
    exit 2
  fi
done

tmp="$(mktemp -d)"
daemon_cwd="$tmp/daemon-cwd"
diagnose_cwd="$tmp/diagnose-cwd"
mkdir -p "$daemon_cwd" "$diagnose_cwd"
daemon_log="$tmp/daemon.log"

dump_daemon_log() {
  if [[ -s "$daemon_log" ]]; then
    echo "--- daemon log ---" >&2
    cat "$daemon_log" >&2
    echo "--- end daemon log ---" >&2
  fi
}

cleanup() {
  if [[ "${dump_log_on_exit:-0}" == "1" ]]; then
    dump_daemon_log
  fi
  if [[ -n "${daemon_pid:-}" ]] && kill -0 "$daemon_pid" 2>/dev/null; then
    kill "$daemon_pid" >/dev/null 2>&1 || true
    wait "$daemon_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

echo "==> creating workspace $WS_KEY (idempotent)"
if ! LOOM_WORKSPACE="$WS_KEY" "$LOOM_BIN" workspace add "$WS_KEY" >"$tmp/add.log" 2>&1; then
  # Likely "workspace already exists" — proceed but show the message.
  cat "$tmp/add.log" >&2
fi

echo "==> spawning daemon from $daemon_cwd"
(
  cd "$daemon_cwd"
  LOOM_WORKSPACE="$WS_KEY" "$LOOM_BIN" daemon >"$daemon_log" 2>&1 &
  echo $!
) >"$tmp/daemon.pid"
daemon_pid="$(cat "$tmp/daemon.pid")"

echo "==> waiting for daemon (pid=$daemon_pid) to register Node"
deadline=$((SECONDS + 30))
while (( SECONDS < deadline )); do
  if ! kill -0 "$daemon_pid" 2>/dev/null; then
    echo "daemon exited prematurely; log follows:" >&2
    cat "$daemon_log" >&2
    exit 2
  fi
  # The daemon publishes its Node within the first heartbeat cycle.
  if (
    cd "$diagnose_cwd"
    LOOM_WORKSPACE="$WS_KEY" "$LOOM_BIN" workspace ops status --json 2>/dev/null \
      | jq -e '.daemon.registered.running == true' >/dev/null 2>&1
  ); then
    break
  fi
  sleep 0.5
done

echo "==> running diagnose from $diagnose_cwd"
diag_json="$(cd "$diagnose_cwd" && LOOM_WORKSPACE="$WS_KEY" "$LOOM_BIN" workspace ops diagnose --json 2>/dev/null)"
echo "$diag_json" | jq . >/dev/null  # sanity-check JSON

if echo "$diag_json" | jq -e '.problems[]? | select(.code=="daemon_not_running")' >/dev/null; then
  echo "FAIL: diagnose reported daemon_not_running (LOOM-3 bug present)" >&2
  echo "$diag_json" | jq '.problems' >&2
  dump_log_on_exit=1
  exit 1
fi

echo "PASS: diagnose did not report daemon_not_running"
exit 0

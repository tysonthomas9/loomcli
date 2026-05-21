#!/usr/bin/env bash
# scripts/repro-loom-7-split-brain.sh — LOOM-7 reproduction.
#
# Verifies that two `loom serve` processes started with different
# LOOM_CONFIG_DIRs do NOT each spawn their own embedded fleet-db, which
# would cause split-brain (CLI hits one, desktop hits the other).
#
# Pre-fix: two serves -> two runtime.json files -> exits 1.
# Post-fix: second serve joins the first via the host-wide registry
# at $LOOM_FLEET_DB_REGISTRY, and only the first data dir has a
# runtime.json. Exits 0.
#
# Environment:
#   LOOM_BIN       — path to loom binary (default: ./loom relative to repo root)
#   FLEET_DB_BIN   — path to fleet-db binary (must be set)
#   LOOM_RUN_REPRO — must be set to "1" to run; otherwise the script
#                    skips (so it stays out of plain `make test`).

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOOM_BIN="${LOOM_BIN:-$ROOT/loom}"

if [[ "${LOOM_RUN_REPRO:-0}" != "1" ]]; then
  echo "skipping (set LOOM_RUN_REPRO=1 to run)"
  exit 0
fi

if [[ ! -x "$LOOM_BIN" ]]; then
  echo "loom binary not found/executable at $LOOM_BIN" >&2
  echo "  (set LOOM_BIN or build: go build -o ./loom ./cmd/loom)" >&2
  exit 2
fi
if [[ -z "${FLEET_DB_BIN:-}" || ! -x "${FLEET_DB_BIN}" ]]; then
  echo "FLEET_DB_BIN not set or not executable" >&2
  exit 2
fi
for tool in jq mktemp curl; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "required tool missing: $tool" >&2
    exit 2
  fi
done

tmp="$(mktemp -d)"
DIR_A="$tmp/data-a"
DIR_B="$tmp/data-b"
REG_DIR="$tmp/registry"
REG_FILE="$REG_DIR/active.json"
mkdir -p "$DIR_A" "$DIR_B" "$REG_DIR"

LOG_A="$tmp/serve-a.log"
LOG_B="$tmp/serve-b.log"

pid_a=""
pid_b=""

cleanup() {
  if [[ -n "$pid_a" ]] && kill -0 "$pid_a" 2>/dev/null; then
    kill "$pid_a" >/dev/null 2>&1 || true
    wait "$pid_a" >/dev/null 2>&1 || true
  fi
  if [[ -n "$pid_b" ]] && kill -0 "$pid_b" 2>/dev/null; then
    kill "$pid_b" >/dev/null 2>&1 || true
    wait "$pid_b" >/dev/null 2>&1 || true
  fi
  if [[ "${dump_logs:-0}" == "1" ]]; then
    echo "--- serve A log ---" >&2; cat "$LOG_A" >&2 || true
    echo "--- serve B log ---" >&2; cat "$LOG_B" >&2 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

pick_free_port() {
  python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'
}

PORT_A="$(pick_free_port)"
PORT_B="$(pick_free_port)"

echo "==> starting serve A: data=$DIR_A port=$PORT_A registry=$REG_FILE"
(
  LOOM_CONFIG_DIR="$DIR_A" \
    LOOM_FLEET_DB_REGISTRY="$REG_FILE" \
    FLEET_DB_BIN="$FLEET_DB_BIN" \
    "$LOOM_BIN" serve --port "$PORT_A" >"$LOG_A" 2>&1 &
  echo $!
) >"$tmp/pid-a"
pid_a="$(cat "$tmp/pid-a")"

# Wait for A's fleet-db runtime.json to exist.
deadline=$((SECONDS + 30))
while (( SECONDS < deadline )); do
  if [[ -s "$DIR_A/fleet-db/runtime.json" ]] && [[ -s "$REG_FILE" ]]; then
    break
  fi
  if ! kill -0 "$pid_a" 2>/dev/null; then
    echo "serve A exited early" >&2
    dump_logs=1
    exit 2
  fi
  sleep 0.3
done

if [[ ! -s "$DIR_A/fleet-db/runtime.json" ]]; then
  echo "FAIL: serve A never wrote runtime.json" >&2
  dump_logs=1
  exit 2
fi
if [[ ! -s "$REG_FILE" ]]; then
  echo "FAIL: serve A never wrote registry entry $REG_FILE" >&2
  dump_logs=1
  exit 1
fi

REG_PID_A="$(jq -r .pid "$REG_FILE")"
REG_URL_A="$(jq -r .url "$REG_FILE")"
echo "    registry entry: pid=$REG_PID_A url=$REG_URL_A"

echo "==> starting serve B: data=$DIR_B port=$PORT_B"
(
  LOOM_CONFIG_DIR="$DIR_B" \
    LOOM_FLEET_DB_REGISTRY="$REG_FILE" \
    FLEET_DB_BIN="$FLEET_DB_BIN" \
    "$LOOM_BIN" serve --port "$PORT_B" >"$LOG_B" 2>&1 &
  echo $!
) >"$tmp/pid-b"
pid_b="$(cat "$tmp/pid-b")"

# Give B a few seconds to either join or spawn its own.
sleep 5

if ! kill -0 "$pid_b" 2>/dev/null; then
  echo "FAIL: serve B exited unexpectedly" >&2
  dump_logs=1
  exit 1
fi

# Pre-fix expectation: B also wrote its own fleet-db/runtime.json under DIR_B.
# Post-fix expectation: B joined A via the registry, so DIR_B/fleet-db/runtime.json does NOT exist.
if [[ -s "$DIR_B/fleet-db/runtime.json" ]]; then
  echo "FAIL: serve B spawned its own embedded fleet-db (LOOM-7 split-brain reproduced)" >&2
  echo "  DIR_B runtime.json:" >&2
  cat "$DIR_B/fleet-db/runtime.json" >&2
  dump_logs=1
  exit 1
fi

# The registry entry should still belong to A.
REG_PID_AFTER="$(jq -r .pid "$REG_FILE")"
REG_URL_AFTER="$(jq -r .url "$REG_FILE")"
if [[ "$REG_PID_AFTER" != "$REG_PID_A" ]] || [[ "$REG_URL_AFTER" != "$REG_URL_A" ]]; then
  echo "FAIL: registry entry was overwritten by serve B" >&2
  echo "  before: pid=$REG_PID_A url=$REG_URL_A" >&2
  echo "  after:  pid=$REG_PID_AFTER url=$REG_URL_AFTER" >&2
  dump_logs=1
  exit 1
fi

# Sanity: A's fleet-db is healthy.
if ! curl -sf "$REG_URL_A/healthz" >/dev/null; then
  echo "FAIL: serve A's fleet-db /healthz failed after B started" >&2
  dump_logs=1
  exit 1
fi

echo "PASS: serve B joined serve A's fleet-db; no split-brain"
exit 0

#!/usr/bin/env bash
# Repro for LOOM-6: desktop runtime /health returns 200 OK even when no
# workspace daemon is connected.
#
# Pre-fix:  /api/workspaces/{ws}/runtime-ready returns 404 (route missing). Exit 1.
# Post-fix: same route returns 503 with body {"ready":false,...,"reason":"..."}
#           pointing at the real cause (e.g. "workspace not registered"). Exit 0.
#
# Both pre- and post-fix: /api/health returns 200 unchanged (this is the LB
# probe and must not move). The bug is that nothing distinguishes "HTTP up but
# workspace not wired" until LOOM-6's workspace-scoped readiness endpoint.
#
# Run from repo root. Uses curl + jq (falls back to grep if jq absent).

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

if [[ -z "${LOOM_BIN:-}" ]]; then
  echo "[repro] building loom binary into ./bin/loom"
  mkdir -p ./bin
  go build -o ./bin/loom ./cmd/loom
  LOOM_BIN="$(pwd)/bin/loom"
fi

tmp="$(mktemp -d)"
runtime_log="$tmp/runtime.log"

cleanup() {
  if [[ -f "$tmp/runtime.json" ]]; then
    "$LOOM_BIN" local --data-dir "$tmp" stop >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

echo "[repro] starting loom local runtime in $tmp"
nohup "$LOOM_BIN" local --data-dir "$tmp" start >"$runtime_log" 2>&1 &

# Wait for runtime.json to appear and report status=running.
deadline=$(( $(date +%s) + 20 ))
while [[ $(date +%s) -lt $deadline ]]; do
  if [[ -f "$tmp/runtime.json" ]]; then
    if grep -q '"status": "running"' "$tmp/runtime.json" 2>/dev/null; then
      break
    fi
  fi
  sleep 0.5
done

if [[ ! -f "$tmp/runtime.json" ]]; then
  echo "[repro] runtime.json never appeared; see $runtime_log" >&2
  exit 1
fi

read_url() {
  if command -v jq >/dev/null 2>&1; then
    jq -r '.url // empty' "$tmp/runtime.json"
  else
    sed -n 's/.*"url": *"\([^"]*\)".*/\1/p' "$tmp/runtime.json" | head -n1
  fi
}

URL="$(read_url)"
if [[ -z "$URL" ]]; then
  echo "[repro] runtime.json has no URL field" >&2
  cat "$tmp/runtime.json" >&2
  exit 1
fi

# Pre-condition: /api/health still 200 (load-balancer probe semantics
# preserved — LOOM-6 is intentionally additive).
http_health="$(curl -s -o /dev/null -w '%{http_code}' "$URL/api/health")"
if [[ "$http_health" != "200" ]]; then
  echo "[repro] FAIL: /api/health returned $http_health, expected 200" >&2
  exit 1
fi
echo "[repro] /api/health = 200 (LB probe semantics intact)"

# The repro probe: /api/workspaces/NOPE/runtime-ready.
# Pre-fix: route does not exist → 404 → exit 1.
# Post-fix: 503 with diagnostic JSON body → exit 0.
#
# Capture body to a file (avoids `head -n -1`, which is GNU-only and silently
# breaks on macOS where this script is most likely to run).
ready_body_file="$tmp/ready.body"
ready_status="$(curl -s -o "$ready_body_file" -w '%{http_code}' "$URL/api/workspaces/NOPE/runtime-ready")"
ready_payload="$(cat "$ready_body_file")"

case "$ready_status" in
  404)
    echo "[repro] expected /runtime-ready route — got 404 (route missing); pre-fix behavior"
    exit 1
    ;;
  503)
    if echo "$ready_payload" | grep -q '"ready":false' && echo "$ready_payload" | grep -q '"reason"'; then
      echo "[repro] OK — /runtime-ready route returns 503 with diagnostic reason:"
      echo "$ready_payload"
      exit 0
    fi
    echo "[repro] FAIL: 503 but body missing ready=false or reason: $ready_payload" >&2
    exit 1
    ;;
  *)
    echo "[repro] FAIL: unexpected status $ready_status: $ready_payload" >&2
    exit 1
    ;;
esac

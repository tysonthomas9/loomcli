#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOOM_BIN="${LOOM_BIN:-$ROOT/loom}"
FLEET_DB_BIN="${FLEET_DB_BIN:-/tmp/fleet-db}"

if [[ ! -x "$LOOM_BIN" ]]; then
  echo "loom binary not found/executable at $LOOM_BIN (set LOOM_BIN or run: go build -o ./loom ./cmd/loom)" >&2
  exit 2
fi
if [[ ! -x "$FLEET_DB_BIN" ]]; then
  echo "fleet-db binary not found/executable at $FLEET_DB_BIN (set FLEET_DB_BIN)" >&2
  exit 2
fi

tmp="$(mktemp -d)"
cleanup() {
  if [[ -n "${serve_pid:-}" ]]; then
    kill "$serve_pid" >/dev/null 2>&1 || true
    wait "$serve_pid" >/dev/null 2>&1 || true
  fi
  rm -rf "$tmp"
}
trap cleanup EXIT

dump_serve_log() {
  if [[ -f "$tmp/serve.log" ]]; then
    echo "--- loom serve log ---" >&2
    cat "$tmp/serve.log" >&2
    echo "--- end loom serve log ---" >&2
  fi
}

safe_bin="$tmp/bin"
mkdir -p "$safe_bin"
ln -s "$LOOM_BIN" "$safe_bin/loom"
ln -s "$FLEET_DB_BIN" "$safe_bin/fleet-db"
for tool in git curl python3 sed grep sleep rm find seq mktemp dirname cat tmux; do
  if command -v "$tool" >/dev/null 2>&1; then
    ln -s "$(command -v "$tool")" "$safe_bin/$tool"
  fi
done

checkout="$tmp/checkout"
loom_dir="$tmp/loom-home"
mkdir -p "$checkout" "$loom_dir"
cd "$checkout"
git init -q

export PATH="$safe_bin"
export FLEET_DB_BIN="$safe_bin/fleet-db"
export LOOM_CONFIG_DIR="$loom_dir"
export LOOM_ISSUE_BACKEND=fleetdb
export LOOM_WORKSPACE=CLEAN
export LOOM_LOG_FORMAT=text

if command -v bd >/dev/null 2>&1; then
  echo "bd unexpectedly present in stripped PATH" >&2
  exit 1
fi
if [[ -e .beads ]]; then
  echo ".beads unexpectedly exists before smoke" >&2
  exit 1
fi

loom workspace add CLEAN --description "clean checkout smoke" >/tmp/loom-clean-workspace.out

port="$((18080 + RANDOM % 1000))"
loom serve --port "$port" --bind 127.0.0.1 >"$tmp/serve.log" 2>&1 &
serve_pid=$!
base="http://127.0.0.1:$port"

for _ in $(seq 1 80); do
  if curl -fsS "$base/health" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "$serve_pid" >/dev/null 2>&1; then
    echo "loom serve exited early" >&2
    cat "$tmp/serve.log" >&2
    exit 1
  fi
  sleep 0.25
done
curl -fsS "$base/health" >/dev/null

sse_out="$tmp/sse.out"
curl -fsSN --max-time 3 "$base/api/workspaces/CLEAN/events" >"$sse_out" 2>/dev/null || true
grep -q "event: connected" "$sse_out"

create_resp="$tmp/create-response.json"
create_status="$(curl -sS -o "$create_resp" -w '%{http_code}' -X POST "$base/api/workspaces/CLEAN/issues" \
  -H 'content-type: application/json' \
  --data '{"title":"Clean checkout smoke","issue_type":"task","priority":2,"description":"fleet-db only"}')"
if [[ "$create_status" != "200" && "$create_status" != "201" ]]; then
  echo "issue create failed with HTTP $create_status" >&2
  cat "$create_resp" >&2
  echo >&2
  dump_serve_log
  exit 1
fi
create_json="$(cat "$create_resp")"
issue_id="$(python3 - <<'PY' "$create_json"
import json, sys
doc = json.loads(sys.argv[1])
data = doc.get("data")
if isinstance(data, str):
    data = json.loads(data)
print(data.get("id") or data.get("ID") or "")
PY
)"
if [[ -z "$issue_id" ]]; then
  echo "could not parse issue id from create response: $create_json" >&2
  exit 1
fi

curl -fsS "$base/api/workspaces/CLEAN/issues" | grep -q "$issue_id"
curl -fsS "$base/api/workspaces/CLEAN/issues/$issue_id" | grep -q "Clean checkout smoke"

if [[ -e .beads ]] || find "$checkout" -name .beads -print -quit | grep -q .; then
  echo ".beads artifact was created in clean checkout" >&2
  exit 1
fi

kill "$serve_pid"
wait "$serve_pid" >/dev/null 2>&1 || true
serve_pid=""

echo "fleet-db clean checkout smoke passed"

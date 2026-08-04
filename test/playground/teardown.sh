#!/usr/bin/env bash
# Reliable playground teardown.
#
# Usage: teardown.sh [<scenario>]
#
# No arg     — happy-path playground workspace (PLAYGROUND).
# <scenario> — playground-<scenario> workspace created by setup.sh <scenario>.
#
# Stops the local daemon, removes the workspace via the CLI, kills any
# leftover scenario grandchildren, then surgically purges orphan fleet-db
# keys under fleet-db:<WORKSPACE>:* (which `loom workspace remove` leaves
# behind), then removes the local .runtime[-<scenario>]/ and
# .loom[-<scenario>]/ dirs.
#
# Safe to run any number of times. Targets only the matching workspace key;
# other fleet-db workspaces are untouched.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SCENARIO="${1:-}"

if [ -n "$SCENARIO" ]; then
  SUFFIX="-$SCENARIO"
  WORKSPACE_NAME="playground-$SCENARIO"
  WORKSPACE_KEY="$(printf 'PLAYGROUND-%s' "$SCENARIO" | tr '[:lower:]' '[:upper:]')"
else
  SUFFIX=""
  WORKSPACE_NAME="playground"
  WORKSPACE_KEY="PLAYGROUND"
fi

# Env override for older callers that drove teardown via env vars.
WORKSPACE_KEY="${LOOM_PLAYGROUND_WORKSPACE:-$WORKSPACE_KEY}"

RUNTIME="$HERE/.runtime$SUFFIX"
LOCAL_LOOM="$HERE/.loom$SUFFIX"

# 1. Best-effort kill of any leftover backend descendant from a botched
#    scenario run. Convention for scenarios that spawn setsid descendants:
#    write the descendant PID to
#      <loom-dir>/workspaces/<workspace>/<scenario>/grandchild.pid
#    The no-arg happy-path workspace has no marker; the file check
#    short-circuits.
loom_dir="${LOOM_CONFIG_DIR:-$HOME/.loom}"
if [ -n "$SCENARIO" ]; then
  pid_file="$loom_dir/workspaces/$WORKSPACE_NAME/$SCENARIO/grandchild.pid"
  if [ -f "$pid_file" ]; then
    pid="$(cat "$pid_file" 2>/dev/null || true)"
    if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      sleep 0.5
      kill -9 "$pid" 2>/dev/null || true
    fi
  fi
fi

# 2. Stop any locally-attached daemon (best-effort).
if [ -f "$LOCAL_LOOM/daemon.pid" ]; then
  pid="$(cat "$LOCAL_LOOM/daemon.pid" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.3
    done
    kill -9 "$pid" 2>/dev/null || true
  fi
fi

# 3. CLI-level remove (clean path; idempotent).
loom workspace remove "$WORKSPACE_KEY" --force >/dev/null 2>&1 || true

# Remove the materialized workspace directory even when fleet-db no longer has
# a registry entry for it. `workspace create` refuses to reuse a non-empty path.
rm -rf "$loom_dir/workspaces/$WORKSPACE_NAME"

# 4. Surgical fleet-db purge — required because `loom workspace remove`
#    only deletes the workspace registry entry, not the operational data
#    keys under fleet-db:<WORKSPACE>:* (issues, agents, roles, events,
#    indexes, full-text search). Those orphans block a subsequent
#    `loom workspace create` with HTTP 409 on role re-seeding.
if [ -f "$loom_dir/fleet-db/runtime.json" ]; then
  python3 - "$loom_dir/fleet-db/runtime.json" "$WORKSPACE_KEY" "$SUFFIX" <<'PY' || true
import json, socket, sys

runtime_path, workspace, suffix = sys.argv[1], sys.argv[2], sys.argv[3]
label = "teardown" + suffix
with open(runtime_path) as f:
    runtime = json.load(f)
host, port = runtime["redis_addr"].rsplit(":", 1)
port = int(port)
pattern = f"fleet-db:{workspace}:*".encode()


def resp(parts):
    out = [b"*" + str(len(parts)).encode()]
    for p in parts:
        b = p if isinstance(p, bytes) else p.encode()
        out += [b"$" + str(len(b)).encode(), b]
    return b"\r\n".join(out) + b"\r\n"


def recv(sock):
    sock.settimeout(2.0)
    buf = b""
    while True:
        try:
            chunk = sock.recv(65536)
        except socket.timeout:
            break
        if not chunk:
            break
        buf += chunk
        if len(chunk) < 65536:
            break
    return buf


try:
    sock = socket.create_connection((host, port), timeout=2.0)
except OSError as e:
    print(f"[{label}] fleet-db Redis not reachable at {host}:{port} ({e}); skipping purge", file=sys.stderr)
    sys.exit(0)

keys = []
cursor = b"0"
while True:
    sock.sendall(resp([b"SCAN", cursor, b"MATCH", pattern, b"COUNT", b"1000"]))
    raw = recv(sock)
    new_cursor = None
    for i, line in enumerate(raw.split(b"\r\n")):
        if line.startswith(b"fleet-db:"):
            keys.append(line)
        elif line.isdigit() and new_cursor is None and i > 0:
            new_cursor = line
    if new_cursor is None or new_cursor == b"0":
        break
    cursor = new_cursor

if keys:
    for i in range(0, len(keys), 50):
        sock.sendall(resp([b"DEL"] + keys[i : i + 50]))
        recv(sock)
    print(f"[{label}] purged {len(keys)} orphan fleet-db keys under fleet-db:{workspace}:*", file=sys.stderr)
sock.close()
PY
fi

# 5. Local artifacts.
rm -rf "$RUNTIME" "$LOCAL_LOOM"

echo "Playground$SUFFIX torn down (workspace=$WORKSPACE_KEY)."

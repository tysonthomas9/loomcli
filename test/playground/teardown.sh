#!/usr/bin/env bash
# Reliable playground teardown: stops the local daemon, removes the
# workspace via the CLI, then surgically purges any orphan fleet-db keys
# under fleet-db:<WORKSPACE>:* (which `loom workspace remove` leaves
# behind), then removes the local .runtime/ and .loom/ dirs.
#
# Safe to run any number of times. Targets only PLAYGROUND keys; other
# fleet-db workspaces are untouched.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE="${LOOM_PLAYGROUND_WORKSPACE:-PLAYGROUND}"

# 1. Stop the local agent supervisor if one is still attached to this dir.
if [ -f "$HERE/.loom/daemon.pid" ]; then
  pid="$(cat "$HERE/.loom/daemon.pid" 2>/dev/null || true)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.3
    done
    kill -9 "$pid" 2>/dev/null || true
  fi
fi

# 2. CLI-level remove (clean path; idempotent).
loom workspace remove "$WORKSPACE" --force >/dev/null 2>&1 || true

# 3. Surgical fleet-db purge — required because `loom workspace remove`
#    only deletes the workspace registry entry, not the operational data
#    keys under fleet-db:<WORKSPACE>:* (issues, agents, roles, events,
#    indexes, full-text search). Those orphans block a subsequent
#    `loom workspace create` with HTTP 409 on role re-seeding.
if [ -f "$HOME/.loom/fleet-db/runtime.json" ]; then
  python3 - "$HOME/.loom/fleet-db/runtime.json" "$WORKSPACE" <<'PY' || true
import json, socket, sys

runtime_path, workspace = sys.argv[1], sys.argv[2]
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
    print(f"[teardown] fleet-db Redis not reachable at {host}:{port} ({e}); skipping purge", file=sys.stderr)
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
    print(f"[teardown] purged {len(keys)} orphan fleet-db keys under fleet-db:{workspace}:*", file=sys.stderr)
sock.close()
PY
fi

# 4. Local artifacts.
rm -rf "$HERE/.runtime" "$HERE/.loom"

echo "Playground torn down (workspace=$WORKSPACE)."

#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WEB_DIR="${ROOT_DIR}/examples/beads-web-ui"
RUN_DIR="${TMPDIR:-/tmp}/beads-web-ui-run"
WEB_BINARY="${RUN_DIR}/beads-web-ui"

PORT=8080
LOOM_PORT=9000
DETACH=0
STOP=0
NO_BUILD=0
RESTART=0
NO_DAEMON=0
SOCKET_PATH=""
RESTART_DAEMON_ON_FAIL=1
LOOM_USE_DAEMON=0
DAEMON_PROBE_ATTEMPTS=5
DAEMON_PROBE_SLEEP=0.2

usage() {
  cat <<'EOF'
Run the beads web UI with a local loom server.

Usage:
  ./scripts/run-web-ui-with-loom.sh [options]

Options:
  --port <port>         Web UI port (default: 8080)
  --loom-port <port>    Loom server port (default: 9000)
  --socket <path>       Explicit beads daemon socket path
  --no-daemon           Skip starting/validating beads daemon
  --no-restart-daemon   Do not restart daemon if it is unresponsive
  --loom-use-daemon     Allow loom to use beads daemon (default: run loom with BEADS_NO_DAEMON=1)
  --detach              Run in background and exit
  --stop                Stop processes started by this script
  --restart             Stop then start
  --no-build            Do not build frontend if dist is missing
  -h, --help            Show this help
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --port)
      PORT="${2:-}"
      shift 2
      ;;
    --loom-port)
      LOOM_PORT="${2:-}"
      shift 2
      ;;
    --detach)
      DETACH=1
      shift
      ;;
    --socket)
      SOCKET_PATH="${2:-}"
      shift 2
      ;;
    --no-daemon)
      NO_DAEMON=1
      shift
      ;;
    --no-restart-daemon)
      RESTART_DAEMON_ON_FAIL=0
      shift
      ;;
    --loom-use-daemon)
      LOOM_USE_DAEMON=1
      shift
      ;;
    --stop)
      STOP=1
      shift
      ;;
    --restart)
      RESTART=1
      shift
      ;;
    --no-build)
      NO_BUILD=1
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1"
      usage
      exit 1
      ;;
  esac
done

LOOM_PID_FILE="${RUN_DIR}/loom.pid"
WEB_PID_FILE="${RUN_DIR}/web-ui.pid"
DAEMON_PID_FILE="${RUN_DIR}/bd-daemon.pid"

stop_pid() {
  local pid_file="$1"
  local name="$2"
  if [[ -f "$pid_file" ]]; then
    local pid
    pid="$(cat "$pid_file" 2>/dev/null || true)"
    if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then
      echo "Stopping ${name} (pid ${pid})"
      kill "$pid" 2>/dev/null || true
      for ((i=0; i<30; i++)); do
        if ! kill -0 "$pid" 2>/dev/null; then
          break
        fi
        sleep 0.1
      done
      if kill -0 "$pid" 2>/dev/null; then
        kill -9 "$pid" 2>/dev/null || true
      fi
    fi
    rm -f "$pid_file"
  fi
}

stop_all() {
  stop_pid "$WEB_PID_FILE" "web UI"
  stop_pid "$LOOM_PID_FILE" "loom"
  if [[ -f "$DAEMON_PID_FILE" ]]; then
    echo "Stopping beads daemon"
    run_with_timeout 2 bd daemons stop "$ROOT_DIR" >/dev/null 2>&1 || true
    rm -f "$DAEMON_PID_FILE"
  fi
}

if [[ "$STOP" -eq 1 ]]; then
  stop_all
  exit 0
fi

mkdir -p "$RUN_DIR"

if [[ "$RESTART" -eq 1 ]]; then
  stop_all
fi

if [[ -f "$LOOM_PID_FILE" ]] && kill -0 "$(cat "$LOOM_PID_FILE" 2>/dev/null || true)" 2>/dev/null; then
  echo "Loom already running (pid $(cat "$LOOM_PID_FILE")). Use --restart or --stop."
  exit 1
fi

if [[ -f "$WEB_PID_FILE" ]] && kill -0 "$(cat "$WEB_PID_FILE" 2>/dev/null || true)" 2>/dev/null; then
  echo "Web UI already running (pid $(cat "$WEB_PID_FILE")). Use --restart or --stop."
  exit 1
fi

if [[ ! -d "$WEB_DIR" ]]; then
  echo "Web UI directory not found: $WEB_DIR"
  exit 1
fi

if ! command -v loom >/dev/null 2>&1; then
  echo "loom is not installed or not on PATH."
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "go is not installed or not on PATH."
  exit 1
fi

start_bg() {
  local pid_file="$1"
  local log_file="$2"
  shift 2

  : > "$log_file"
  if [[ "$DETACH" -eq 1 ]]; then
    if command -v setsid >/dev/null 2>&1; then
      nohup setsid "$@" > "$log_file" 2>&1 < /dev/null &
    else
      nohup "$@" > "$log_file" 2>&1 < /dev/null &
    fi
  else
    "$@" > "$log_file" 2>&1 &
  fi
  echo $! > "$pid_file"
}

run_with_timeout() {
  local timeout="$1"
  shift
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$timeout" "$@" <<'PY'
import subprocess
import sys

timeout = float(sys.argv[1])
cmd = sys.argv[2:]
try:
    proc = subprocess.run(cmd, timeout=timeout)
    sys.exit(proc.returncode)
except Exception:
    sys.exit(124)
PY
  else
    "$@"
  fi
}

probe_daemon() {
  local socket_path="${SOCKET_PATH:-${ROOT_DIR}/.beads/bd.sock}"

  if ! command -v python3 >/dev/null 2>&1; then
    if [[ -S "$socket_path" ]]; then
      echo "true|${socket_path}"
    else
      echo "false|${socket_path}"
    fi
    return
  fi

  python3 - "$socket_path" <<'PY'
import os
import socket
import sys
import json

path = sys.argv[1]
if not path or not os.path.exists(path):
    print(f"false|{path}")
    sys.exit(0)

s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
s.settimeout(0.3)
try:
    s.connect(path)
    req = json.dumps({"operation": "health", "args": None}).encode() + b"\n"
    s.sendall(req)
    data = b""
    while not data.endswith(b"\n"):
        chunk = s.recv(4096)
        if not chunk:
            break
        data += chunk
    if not data:
        print(f"false|{path}")
    else:
        line = data.decode(errors="ignore").strip().splitlines()[0]
        resp = json.loads(line)
        if resp.get("success") is True:
            print(f"true|{path}")
        else:
            print(f"false|{path}")
except Exception:
    print(f"false|{path}")
finally:
    try:
        s.close()
    except Exception:
        pass
PY
}

if [[ "$NO_DAEMON" -eq 0 ]]; then
  if ! command -v bd >/dev/null 2>&1; then
    echo "bd is not installed or not on PATH."
    exit 1
  fi

  if ! (cd "$ROOT_DIR" && run_with_timeout 2 bd daemon status >/dev/null 2>&1); then
    echo "Starting beads daemon..."
    (cd "$ROOT_DIR" && bd daemon start --foreground) >/dev/null 2>&1 &
    echo $! > "$DAEMON_PID_FILE"
  fi

  if [[ -z "$SOCKET_PATH" ]]; then
    for ((i=0; i<DAEMON_PROBE_ATTEMPTS; i++)); do
      probe_result="$(probe_daemon)"
      daemon_connected="${probe_result%%|*}"
      socket_path="${probe_result#*|}"
      if [[ "$daemon_connected" == "true" ]]; then
        SOCKET_PATH="$socket_path"
        break
      fi
      sleep "$DAEMON_PROBE_SLEEP"
    done
  fi

  if [[ -z "$SOCKET_PATH" && "$RESTART_DAEMON_ON_FAIL" -eq 1 ]]; then
    echo "Daemon not responding; restarting..."
    run_with_timeout 2 bd daemons stop "$ROOT_DIR" >/dev/null 2>&1 || true
    (cd "$ROOT_DIR" && bd daemon start --foreground) >/dev/null 2>&1 &
    echo $! > "$DAEMON_PID_FILE"
    for ((i=0; i<DAEMON_PROBE_ATTEMPTS; i++)); do
      probe_result="$(probe_daemon)"
      daemon_connected="${probe_result%%|*}"
      socket_path="${probe_result#*|}"
      if [[ "$daemon_connected" == "true" ]]; then
        SOCKET_PATH="$socket_path"
        break
      fi
      sleep "$DAEMON_PROBE_SLEEP"
    done
  fi

  if [[ -z "$SOCKET_PATH" ]]; then
    if [[ -S "${ROOT_DIR}/.beads/bd.sock" ]]; then
      SOCKET_PATH="${ROOT_DIR}/.beads/bd.sock"
    fi
  fi

  if [[ -n "$SOCKET_PATH" ]] && [[ ! -S "$SOCKET_PATH" ]]; then
    echo "Warning: socket path does not exist: $SOCKET_PATH"
  fi
fi

if [[ ! -f "${WEB_DIR}/frontend/dist/index.html" ]]; then
  if [[ "$NO_BUILD" -eq 1 ]]; then
    echo "Missing frontend build at ${WEB_DIR}/frontend/dist."
    echo "Run: (cd ${WEB_DIR}/frontend && npm install && npm run build)"
    exit 1
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "npm is not installed; cannot build frontend."
    exit 1
  fi
  echo "Building frontend..."
  (
    cd "${WEB_DIR}/frontend"
    if [[ -f package-lock.json ]]; then
      npm ci
    else
      npm install
    fi
    npm run build
  )
fi

echo "Building web UI server binary..."
(
  cd "$WEB_DIR"
  go build -o "$WEB_BINARY"
)

echo "Starting loom on port ${LOOM_PORT}..."
LOOM_LOG="${RUN_DIR}/loom.log"
loom_env=()
if [[ "$LOOM_USE_DAEMON" -eq 0 ]]; then
  loom_env+=(BEADS_NO_DAEMON=1)
fi
start_bg "$LOOM_PID_FILE" "$LOOM_LOG" env "${loom_env[@]}" loom serve --no-webui --port "${LOOM_PORT}" --cors "http://localhost:${PORT}"

echo "Starting web UI on port ${PORT}..."
WEB_LOG="${RUN_DIR}/web-ui.log"
socket_arg=()
if [[ -n "$SOCKET_PATH" ]]; then
  socket_arg=(-socket "$SOCKET_PATH")
fi
pushd "$ROOT_DIR" >/dev/null
start_bg "$WEB_PID_FILE" "$WEB_LOG" env LOOM_SERVER_URL="http://localhost:${LOOM_PORT}" "$WEB_BINARY" -port "${PORT}" "${socket_arg[@]}"
popd >/dev/null

sleep 0.3

if ! kill -0 "$(cat "$LOOM_PID_FILE" 2>/dev/null || true)" 2>/dev/null; then
  echo "Loom failed to start. Log: $LOOM_LOG"
  tail -n 50 "$LOOM_LOG" || true
  exit 1
fi

if ! kill -0 "$(cat "$WEB_PID_FILE" 2>/dev/null || true)" 2>/dev/null; then
  echo "Web UI failed to start. Log: $WEB_LOG"
  tail -n 50 "$WEB_LOG" || true
  exit 1
fi

echo "Web UI: http://localhost:${PORT}"
echo "Logs: $WEB_LOG (web), $LOOM_LOG (loom)"

if [[ "$DETACH" -eq 1 ]]; then
  exit 0
fi

trap stop_all EXIT
wait "$(cat "$WEB_PID_FILE")"

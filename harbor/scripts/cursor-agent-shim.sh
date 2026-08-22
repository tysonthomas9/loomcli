#!/usr/bin/env bash
# loom-marathon cursor-agent shim (installed as /usr/local/bin/cursor-agent).
#
# Spend accounting: cursor keeps no token records on disk, but every
# `cursor-agent -p` turn ends with a stream-json `result` event carrying usage
# and starts with a `system` event naming the model. This shim tees exactly
# those two event lines — written by cursor itself, mid-turn, so a worker that
# is reaped right after its turn still counts — into one file per process
# under $LOOM_MARATHON_CURSOR_USAGE_DIR (host-mounted evidence; spend.sh
# prices each turn by model). stdout reaches the caller byte-for-byte.
#
# Model pinning: $LOOM_MARATHON_CURSOR_MODEL adds `--model <id>` to print-mode
# invocations (loom's cursor backend has no model flag; `auto` otherwise).
#
# exec keeps this process's PID = cursor-agent's, so the caller's shutdown
# signals land on the real process; the tee/grep helpers exit on EOF.
set -u

# loom_capture FILE: stdin -> stdout passthrough, appending system/result
# event lines to FILE first (so a dead stdout loses nothing).
loom_capture() {
  trap '' HUP
  local line
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      *'"type":"system"'*|*'"type":"result"'*) printf '%s\n' "$line" >> "$1" ;;
    esac
    printf '%s\n' "$line" 2>/dev/null || true
  done
}

REAL="${LOOM_MARATHON_CURSOR_REAL:-/installed-agent/cursor-home/.local/bin/cursor-agent}"
print=0
for a in "$@"; do
  case "$a" in -p|--print) print=1 ;; esac
done
args=()
if [ "$print" = 1 ] && [ -n "${LOOM_MARATHON_CURSOR_MODEL:-}" ]; then
  args+=(--model "$LOOM_MARATHON_CURSOR_MODEL")
fi
args+=("$@")

# Accounting is mandatory once LOOM_MARATHON_CURSOR_USAGE_DIR is set: a paid
# turn must never run unmetered, so an unusable directory fails the turn
# (exit 97, visible in the agent log) instead of silently skipping capture.
# Workers run under loom's harness PTY, so stdout being a TTY is normal here —
# never gate capture on it.
#
# Process shape: this shell stays the parent. The agent writes into a FIFO
# that the capture reader drains onto our stdout; TERM/INT/HUP are forwarded
# to the agent, and we exit with the agent's status only after the reader has
# flushed. (An exec'd agent with a process-substitution helper lost the final
# result line in ~1 of 6 PTY runs: the helper died with the session leader.)
dir="${LOOM_MARATHON_CURSOR_USAGE_DIR:-}"
if [ "$print" = 1 ] && [ -n "$dir" ]; then
  stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
  f="$dir/$stamp.jsonl"
  fifo="$dir/.$stamp.fifo"
  if ! mkdir -p "$dir" 2>/dev/null || ! { printf '{"type":"loom_shim","agent":"%s","pid":%d,"cwd":"%s"}\n' \
      "${LOOM_AGENT_NAME:-}" "$$" "$PWD" > "$f"; } 2>/dev/null || ! mkfifo "$fifo" 2>/dev/null; then
    echo "cursor-agent shim: spend accounting dir unusable: $dir (refusing to run an unmetered turn)" >&2
    exit 97
  fi
  loom_capture "$f" < "$fifo" &
  reader=$!
  "$REAL" ${args[@]+"${args[@]}"} > "$fifo" &
  child=$!
  trap 'kill -TERM "$child" 2>/dev/null' TERM INT HUP
  wait "$child"; rc=$?
  while [ "$rc" -gt 128 ] && kill -0 "$child" 2>/dev/null; do wait "$child"; rc=$?; done
  # The reader ends when every FIFO writer is gone; the agent is, but a
  # tool subprocess it orphaned could still hold the write end. The result
  # line (if any) was written before the agent exited, so bound the flush.
  for _ in $(seq 1 50); do kill -0 "$reader" 2>/dev/null || break; sleep 0.1; done
  kill "$reader" 2>/dev/null
  wait "$reader" 2>/dev/null
  rm -f "$fifo"
  exit "$rc"
fi
exec "$REAL" ${args[@]+"${args[@]}"}

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

dir="${LOOM_MARATHON_CURSOR_USAGE_DIR:-}"
if [ "$print" = 1 ] && [ -n "$dir" ] && [ ! -t 1 ] && mkdir -p "$dir" 2>/dev/null; then
  f="$dir/$(date -u +%Y%m%dT%H%M%SZ)-$$.jsonl"
  printf '{"type":"loom_shim","agent":"%s","pid":%d,"cwd":"%s"}\n' \
    "${LOOM_AGENT_NAME:-}" "$$" "$PWD" > "$f"
  tee_cmd=(tee)
  command -v stdbuf >/dev/null 2>&1 && tee_cmd=(stdbuf -oL tee)
  exec "$REAL" "${args[@]}" \
    > >("${tee_cmd[@]}" >(grep --line-buffered -E '"type":"(system|result)"' >> "$f"))
fi
exec "$REAL" "${args[@]}"

#!/usr/bin/env bash
# loom-marathon cursor-agent shim (installed as /usr/local/bin/cursor-agent).
#
# Spend accounting: cursor keeps no token records on disk, but every
# `cursor-agent -p` turn ends with a stream-json `result` event carrying usage
# and starts with a `system` event naming the model. This shim copies exactly
# those two event lines — written by cursor itself, mid-turn, so a worker that
# is reaped right after its turn still counts — into one file per process
# under $LOOM_MARATHON_CURSOR_USAGE_DIR (host-mounted evidence; spend.sh
# prices each turn by model). stdout reaches the caller byte-for-byte.
#
# Metering is mandatory for every print-mode turn: the usage dir defaults to
# the marathon path, `--output-format stream-json` is forced when the caller
# did not ask for a format (a plain `-p` turn has no usage record otherwise),
# and a turn that cannot be recorded — unusable dir, capture write failure,
# exit 0 without a `result` event — fails with exit 97 instead of running or
# reporting success unmetered.
#
# Model pinning: $LOOM_MARATHON_CURSOR_MODEL adds `--model <id>` to print-mode
# invocations (loom's cursor backend has no model flag; `auto` otherwise).
#
# Process shape: this shell stays the parent. The agent writes into a FIFO
# that the capture reader drains onto our stdout; TERM/INT/HUP (including the
# headless runtime's process-group TERM and Linux parent-death TERM) are
# forwarded to the agent with a KILL escalation, and we exit with the agent's
# status only after the reader has flushed. (An exec'd agent with a
# process-substitution helper lost the final result line in ~1 of 6 PTY
# runs: the helper died with the session leader.) The agent's stderr goes
# straight to ours, so a stderr line can appear before earlier stdout in a
# merged log; usage is parsed from the stdout channel only.
set -u

# loom_capture FILE MARK PARENT: stdin -> stdout passthrough, appending
# system/result event lines to FILE first (so a dead stdout loses nothing).
# MARK is created once the result line has been both recorded and forwarded.
# A failed record is reported to PARENT by SIGUSR1 at once — no disk involved,
# so a full disk or a reader later SIGKILLed on an orphan-held FIFO cannot
# lose the signal — plus FILE.err and exit 98 as best-effort evidence.
# Shutdown signals are ignored here: the reader must outlive a TERMed agent
# long enough to record the result it may still print.
loom_capture() {
  trap '' HUP TERM INT
  local line failed=0
  while IFS= read -r line || [ -n "$line" ]; do
    case "$line" in
      *'"type":"system"'*|*'"type":"result"'*)
        if ! printf '%s\n' "$line" >> "$1" 2>/dev/null; then
          if [ "$failed" = 0 ]; then failed=1; kill -USR1 "$3" 2>/dev/null; : > "$1.err" 2>/dev/null; fi
        fi ;;
    esac
    printf '%s\n' "$line" 2>/dev/null || true
    case "$line" in *'"type":"result"'*) : > "$2" ;; esac
  done
  [ "$failed" = 0 ] || exit 98
}

REAL="${LOOM_MARATHON_CURSOR_REAL:-/installed-agent/cursor-home/.local/bin/cursor-agent}"
print=0
fmt=0
for a in "$@"; do
  case "$a" in
    -p|--print) print=1 ;;
    --output-format|--output-format=*) fmt=1 ;;
  esac
done
args=()
if [ "$print" = 1 ]; then
  [ -n "${LOOM_MARATHON_CURSOR_MODEL:-}" ] && args+=(--model "$LOOM_MARATHON_CURSOR_MODEL")
  [ "$fmt" = 1 ] || args+=(--output-format stream-json)
fi
args+=("$@")

if [ "$print" != 1 ]; then
  exec "$REAL" ${args[@]+"${args[@]}"}
fi

# Only the record file lives in the usage dir: that is a host bind mount in
# the marathon container, where mkfifo is refused (trial team-cursor-112144:
# every turn exited 97 with the header written). FIFO and marker go to
# container-local scratch.
dir="${LOOM_MARATHON_CURSOR_USAGE_DIR:-/logs/agent/cursor-usage}"
scratch="${LOOM_MARATHON_CURSOR_SHIM_TMP:-${TMPDIR:-/tmp}/cursor-agent-shim}"
stamp="$(date -u +%Y%m%dT%H%M%SZ)-$$"
f="$dir/$stamp.jsonl"
fifo="$scratch/.$stamp.fifo"
mark="$scratch/.$stamp.result"
if ! mkdir -p "$dir" "$scratch" 2>/dev/null || ! { printf '{"type":"loom_shim","agent":"%s","pid":%d,"cwd":"%s"}\n' \
    "${LOOM_AGENT_NAME:-}" "$$" "$PWD" > "$f"; } 2>/dev/null; then
  echo "cursor-agent shim: spend accounting dir unusable: $dir (refusing to run an unmetered turn)" >&2
  exit 97
fi
if ! mkfifo "$fifo" 2>/dev/null; then
  echo "cursor-agent shim: cannot create capture FIFO in $scratch (refusing to run an unmetered turn)" >&2
  exit 97
fi
rm -f "$mark" "$f.err"

# Signal handling is armed before the agent exists: a signal that lands in the
# gap sets `aborted` and is acted on right after the spawn. TERM is forwarded,
# then KILL after 5s if the agent ignores it. `child` is cleared the moment
# the agent is reaped and the escalation timer cancelled, so a late signal
# (during the flush) only shortens the flush — nothing is ever sent to a PID
# that could have been reused.
child=""
aborted=""
esc=""
on_signal() {
  aborted=1
  if [ -n "$child" ] && [ -z "$esc" ]; then
    kill -TERM "$child" 2>/dev/null
    { sleep 5; kill -KILL "$child" 2>/dev/null; } &
    esc=$!
  fi
}
trap on_signal TERM INT HUP
record_failed=""
trap 'record_failed=1' USR1

loom_capture "$f" "$mark" "$$" < "$fifo" &
reader=$!
"$REAL" ${args[@]+"${args[@]}"} > "$fifo" &
child=$!
[ -n "$aborted" ] && on_signal
wait "$child"; rc=$?
while [ "$rc" -gt 128 ] && kill -0 "$child" 2>/dev/null; do wait "$child"; rc=$?; done
child=""
if [ -n "$esc" ]; then kill "$esc" 2>/dev/null; wait "$esc" 2>/dev/null; esc=""; fi

# Flush the reader. With the result already recorded and forwarded (MARK), or
# after a shutdown signal (no result is coming), anything still in the FIFO
# is output from a tool subprocess the agent orphaned, so the wait is short.
# Otherwise the reader may still be pushing the result through a slow PTY:
# give it much longer before giving up. Both conditions are re-read every
# tick so a signal that lands mid-flush takes effect immediately.
i=0
while kill -0 "$reader" 2>/dev/null; do
  i=$((i + 1))
  bound=300
  { [ -e "$mark" ] || [ -n "$aborted" ]; } && bound=50
  [ "$i" -ge "$bound" ] && break
  sleep 0.1
done
# The reader ignores TERM (see loom_capture), so the give-up kill is KILL; each
# record is a single-line write, so nothing half-written can be left behind.
kill -KILL "$reader" 2>/dev/null
wait "$reader" 2>/dev/null; rrc=$?
rm -f "$fifo" "$mark"

# Accounting verdict, independent of how the agent exited: a turn whose
# record could not be written is reported as unaccounted even when the agent
# failed (its tokens were still billed), and a zero exit additionally needs
# the result record.
if [ -n "$record_failed" ] || [ "$rrc" -eq 98 ] || [ -e "$f.err" ]; then
  echo "cursor-agent shim: spend record write failed for $f (turn not accounted; agent rc=$rc)" >&2
  rc=97
elif [ "$rc" -eq 0 ] && ! grep -q '"type":"result"' "$f" 2>/dev/null; then
  echo "cursor-agent shim: agent exited 0 without a result event (turn not accounted, see $f)" >&2
  rc=97
fi
rm -f "$f.err"
exit "$rc"

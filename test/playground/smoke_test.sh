#!/usr/bin/env bash
# Shell smoke test for the playground workspace.
#
# What it verifies (end-to-end):
#   - setup.sh creates the workspace, agents, and 3 seed tasks
#   - `loom daemon` drives them to status=closed within the timeout
#   - playground.txt receives 3 result blocks (one per task)
#   - 3 "Playground implementation (PLAYGROUND-N)" commits land in the
#     coder worktree
#   - Each agent invocation produces an assistant_transcript.jsonl entry
#
# Prereqs:
#   - `loom serve` reachable at http://localhost:8080
#   - `loom` on PATH
#   - bash 4+, git, curl, jq optional
#
# Usage:
#   bash test/playground/smoke_test.sh
#
# Exit codes: 0 pass, 1 fail (assertion), 2 prereq missing, 3 timeout
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
RUNTIME="$HERE/.runtime"
TIMEOUT_SECS="${PLAYGROUND_SMOKE_TIMEOUT:-90}"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
log()   { printf '[smoke %s] %s\n' "$(date -u +%H:%M:%S)" "$*"; }

DAEMON_PID=""

cleanup() {
  local rc=$?
  if [ -n "$DAEMON_PID" ] && kill -0 "$DAEMON_PID" 2>/dev/null; then
    log "stopping daemon (PID $DAEMON_PID)"
    kill "$DAEMON_PID" 2>/dev/null || true
    wait "$DAEMON_PID" 2>/dev/null || true
  fi
  if [ -x "$HERE/teardown.sh" ]; then
    log "tearing down workspace"
    "$HERE/teardown.sh" >/dev/null 2>&1 || true
  fi
  rm -rf "$HERE/.loom" 2>/dev/null || true
  if [ $rc -eq 0 ]; then green "SMOKE PASS"; else red "SMOKE FAIL (rc=$rc)"; fi
  exit $rc
}
trap cleanup EXIT

#---------- prereqs ----------
command -v loom >/dev/null || { red "loom not on PATH"; exit 2; }
command -v git  >/dev/null || { red "git not on PATH";  exit 2; }
curl -sfm 2 http://localhost:8080/health >/dev/null \
  || { red "loom serve not reachable at http://localhost:8080 — start it first"; exit 2; }
log "prereqs OK"

#---------- pre-clean any stale state ----------
"$HERE/teardown.sh" >/dev/null 2>&1 || true
rm -rf "$RUNTIME" "$HERE/.loom"

#---------- run setup ----------
log "running setup.sh"
"$HERE/setup.sh" >/dev/null

# shellcheck disable=SC1091
. "$RUNTIME/env"

#---------- start daemon ----------
log "starting loom daemon (timeout ${TIMEOUT_SECS}s)"
loom daemon >"$RUNTIME/daemon.log" 2>&1 &
DAEMON_PID=$!
disown

#---------- poll for all tasks closed ----------
deadline=$(( $(date +%s) + TIMEOUT_SECS ))
while :; do
  closed=$(loom data list 2>/dev/null | grep -c closed || true)
  if [ "$closed" -ge 3 ]; then
    log "all 3 tasks closed after $(( TIMEOUT_SECS - (deadline - $(date +%s)) ))s"
    break
  fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    red "timeout: only $closed/3 tasks closed after ${TIMEOUT_SECS}s"
    log "--- last 50 lines of daemon.log ---"
    tail -50 "$RUNTIME/daemon.log"
    exit 3
  fi
  sleep 2
done

#---------- assertions ----------
fails=0
expect_eq() {
  local got="$1" expected="$2" label="$3"
  if [ "$got" = "$expected" ]; then green "  ✓ $label ($got)"; else red "  ✗ $label: got $got, expected $expected"; fails=$((fails+1)); fi
}
expect_ge() {
  local got="$1" min="$2" label="$3"
  if [ "$got" -ge "$min" ] 2>/dev/null; then green "  ✓ $label ($got >= $min)"; else red "  ✗ $label: got $got, expected ≥ $min"; fails=$((fails+1)); fi
}

log "assertions:"
closed=$(loom data list 2>/dev/null | grep -c closed)
expect_eq "$closed" "3" "3 tasks closed"

coder_repo="$HOME/.loom/workspaces/playground/worktrees/repo/playground-coder"
impl_commits=$(git -C "$coder_repo" log --oneline 2>/dev/null | grep -c "Playground implementation (PLAYGROUND-" || true)
expect_eq "$impl_commits" "3" "3 impl commits in coder worktree"

result_blocks=$(grep -c '^Result: playground deterministic backend' "$coder_repo/playground.txt" 2>/dev/null || echo 0)
expect_eq "$result_blocks" "3" "3 result blocks in playground.txt"

transcript_count=$(find "$HOME/.loom/workspaces/playground/sessions" -name agent_transcript.jsonl 2>/dev/null | wc -l | tr -d ' ')
expect_ge "$transcript_count" "6" "≥6 transcripts (3 planner + 3 coder)"

# Spot-check one task has the canned design attached
design_lines=$(loom data show PLAYGROUND-1 2>/dev/null | grep -c "Approved design: playground planner" || true)
expect_ge "$design_lines" "1" "PLAYGROUND-1 has playground design attached"

if [ "$fails" -gt 0 ]; then exit 1; fi

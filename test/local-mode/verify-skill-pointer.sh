#!/usr/bin/env bash
# Live-model smoke of the stale-session skill catalog: proves a LONG-LIVED
# codex session learns about skills added/removed after its session-start
# snapshot, via the managed UserPromptSubmit hook (files) + the
# loom-skill-catalog/INDEX.md pointer (awareness).
#
# COSTS REAL MODEL TOKENS and needs the codex-variant stack
# (make local-mode-codex-up) with working codex auth in the container.
# On-demand only — do not wire into CI.
#
#   LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode make local-mode-skill-pointer-verify
set -euo pipefail

VERIFY_LABEL="verify-skill-pointer"
# shellcheck source=test/local-mode/verify-lib.sh
. "$(dirname "$0")/verify-lib.sh"

TURN_TIMEOUT="${SKILL_POINTER_TURN_TIMEOUT:-180}"

require_running
require_in_container codex "bring the stack up with make local-mode-codex-up"
require_in_container tmux

RUN_ID="$(date +%s)-$$"
SEED="vsp-${RUN_ID}-seed"
LIVE="vsp-${RUN_ID}-live"
DIR="/tmp/verify-pointer-${RUN_ID}"
TMUX_SESSION="vsp-${RUN_ID}"

cleanup() {
  cexec "tmux kill-session -t ${TMUX_SESSION} >/dev/null 2>&1; loom skill delete ${SEED} >/dev/null 2>&1; loom skill delete ${LIVE} >/dev/null 2>&1; rm -rf ${DIR}" || true
}
trap cleanup EXIT

# Send a prompt to the codex TUI. Text and Enter go separately with a pause —
# codex's paste detection swallows an Enter that arrives with the text.
send_turn() {
  cexec "tmux send-keys -t ${TMUX_SESSION} \"$1\"" >/dev/null
  sleep 2
  cexec "tmux send-keys -t ${TMUX_SESSION} Enter" >/dev/null
}

dump_pane() {
  cexec "tmux capture-pane -t ${TMUX_SESSION} -p -S -40" | grep -v '^$' | tail -25 >&2 || true
}

# Poll the pane until a pattern appears (or time out).
#
# An exhausted codex account renders as an ordinary pane line, so without the
# usage-limit probe the run looks identical to a hung model: it burns the full
# TURN_TIMEOUT and then blames the turn. Bail on it immediately instead — the
# environment is at fault, not the skills vertical under test.
wait_for_pane() {
  pattern="$1"
  label="$2"
  deadline=$((SECONDS + TURN_TIMEOUT))
  while true; do
    # Tolerate a failed capture: the pane is polled, and a session that has not
    # come up yet (or a blipped exec) must not abort the run through set -e.
    pane="$(cexec "tmux capture-pane -t ${TMUX_SESSION} -p -S -80" 2>/dev/null || true)"
    if printf '%s\n' "$pane" | grep -q "$pattern"; then
      log "ok: $label"
      return 0
    fi
    if printf '%s\n' "$pane" | grep -qiE "hit your usage limit|rate limit reached|purchase more credits"; then
      log "pane at usage limit:" >&2
      dump_pane
      fatal "codex account is out of credits — rerun when the quota resets (this is an environment limit, not a skills failure)"
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      log "pane at timeout:" >&2
      dump_pane
      fatal "timed out waiting for: $label"
    fi
    sleep 3
  done
}

log "engine=${ENGINE} container=${CONTAINER} run=${RUN_ID}"

# Seed one skill, materialize the session dir, and trust it for codex.
cexec "printf 'The seed phrase is SEED-${RUN_ID}.\n' | loom skill create ${SEED} --description 'pointer smoke: present at session start' >/dev/null 2>&1"
cexec "mkdir -p ${DIR} && cd ${DIR} && loom skill materialize >/dev/null 2>&1"
cexec "grep -Fq '[projects.\"${DIR}\"]' /root/.codex/config.toml 2>/dev/null || printf '\n[projects.\"%s\"]\ntrust_level = \"trusted\"\n' '${DIR}' >> /root/.codex/config.toml"

# Long-lived TUI session: turn 1 pins the session-start skill snapshot.
cexec "tmux new-session -d -s ${TMUX_SESSION} -c ${DIR} codex"
sleep 8
send_turn "Reply with exactly: HELLO-${RUN_ID}"
wait_for_pane "^• HELLO-${RUN_ID}" "turn 1 completed (snapshot pinned with ${SEED})"

# Mutate behind the session's back. No manual materialize: the managed
# UserPromptSubmit hook must converge the files during turn 2 itself.
cexec "printf 'The live phrase is LIVE-${RUN_ID}.\n' | loom skill create ${LIVE} --description 'pointer smoke: added mid-session' >/dev/null 2>&1"
cexec "loom skill delete ${SEED} >/dev/null 2>&1"

send_turn "what skills do you see now"
wait_for_pane "${LIVE}" "stale session reported the mid-session skill ${LIVE}"

# The files must have converged via the hook, and the deleted skill must not
# appear in the model's final answer (its name is unique to this run, so any
# mention after the question would be a stale-list leak).
cexec "test -e ${DIR}/.agents/skills/${LIVE} && ! test -e ${DIR}/.agents/skills/${SEED}" \
  || fatal "hook did not converge files during turn 2"
log "ok: managed hook converged files during the turn"

ANSWER="$(cexec "tmux capture-pane -t ${TMUX_SESSION} -p -S -80" | sed -n '/what skills do you see now/,$p')"
printf '%s\n' "$ANSWER" | grep -q "${LIVE}" || fatal "answer does not mention ${LIVE}"
if printf '%s\n' "$ANSWER" | grep -q "${SEED}"; then
  # Mentioning the seed is fine only if the model says it was removed.
  printf '%s\n' "$ANSWER" | grep -qiE "removed|no longer|deleted|was present" \
    || fatal "answer still advertises deleted skill ${SEED}"
fi
log "ok: answer reflects the live catalog, not the session-start snapshot"

log "PASS: stale codex session healed via loom-skill-catalog/INDEX.md"

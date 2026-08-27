#!/usr/bin/env bash
# Live-model smoke of the stale-session skill catalog: proves a LONG-LIVED
# agent session learns about skills added/removed after its session-start
# snapshot, via the managed UserPromptSubmit hook (files) + the
# loom-skill-catalog/INDEX.md pointer (awareness).
#
# Runs against either backend. The two differ in how the managed hook reaches
# the session, which is the whole point of covering both:
#   codex  — trust-gates repo-local hooks and misreads .codex in linked
#            worktrees, so the image bakes /etc/codex/requirements.toml.
#   claude — the ordinary hookcfg adapter path: .claude/settings.json in the
#            work dir, which is what a loom-launched session writes.
#
# COSTS REAL MODEL TOKENS and needs a stack with working auth in the container
# (make local-mode-codex-up or make local-mode-claude-up). On-demand only —
# do not wire into CI.
#
#   LOCAL_MODE_COMPOSE_PROJECT=loomcli-local-mode make local-mode-skill-pointer-verify
#
# SKILL_POINTER_BACKEND=codex|claude overrides the autodetect.
set -euo pipefail

VERIFY_LABEL="verify-skill-pointer"
# shellcheck source=test/local-mode/verify-lib.sh
. "$(dirname "$0")/verify-lib.sh"

TURN_TIMEOUT="${SKILL_POINTER_TURN_TIMEOUT:-180}"
BACKEND="${SKILL_POINTER_BACKEND:-auto}"

require_running
require_in_container tmux

if [ "$BACKEND" = auto ]; then
  for candidate in codex claude; do
    if cexec "command -v ${candidate} >/dev/null 2>&1"; then
      BACKEND="$candidate"
      break
    fi
  done
fi
case "$BACKEND" in
  codex | claude) ;;
  auto) fatal "neither codex nor claude is installed in ${CONTAINER}; bring up a backend stack (make local-mode-codex-up / make local-mode-claude-up)" ;;
  *) fatal "SKILL_POINTER_BACKEND must be codex or claude, got ${BACKEND}" ;;
esac
require_in_container "$BACKEND" "bring the stack up with make local-mode-${BACKEND}-up"

RUN_ID="$(date +%s)-$$"
SEED="vsp-${RUN_ID}-seed"
LIVE="vsp-${RUN_ID}-live"
DIR="/tmp/verify-pointer-${RUN_ID}"
TMUX_SESSION="vsp-${RUN_ID}"

cleanup() {
  cexec "tmux kill-session -t ${TMUX_SESSION} >/dev/null 2>&1; loom skill delete ${SEED} >/dev/null 2>&1; loom skill delete ${LIVE} >/dev/null 2>&1; rm -rf ${DIR}" || true
}
trap cleanup EXIT

# Send a prompt to the TUI. Text and Enter go separately with a pause — both
# TUIs run paste detection that swallows an Enter arriving with the text.
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
# An exhausted account or a dead credential renders as an ordinary pane line,
# so without these probes the run looks identical to a hung model: it burns the
# full TURN_TIMEOUT and then blames the turn. Bail on them immediately instead —
# the environment is at fault, not the skills vertical under test.
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
      fatal "${BACKEND} account is out of credits — rerun when the quota resets (this is an environment limit, not a skills failure)"
    fi
    if printf '%s\n' "$pane" | grep -qiE "login expired|please run /login|not logged in"; then
      log "pane at auth failure:" >&2
      dump_pane
      fatal "${BACKEND} credentials in ${CONTAINER} are expired — refresh the mounted auth and restart the stack (this is an environment limit, not a skills failure)"
    fi
    if [ "$SECONDS" -ge "$deadline" ]; then
      log "pane at timeout:" >&2
      dump_pane
      fatal "timed out waiting for: $label"
    fi
    sleep 3
  done
}

# Trust the work dir so the TUI opens straight into a prompt instead of gating
# on a dialog, and make sure the managed hook is in place for this session.
#
# The claude branch writes the same managed block that
# hookcfg.EnsureSkillMaterializeHook writes, because no CLI installs it — in
# production a loom-launched session (lead, supervisor spawn, terminal) does.
# Keep it in sync with that function; the shape is pinned by hookcfg's tests.
prepare_backend() {
  case "$BACKEND" in
    codex)
      cexec "grep -Fq '[projects.\"${DIR}\"]' /root/.codex/config.toml 2>/dev/null || printf '\n[projects.\"%s\"]\ntrust_level = \"trusted\"\n' '${DIR}' >> /root/.codex/config.toml"
      ;;
    claude)
      cexec "mkdir -p ${DIR}/.claude && printf '%s\n' '{\"hooks\":{\"UserPromptSubmit\":[{\"matcher\":\"\",\"hooks\":[{\"type\":\"command\",\"command\":\"loom skill materialize\"}]}]}}' > ${DIR}/.claude/settings.json"
      cexec "jq --arg d '${DIR}' '.projects[\$d] = ((.projects[\$d] // {}) + {hasTrustDialogAccepted:true, hasCompletedProjectOnboarding:true})' /root/.claude.json > ${DIR}/.claude-state.json && mv ${DIR}/.claude-state.json /root/.claude.json"
      ;;
  esac
}

# Each TUI prefixes a finished assistant message with its own glyph.
reply_marker() {
  case "$BACKEND" in
    codex) printf '^• ' ;;
    claude) printf '^● ' ;;
  esac
}

log "engine=${ENGINE} container=${CONTAINER} backend=${BACKEND} run=${RUN_ID}"

# Seed one skill, materialize the session dir, and prepare the backend.
cexec "printf 'The seed phrase is SEED-${RUN_ID}.\n' | loom skill create ${SEED} --description 'pointer smoke: present at session start' >/dev/null 2>&1"
cexec "mkdir -p ${DIR} && cd ${DIR} && loom skill materialize >/dev/null 2>&1"
prepare_backend

# Long-lived TUI session: turn 1 pins the session-start skill snapshot.
cexec "tmux new-session -d -s ${TMUX_SESSION} -c ${DIR} ${BACKEND}"
sleep 10
send_turn "Reply with exactly: HELLO-${RUN_ID}"
wait_for_pane "$(reply_marker)HELLO-${RUN_ID}" "turn 1 completed (snapshot pinned with ${SEED})"

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

log "PASS: stale ${BACKEND} session healed via loom-skill-catalog/INDEX.md"

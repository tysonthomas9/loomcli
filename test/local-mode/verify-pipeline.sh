#!/usr/bin/env bash
# Verify that the production-shaped topology the overlays ask for is the
# topology that actually came up.
#
# Every assertion here covers a failure that is silent from the outside: a
# daemon that refused to start because max_agents was exceeded, an extra repo
# that was never registered, a pipeline whose label routing was never applied.
# The stack reports "running" in all three cases, so a fix validated against it
# was validated against a topology that does not exist.
#
#   LOCAL_MODE_COMPOSE_FILES=test/local-mode/docker-compose.topology.yml \
#     make local-mode-up
#   make local-mode-pipeline-verify
#
# The label-routing section runs only when the stack was started with the
# pipeline overlay. No model calls: everything runs on the deterministic
# localdogfood backend.
set -euo pipefail

VERIFY_LABEL="verify-pipeline"
# shellcheck source=test/local-mode/verify-lib.sh
. "$(dirname "$0")/verify-lib.sh"

MIN_AGENTS="${LOCAL_MODE_PIPELINE_MIN_AGENTS:-3}"
EXPECT_REPOS="${LOCAL_MODE_PIPELINE_EXPECT_REPOS:-second-repo}"
STAGE_ROLE="${LOCAL_MODE_PIPELINE_STAGE_ROLE:-critic}"
CYCLE_ROLE="${LOCAL_MODE_PIPELINE_CYCLE_ROLE:-plan}"
CYCLE_LABEL="${LOCAL_MODE_PIPELINE_CYCLE_LABEL:-criticized}"

require_running
log "engine=${ENGINE} container=${CONTAINER}"

# 1. The agent count. max_agents is enforced at daemon creation, so exceeding
#    it stops every agent rather than just the extra one.
agents="$(cexec 'loom agentdef list 2>/dev/null' | grep -c 'role=' || true)"
log "agents configured: ${agents}"
[ "${agents:-0}" -ge "$MIN_AGENTS" ] ||
  fatal "expected at least ${MIN_AGENTS} agents, found ${agents:-0} (is LOOM_LOCAL_MODE_MAX_AGENTS large enough?)"

# 2. The extra repos. A single-repo workspace short-circuits repo selection
#    before the no-selector fallback, so the behavior under test is unreachable.
for repo in ${EXPECT_REPOS//,/ }; do
  cexec 'loom repo list 2>/dev/null' | grep -q "$repo" ||
    fatal "extra repo ${repo} is not registered"
  log "ok: extra repo ${repo} registered"
done

# 3. The label routing, when the pipeline overlay is in play. This is the
#    assertion the stack most needs: the stages stamp their labels whether or
#    not claims are gated, so a pipeline that routes nothing looks exactly like
#    one that works.
if [ -n "$(cexec 'printf %s "${LOOM_LOCAL_MODE_PIPELINE:-}"')" ]; then
  role_routes_on() {
    # role_routes_on <role> <label>
    cexec "loom role show $1 --json 2>/dev/null" | grep -q "\"$2\""
  }
  role_routes_on "$STAGE_ROLE" "$CYCLE_LABEL" ||
    fatal "role ${STAGE_ROLE} carries no ${CYCLE_LABEL} routing — the pipeline stamps labels but gates no claims"
  log "ok: ${STAGE_ROLE} label routing applied"
  role_routes_on "$CYCLE_ROLE" "$CYCLE_LABEL" ||
    fatal "role ${CYCLE_ROLE} is not re-armed on ${CYCLE_LABEL}"
  log "ok: ${CYCLE_ROLE} label routing applied"
else
  log "skip: label routing (stack started without the pipeline overlay)"
fi

# 4. The supervisor. Match precisely, and put the failure string FIRST:
#    `loom daemon status` prints exactly "Daemon: running (PID n)" or
#    "Daemon: not running", so a `*running*` glob matches BOTH and passes on a
#    dead supervisor — the one silent failure this stack exists to surface.
#    Anything unrecognized fails too: an empty status means the exec itself did
#    not work.
status="$(cexec 'loom daemon status 2>/dev/null' | head -1 || true)"
log "${status:-daemon status unavailable}"
case "$status" in
  *"Daemon: not running"*)
    fatal "daemon is not running — see: make local-mode-logs"
    ;;
  *"Daemon: running"*) ;;
  *)
    fatal "unrecognized daemon status: [${status:-<empty>}] — see: make local-mode-logs"
    ;;
esac

log "PASS"

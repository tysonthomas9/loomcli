#!/usr/bin/env bash
# All-cursor team arm launcher (lead + workers on cursor-agent).
#   PROFILE=capped (default): 40 min wall, $25 cap, verification off — the smoke trial.
#   PROFILE=full: 4 h work + 40 min reserve, $180 cap, replica judge (verification on).
# Credential: default = host-account login at ~/.cursor-marathon/.cursor/auth.json (see
# adapter), else ~/.cursor/marathon-api-key; override with CURSOR_AUTH_JSON= / CURSOR_KEY_FILE=.
# The adapter only uploads the credential file; it is never read or printed here.
# Launch detached (Popen start_new_session, stdin DEVNULL) — see harbor/README.md "Watching a
# trial"; the final HARBOR_EXIT=<n> line is the terminal marker.
set -euo pipefail
HARBOR_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SWE="${SWE_MARATHON_DIR:-$HARBOR_DIR/../../../swe-marathon}"
SWE="$(cd "$SWE" && pwd)"
JOBS="$HARBOR_DIR/test/jobs"
PROFILE="${PROFILE:-capped}"
case "$PROFILE" in
  capped) BUDGET=2400;  RESERVE=480;  CADENCE=90;  CAP=25;  VERIFY=(--disable-verification); TAG=team-cursor ;;
  full)   BUDGET=14400; RESERVE=2400; CADENCE=360; CAP=180; VERIFY=();                        TAG=team-cursor-full ;;
  *) echo "unknown PROFILE=$PROFILE (capped|full)" >&2; exit 2 ;;
esac
JOB_NAME="${JOB_NAME:-$TAG-$(date +%H%M%S)}"
# Replica-judge policy: the official CUA must never run on these trials.
export ANTHROPIC_API_KEY="replica-policy-no-official-cua"
export PODMAN_COMPOSE_WARNING_LOGS=false
echo "JOB_NAME=$JOB_NAME PROFILE=$PROFILE budget=${BUDGET}s reserve=${RESERVE}s cadence=${CADENCE}s cap=\$$CAP"
cd "$SWE"
set +e
PYTHONPATH="$HARBOR_DIR" harbor run \
  -p tasks/slack-clone \
  -a loom_harbor:LoomAgent \
  -e docker \
  --ak team=fullstack-app \
  --ak max_agents=4 \
  --ak lead_mode=persistent \
  --ak prompts_profile=generic \
  --ak budget_secs="$BUDGET" \
  --ak reserve_secs="$RESERVE" \
  --ak cadence_secs="$CADENCE" \
  --ak spend_cap_usd="$CAP" \
  --ak backend=cursor \
  ${CURSOR_AUTH_JSON:+--ak cursor_auth_json_path="$CURSOR_AUTH_JSON"} \
  ${CURSOR_KEY_FILE:+--ak cursor_api_key_path="$CURSOR_KEY_FILE"} \
  ${CURSOR_MODEL:+--ak cursor_model="$CURSOR_MODEL"} \
  -o "$JOBS" --job-name "$JOB_NAME" -n 1 -q -y \
  ${VERIFY[@]+"${VERIFY[@]}"}
echo "HARBOR_EXIT=$?"

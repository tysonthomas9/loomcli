#!/bin/sh
# seed.sh — populate identical fixture set into both parity backends.
#
# Flow:
#   1. Ensure fleet-db has the PARITY workspace (admin API; needs X-Actor
#      header because fleet-db is running in --auth-dev-mode).
#   2. Discover loom-beads' workspace ID via GET /api/workspaces.
#   3. Discover loom-fleet's workspace ID likewise (loom-fleet surfaces
#      fleet-db's workspaces). If loom-fleet has no workspace yet, hit
#      fleet-db admin directly and trust the loom webui to re-list.
#   4. For each fixture issue: POST to
#      loom-beads:/api/workspaces/{beads-ws}/issues  AND
#      loom-fleet:/api/workspaces/{fleet-ws}/issues.
#
# Environment inputs (set by docker-compose.parity.yml):
#   FLEET_URL        e.g. http://fleet-db:8080
#   FLEET_WORKSPACE  e.g. PARITY
#   BEADS_URL        e.g. http://loom-beads:8080
#
# Additional variables discovered at runtime:
#   LOOM_FLEET_URL   hard-coded below — http://loom-fleet:8080
#   ACTOR            parity-harness — forwarded as X-Actor to fleet-db admin
#
# On failure, exits non-zero and leaves partial state behind so the
# operator can inspect containers for forensics.

set -eu

: "${FLEET_URL:?must be set}"
: "${FLEET_WORKSPACE:?must be set}"
: "${BEADS_URL:?must be set}"

LOOM_FLEET_URL="${LOOM_FLEET_URL:-http://loom-fleet:8080}"
ACTOR="parity-harness"

echo "[seed] FLEET_URL=${FLEET_URL}  workspace=${FLEET_WORKSPACE}"
echo "[seed] BEADS_URL=${BEADS_URL}"
echo "[seed] LOOM_FLEET_URL=${LOOM_FLEET_URL}"

# ──────────────────────────────────────────────────────────────────
# Phase 1: create fleet-db workspace via admin API (X-Actor required)
# ──────────────────────────────────────────────────────────────────
echo "[seed] ensuring fleet-db workspace ${FLEET_WORKSPACE} exists..."
curl -fsS -X POST "${FLEET_URL}/api/v1/admin/workspaces" \
    -H 'Content-Type: application/json' \
    -H "X-Actor: ${ACTOR}" \
    -d "{\"key\":\"${FLEET_WORKSPACE}\",\"name\":\"Parity Fixture\"}" \
    > /dev/null 2>&1 \
    && echo "[seed]   ✓ created" \
    || echo "[seed]   ≈ already exists (ok)"

# ──────────────────────────────────────────────────────────────────
# Phase 2: discover loom-beads workspace ID
# ──────────────────────────────────────────────────────────────────
BEADS_WS_ID=$(curl -fsS "${BEADS_URL}/api/workspaces" \
    | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
if [ -z "${BEADS_WS_ID}" ]; then
    echo "[seed] FATAL: loom-beads has no workspaces"
    exit 2
fi
echo "[seed] loom-beads workspace ID: ${BEADS_WS_ID}"

# ──────────────────────────────────────────────────────────────────
# Phase 3: discover loom-fleet workspace ID (or fall back)
# ──────────────────────────────────────────────────────────────────
FLEET_WS_ID=$(curl -fsS "${LOOM_FLEET_URL}/api/workspaces" 2>/dev/null \
    | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1 || true)
if [ -z "${FLEET_WS_ID}" ]; then
    # If loom-fleet's webui doesn't list the workspace, try creating one
    # via the loom webui itself — which proxies to fleet-db.
    echo "[seed] loom-fleet has no workspaces; bootstrapping..."
    # Loom webui workspace schema requires "type" (one of empty/clone/template).
    BOOT=$(curl -sS -X POST "${LOOM_FLEET_URL}/api/workspaces" \
        -H 'Content-Type: application/json' \
        -d "{\"name\":\"${FLEET_WORKSPACE}\",\"path\":\"/workspace\",\"type\":\"empty\"}" 2>&1)
    echo "[seed]   bootstrap response: ${BOOT}"
    FLEET_WS_ID=$(echo "${BOOT}" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1 || true)
fi
if [ -z "${FLEET_WS_ID}" ]; then
    echo "[seed] could not discover or create loom-fleet workspace; will"
    echo "[seed] fall back to using FLEET_WORKSPACE (${FLEET_WORKSPACE}) as the ID"
    FLEET_WS_ID="${FLEET_WORKSPACE}"
fi
echo "[seed] loom-fleet workspace ID: ${FLEET_WS_ID}"

# ──────────────────────────────────────────────────────────────────
# Phase 4: seed fixtures identically on both sides
# ──────────────────────────────────────────────────────────────────
post_issue() {
    local title="$1"; local type="$2"; local priority="$3"
    local body
    body="{\"title\":\"$title\",\"issue_type\":\"$type\",\"priority\":$priority}"
    echo "[seed] creating: $title ($type, P$priority)"
    curl -fsS -X POST "${BEADS_URL}/api/workspaces/${BEADS_WS_ID}/issues" \
        -H 'Content-Type: application/json' \
        -d "$body" > /dev/null \
        || { echo "[seed]   FAILED on loom-beads"; exit 3; }
    curl -fsS -X POST "${LOOM_FLEET_URL}/api/workspaces/${FLEET_WS_ID}/issues" \
        -H 'Content-Type: application/json' \
        -d "$body" > /dev/null \
        || { echo "[seed]   FAILED on loom-fleet"; exit 4; }
}

post_issue "Epic Alpha"  epic    2
post_issue "Epic Beta"   epic    2
post_issue "Epic Gamma"  epic    3

post_issue "Add login flow"           feature 2
post_issue "Fix checkout NPE"         bug     1
post_issue "Refactor auth middleware" task    3
post_issue "Onboarding wizard"        feature 2
post_issue "Cache invalidation bug"   bug     1
post_issue "Update README"            task    4
post_issue "Session timeout edge"     bug     2
post_issue "Theme toggle"             feature 3
post_issue "Flaky test: login_e2e"    task    2
post_issue "Clarify rate limit docs"  task    4

echo "[seed] done"

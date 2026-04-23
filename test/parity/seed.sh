#!/bin/sh
# seed.sh — populate identical fixture set into both parity backends.
#
# Strategy:
#   - fleet-db side: use the `fdb` CLI directly (--server URL, --workspace
#     PARITY, --actor parity-harness). Tests the real user-facing CLI
#     path, not just the HTTP wire shape.
#   - loom-beads side: POST to loom-beads webui's
#     /api/workspaces/{id}/issues path, since bd's CLI lives inside the
#     loom-beads container and isn't easily callable from here.
#
# Workspace bootstrap:
#   - fleet-db: `fdb workspace create PARITY` via fdb (admin API; fdb
#     forwards X-Actor as an authenticated identity in --auth-dev-mode)
#   - loom-beads: discovered at runtime via GET /api/workspaces (loom
#     auto-creates one called "workspace" on container init)
#
# Environment inputs (set by docker-compose.parity.yml):
#   FLEET_URL        e.g. http://fleet-db:8080
#   FLEET_WORKSPACE  e.g. PARITY
#   BEADS_URL        e.g. http://loom-beads:8080

set -eu

: "${FLEET_URL:?must be set}"
: "${FLEET_WORKSPACE:?must be set}"
: "${BEADS_URL:?must be set}"

ACTOR="parity-harness"

echo "[seed] FLEET_URL=${FLEET_URL}  workspace=${FLEET_WORKSPACE}  actor=${ACTOR}"
echo "[seed] BEADS_URL=${BEADS_URL}"

# Base invocations. `workspace create` needs --key (admin endpoint),
# but per-workspace data operations need -workspace.
FDB_ADMIN="fdb -server ${FLEET_URL} -actor ${ACTOR}"
FDB_DATA="fdb -server ${FLEET_URL} -workspace ${FLEET_WORKSPACE} -actor ${ACTOR}"
echo "[seed] fdb admin base: ${FDB_ADMIN}"
echo "[seed] fdb data  base: ${FDB_DATA}"

# ──────────────────────────────────────────────────────────────────
# Phase 1: ensure fleet-db PARITY workspace exists (via fdb)
# Try create; accept "already_exists" as success. Simpler than trying
# to detect existence via `workspace show` (whose arg shape differs
# from the data commands).
# ──────────────────────────────────────────────────────────────────
echo "[seed] ensuring fleet-db workspace ${FLEET_WORKSPACE} exists..."
CREATE_OUT=$(${FDB_ADMIN} workspace create --key "${FLEET_WORKSPACE}" --name "Parity Fixture" 2>&1 || true)
case "${CREATE_OUT}" in
    *already_exists*|*already\ exists*)
        echo "[seed]   ≈ already exists (ok)" ;;
    *)
        # Any other output: print it for debugging but don't fail — the
        # data calls below will surface a real problem with a clearer error.
        echo "[seed]   create result: ${CREATE_OUT}" ;;
esac

# ──────────────────────────────────────────────────────────────────
# Phase 2: discover loom-beads workspace ID
# ──────────────────────────────────────────────────────────────────
BEADS_WS_ID=$(curl -fsS "${BEADS_URL}/api/workspaces" 2>/dev/null \
    | sed -n 's/.*"id":"\([^"]*\)".*/\1/p' | head -1)
if [ -z "${BEADS_WS_ID}" ]; then
    echo "[seed] FATAL: loom-beads has no workspaces"
    exit 2
fi
echo "[seed] loom-beads workspace ID: ${BEADS_WS_ID}"

# ──────────────────────────────────────────────────────────────────
# Phase 3: seed fixtures via fdb (fleet) + curl (loom-beads webui)
# ──────────────────────────────────────────────────────────────────
post_issue() {
    local title="$1"; local type="$2"; local priority="$3"

    echo "[seed] creating: $title ($type, P$priority)"

    # Loom-beads side via webui (loom-beads has bd internally; we go
    # through the webui to mirror the fleet path's "client → server" shape).
    body="{\"title\":\"$title\",\"issue_type\":\"$type\",\"priority\":$priority}"
    curl -fsS -X POST "${BEADS_URL}/api/workspaces/${BEADS_WS_ID}/issues" \
        -H 'Content-Type: application/json' \
        -d "$body" > /dev/null \
        || { echo "[seed]   FAILED on loom-beads"; exit 3; }

    # Fleet side via fdb CLI — this is the "real user CLI" path.
    ${FDB_DATA} create -title "$title" -type "$type" -priority "$priority" > /dev/null 2>&1 \
        || { echo "[seed]   FAILED on fleet-db (fdb)"; exit 4; }
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

echo "[seed] done — 13 issues seeded into both backends"

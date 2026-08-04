#!/bin/sh
# seed.sh — populate the fleet-db regression fixture set.
#
# Strategy:
#   - fleet-db side: use the `fdb` CLI directly (--server URL, --workspace
#     FLEETDB, --actor fleetdb-regression-harness). Tests the real user-facing CLI
#     path, not just the HTTP wire shape.
# Workspace bootstrap:
#   - fleet-db: `fdb workspace create FLEETDB` via fdb (admin API; fdb
#     forwards X-Actor as an authenticated identity in --auth-dev-mode)
#
# Environment inputs (set by docker-compose.regression.yml):
#   FLEET_URL        e.g. http://fleet-db:8080
#   FLEET_WORKSPACE  e.g. FLEETDB

set -eu

: "${FLEET_URL:?must be set}"
: "${FLEET_WORKSPACE:?must be set}"

ACTOR="fleetdb-regression-harness@fixture.local"

echo "[seed] FLEET_URL=${FLEET_URL}  workspace=${FLEET_WORKSPACE}  actor=${ACTOR}"

# Base invocations. `workspace create` needs --key (admin endpoint),
# but per-workspace data operations need -workspace.
FDB_ADMIN="fdb -server ${FLEET_URL} -actor ${ACTOR}"
FDB_DATA="fdb -server ${FLEET_URL} -workspace ${FLEET_WORKSPACE} -actor ${ACTOR}"
echo "[seed] fdb admin base: ${FDB_ADMIN}"
echo "[seed] fdb data  base: ${FDB_DATA}"

echo "[seed] waiting for fleet-db admin API..."
for i in $(seq 1 30); do
    if ${FDB_ADMIN} workspace list > /dev/null 2>&1; then
        break
    fi
    if [ "$i" = "30" ]; then
        echo "[seed] FATAL: fleet-db admin API did not become ready"
        exit 5
    fi
    sleep 1
done

# ──────────────────────────────────────────────────────────────────
# Phase 1: ensure fleet-db FLEETDB workspace exists (via fdb)
#
# When FLEETDB already exists from a prior compose run, drop and recreate
# it so the issue list starts at 0. Fleet-db's Redis state survives
# `podman-compose up` (the redis container is restarted but its Streams
# data is event-sourced and replayed at boot via fleet-db's own snapshot).
# ──────────────────────────────────────────────────────────────────
echo "[seed] resetting fleet-db workspace ${FLEET_WORKSPACE} for a clean seed..."
DELETE_OUT=$(${FDB_ADMIN} workspace delete "${FLEET_WORKSPACE}" --force 2>&1 || true)
case "${DELETE_OUT}" in
    *not_found*|*does\ not\ exist*|*"not found"*)
        echo "[seed]   ≈ no existing workspace to delete (first run)" ;;
    *)
        echo "[seed]   delete result: ${DELETE_OUT}" ;;
esac
CREATE_OUT=$(${FDB_ADMIN} workspace create --key "${FLEET_WORKSPACE}" --name "FleetDB Regression Fixture" 2>&1 || true)
case "${CREATE_OUT}" in
    *already_exists*|*already\ exists*)
        # Delete didn't take — proceed anyway; the data calls will
        # surface a real problem with a clearer error.
        echo "[seed]   ≈ already exists after delete (ok)" ;;
    *)
        echo "[seed]   create result: ${CREATE_OUT}" ;;
esac

# ──────────────────────────────────────────────────────────────────
# Phase 2: seed fixtures via fdb
# ──────────────────────────────────────────────────────────────────
post_issue() {
    local title="$1"; local type="$2"; local priority="$3"

    echo "[seed] creating: $title ($type, P$priority)"

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

echo "[seed] done — 13 issues seeded into fleet-db"

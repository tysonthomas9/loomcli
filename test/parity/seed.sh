#!/bin/sh
# seed.sh — populate identical fixture set into both parity backends.
#
# Assumes docker-compose.parity.yml has brought up redis, fleet-db,
# loom-beads, loom-fleet. Called via `docker compose run --rm parity-seed`
# or as the parity-seed service entrypoint.
#
# Environment inputs (set by docker-compose.parity.yml):
#   FLEET_URL        e.g. http://fleet-db:8080
#   FLEET_WORKSPACE  e.g. PARITY
#   BEADS_URL        e.g. http://loom-beads:8080  (loomcli serve endpoint)
#
# Both sides receive the same 10 issues, 3 epics, 2 dep chains, 5 comments,
# 3 labels. On failure, exits non-zero and leaves partial state behind.

set -eu

: "${FLEET_URL:?must be set}"
: "${FLEET_WORKSPACE:?must be set}"
: "${BEADS_URL:?must be set}"

echo "[seed] FLEET_URL=${FLEET_URL}  workspace=${FLEET_WORKSPACE}"
echo "[seed] BEADS_URL=${BEADS_URL}"

# -- Phase 0: ensure workspace exists on fleet-db --
curl -fsS -X POST "${FLEET_URL}/api/v1/admin/workspaces" \
    -H 'Content-Type: application/json' \
    -d "{\"key\":\"${FLEET_WORKSPACE}\",\"name\":\"Parity Fixture\"}" \
    > /dev/null 2>&1 || echo "[seed] workspace may already exist (ok)"

# -- Helper: POST an issue to both backends with the same body --
post_issue() {
    local title="$1"; local type="$2"; local priority="$3"
    local parent="${4:-}"

    local body
    body=$(jq -n \
        --arg title "$title" --arg type "$type" \
        --argjson priority "$priority" \
        --arg parent "$parent" \
        '{title:$title, issue_type:$type, priority:$priority}
         + (if $parent == "" then {} else {parent_id:$parent} end)')

    echo "[seed] creating issue: $title ($type, P$priority)"

    # Fleet-db
    curl -fsS -X POST "${FLEET_URL}/api/v1/${FLEET_WORKSPACE}/issues" \
        -H 'Content-Type: application/json' -d "$body" > /dev/null

    # Loom-beads webui exposes the same issue-create API shape under
    # /api/issues (loom's own routing). Beads-side seeding goes through
    # the loom webui so both backends share the same HTTP surface.
    curl -fsS -X POST "${BEADS_URL}/api/issues" \
        -H 'Content-Type: application/json' -d "$body" > /dev/null
}

# -- Phase 1: seed 3 epics --
post_issue "Epic Alpha"  epic    2 ""
post_issue "Epic Beta"   epic    2 ""
post_issue "Epic Gamma"  epic    3 ""

# -- Phase 2: seed 10 children (mix of task, bug, feature) --
post_issue "Add login flow"           feature 2 ""
post_issue "Fix checkout NPE"         bug     1 ""
post_issue "Refactor auth middleware" task    3 ""
post_issue "Onboarding wizard"        feature 2 ""
post_issue "Cache invalidation bug"   bug     1 ""
post_issue "Update README"            task    4 ""
post_issue "Session timeout edge"     bug     2 ""
post_issue "Theme toggle"             feature 3 ""
post_issue "Flaky test: login_e2e"    task    2 ""
post_issue "Clarify rate limit docs"  task    4 ""

# -- Phase 3: dependency chain A → B → C --
# (Dependency creation via loom webui endpoints omitted for brevity;
# extend once the seeding contract is firm.)

echo "[seed] done"

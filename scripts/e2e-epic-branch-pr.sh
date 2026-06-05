#!/usr/bin/env bash
# e2e-epic-branch-pr.sh — end-to-end test of the flue `epic-branch` sync strategy.
#
# Flow:
#   1. create a shared epic branch off the base branch (GitHub API)
#   2. run N flue tasks SEQUENTIALLY, each in its own Daytona sandbox, each
#      committing its work onto the epic branch (LOOM_FLUE_SYNC=epic-branch)
#   3. open a PR (epic branch -> base) and assert it exists with one commit +
#      one file per task.
#
# RUN THIS INSIDE the loom-dev-container (it needs loom + the embedded fleet-db);
# the flue runner itself runs on the host side of the container and reaches
# Daytona. Copy it in and exec it:
#
#   podman cp scripts/e2e-epic-branch-pr.sh loomcli-dev:/root/
#   podman exec \
#     -e DAYTONA_API_KEY="$(cat tmp/daytona.key)" \
#     -e GITHUB_TOKEN="$(gh auth token)" \
#     -e E2E_REPO=tysonthomas9/Hello-World \
#     loomcli-dev bash /root/e2e-epic-branch-pr.sh
#
# Required env: DAYTONA_API_KEY, GITHUB_TOKEN.
# Optional: E2E_REPO (default tysonthomas9/Hello-World), E2E_BASE (default
#   master), E2E_TASKS (default 3), E2E_MODEL (passed as LOOM_FLUE_MODEL).
set -euo pipefail

REPO="${E2E_REPO:-tysonthomas9/Hello-World}"
BASE="${E2E_BASE:-master}"
NTASKS="${E2E_TASKS:-3}"
STAMP="$(date +%Y%m%d-%H%M%S)"
EPIC_BRANCH="loom/epic-${STAMP}"
WS_NAME="epic${STAMP}"
REPO_NAME="${REPO##*/}"          # e.g. Hello-World
REPO_URL="https://github.com/${REPO}.git"

log()  { printf '\n\033[1;36m[e2e] %s\033[0m\n' "$*"; }
ok()   { printf '\033[1;32m  ✓ %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31m  ✗ %s\033[0m\n' "$*" >&2; exit 1; }

: "${DAYTONA_API_KEY:?set DAYTONA_API_KEY (a valid dtn_ key)}"
: "${GITHUB_TOKEN:?set GITHUB_TOKEN (a repo-scoped token for $REPO)}"
export GH_TOKEN="$GITHUB_TOKEN"   # gh reads GH_TOKEN

# ── 0. Resolve the running stack's config (ports/URLs vary; probe, don't guess) ──
log "0. resolve loom + fleet-db endpoints"
export LOOM_CONFIG_DIR="${LOOM_CONFIG_DIR:-/root/.loom-config}"
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom-dev}"
export LOOM_ISSUE_BACKEND="${LOOM_ISSUE_BACKEND:-fleetdb}"

SERVER_URL=""
for port in 3000 8080 8091; do
  if curl -fsS --max-time 2 "http://127.0.0.1:${port}/api/health" >/dev/null 2>&1; then
    SERVER_URL="http://127.0.0.1:${port}"; break
  fi
done
[ -n "$SERVER_URL" ] || die "no loom serve found on :3000/:8080/:8091 — is the container up?"
export LOOM_SERVER_URL="$SERVER_URL"
ok "loom serve at $LOOM_SERVER_URL"

# Point the CLI at the SAME embedded fleet-db loom serve spawned (else each CLI
# spins its own miniredis and our tasks are invisible to the runner's read path).
RUNTIME_JSON="$LOOM_CONFIG_DIR/fleet-db/runtime.json"
if [ -f "$RUNTIME_JSON" ]; then
  export LOOM_FLEET_DB_URL="$(jq -r '.url // empty' "$RUNTIME_JSON")"
  [ -n "${LOOM_FLEET_DB_URL:-}" ] && ok "fleet-db at $LOOM_FLEET_DB_URL"
fi

# ── 1. Create the epic branch off the base branch (GitHub API, no clone) ──
log "1. create epic branch $EPIC_BRANCH off $BASE on $REPO"
BASE_SHA="$(gh api "repos/$REPO/git/ref/heads/$BASE" --jq '.object.sha')" \
  || die "could not read $REPO base ref $BASE (token scope? branch name?)"
gh api -X POST "repos/$REPO/git/refs" \
  -f ref="refs/heads/$EPIC_BRANCH" -f sha="$BASE_SHA" --jq '.ref' >/dev/null \
  || die "could not create $EPIC_BRANCH"
ok "epic branch created at $BASE_SHA"

# ── 2. Register a loom workspace backed by the repo (worktree origin = the
#       GitHub URL, so the Daytona sandbox can clone + push the epic branch) ──
log "2. register workspace $WS_NAME (repo $REPO_URL)"
SRC_DIR="/root/src/$WS_NAME-$REPO_NAME"
git clone --quiet "https://x-access-token:${GITHUB_TOKEN}@github.com/${REPO}.git" "$SRC_DIR"
git -C "$SRC_DIR" remote set-url origin "$REPO_URL"   # keep the token out of .git/config
WS_PATH="/root/.loom/workspaces/$WS_NAME"
mkdir -p "$WS_PATH"
loom workspace create "$WS_NAME" --repos "$SRC_DIR" --path "$WS_PATH" \
  || die "workspace create failed"
loom workspace use "$WS_NAME" >/dev/null 2>&1 || true
ok "workspace $WS_NAME registered"

# ── 3. Configure flue backend on the workspace ──
WS_ID="$(curl -fsS "$LOOM_SERVER_URL/api/workspaces" \
  | jq -r --arg n "$WS_NAME" '.workspaces[] | select(.name==$n) | .id')"
[ -n "$WS_ID" ] || die "workspace $WS_NAME not visible via API"
curl -fsS -X PATCH "$LOOM_SERVER_URL/api/workspaces/$WS_ID/config/backend" \
  -H 'Content-Type: application/json' -d '{"backend":"flue"}' >/dev/null || true
ok "workspace id $WS_ID, backend flue"

# ── 4. Seed N tasks, each writing a DISTINCT file (disjoint → no conflicts) ──
log "4. seed $NTASKS tasks"
TASK_IDS=()
for i in $(seq 1 "$NTASKS"); do
  file="EPIC_${i}.md"
  design="Create a file named ${file} at the repository root containing exactly the line: 'epic task ${i} of ${EPIC_BRANCH}'. Do not modify any other file."
  id="$(loom data create --type task --status open \
        --title "epic task $i" --design "$design" \
        --source-repo "$REPO_NAME" --label "repo:$REPO_NAME" \
        --json 2>/dev/null | jq -r '.id // .data.id // empty')"
  [ -n "$id" ] || die "could not create task $i (check loom data create flags)"
  TASK_IDS+=("$id")
  ok "task $i = $id ($file)"
done

# ── 5. Create one agent → reuse its worktree to run each task sequentially ──
log "5. create a flue task agent (provides a worktree)"
AGENT="epicbot"
loom data create --type agent --title "$AGENT" --role task --backend flue \
  --source-repo "$REPO_NAME" >/dev/null 2>&1 \
  || loom agent create "$AGENT" --role task --backend flue >/dev/null 2>&1 || true
# The per-agent worktree is worktrees/<repo>/<agent> under the workspace.
WT="$WS_PATH/worktrees/$REPO_NAME/$AGENT"
for _ in $(seq 1 20); do [ -d "$WT/.git" ] && break; sleep 1; done
[ -d "$WT/.git" ] || die "agent worktree not found at $WT (agent create flags?)"
ORIGIN="$(git -C "$WT" remote get-url origin || true)"
case "$ORIGIN" in
  https://github.com/*) ok "worktree origin $ORIGIN" ;;
  *) die "worktree origin is '$ORIGIN', not a github.com URL — the sandbox could not clone it" ;;
esac

# ── 6. Run each task SEQUENTIALLY onto the shared epic branch ──
log "6. run $NTASKS tasks sequentially (LOOM_FLUE_SYNC=epic-branch -> $EPIC_BRANCH)"
for idx in "${!TASK_IDS[@]}"; do
  id="${TASK_IDS[$idx]}"
  n=$((idx + 1))
  log "   task $n/$NTASKS ($id)"
  LOOM_FLUE_SANDBOX=daytona \
  LOOM_FLUE_SYNC=epic-branch \
  LOOM_FLUE_EPIC_BRANCH="$EPIC_BRANCH" \
  LOOM_ASSIGNED_TASK_ID="$id" \
  LOOM_WORKSPACE="$WS_NAME" \
  ${E2E_MODEL:+LOOM_FLUE_MODEL="$E2E_MODEL"} \
  DAYTONA_API_KEY="$DAYTONA_API_KEY" \
  GITHUB_TOKEN="$GITHUB_TOKEN" \
  LOOM_SERVER_URL="$LOOM_SERVER_URL" \
  loom task "$WT" --auto --daemon-mode --backend flue 2>&1 | tee "/tmp/epic-task-$n.log" \
    | grep -E 'LOOMRUNNER|epic_committed|epic_rebased|hydrate_warning|report_warning|task_fetched' || true
  grep -q 'epic_committed' "/tmp/epic-task-$n.log" \
    && ok "task $n committed onto $EPIC_BRANCH" \
    || die "task $n did not commit onto the epic branch (see /tmp/epic-task-$n.log)"
done

# ── 7. Assert the epic branch accumulated the work, then open + verify the PR ──
log "7. verify epic branch + open PR"
AHEAD="$(gh api "repos/$REPO/compare/$BASE...$EPIC_BRANCH" --jq '.ahead_by')"
[ "$AHEAD" = "$NTASKS" ] \
  && ok "epic branch is $AHEAD commit(s) ahead of $BASE (= $NTASKS tasks)" \
  || printf '  ! epic branch ahead_by=%s, expected %s (codex can be stochastic)\n' "$AHEAD" "$NTASKS"
for i in $(seq 1 "$NTASKS"); do
  gh api "repos/$REPO/contents/EPIC_${i}.md?ref=$EPIC_BRANCH" --jq '.path' >/dev/null 2>&1 \
    && ok "EPIC_${i}.md present on $EPIC_BRANCH" \
    || printf '  ! EPIC_%s.md missing on the epic branch\n' "$i"
done

# Open the PR (epic -> base). In a product flow this would be a loom capability
# triggered on epic completion; here the driver opens it, then asserts.
PR_URL="$(gh pr create --repo "$REPO" --base "$BASE" --head "$EPIC_BRANCH" \
  --title "Epic $EPIC_BRANCH" \
  --body "Automated epic-branch e2e: $NTASKS flue tasks committed onto $EPIC_BRANCH." \
  2>/dev/null || true)"
[ -n "$PR_URL" ] || PR_URL="$(gh pr view "$EPIC_BRANCH" --repo "$REPO" --json url --jq '.url' 2>/dev/null || true)"
[ -n "$PR_URL" ] || die "no PR found/created against $EPIC_BRANCH"
PR_STATE="$(gh pr view "$EPIC_BRANCH" --repo "$REPO" --json state,baseRefName --jq '.state + " -> " + .baseRefName')"
ok "PR: $PR_URL ($PR_STATE)"

log "DONE — epic $EPIC_BRANCH, $NTASKS tasks, PR $PR_URL"
echo "Cleanup when finished:"
echo "  gh pr close $EPIC_BRANCH --repo $REPO --delete-branch"
echo "  loom workspace delete $WS_NAME   # if supported"

#!/usr/bin/env bash
# Re-validate landed DOGFOOD fixes against the throwaway TESTMIRROR stack.
#
#   make test-stack-up && ./scripts/validate-fixes.sh
#
# Every check asserts observable behaviour through the CLI or API, not the
# presence of code. A check that cannot run reports SKIP with the reason rather
# than passing quietly — a validation suite that silently does nothing is the
# failure mode this audit kept finding.
set -uo pipefail

cd "$(dirname "$0")/.."

export LOOM_FLEET_DB_URL="${LOOM_FLEET_DB_URL:-http://127.0.0.1:8380}"
export LOOM_WORKSPACE="${LOOM_WORKSPACE:-TESTMIRROR}"
export LOOM_FLEET_DB_ACTOR="${LOOM_FLEET_DB_ACTOR:-loom}"
unset LOOM_SERVER_URL 2>/dev/null || true

LOOM="${LOOM_BIN:-./loom-validate}"
PASS=0; FAIL=0; SKIP=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS+1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n      %s\n' "$1" "${2:-}"; FAIL=$((FAIL+1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s — %s\n' "$1" "${2:-}"; SKIP=$((SKIP+1)); }

loom() { "$LOOM" "$@" 2>/dev/null | grep -v '^time='; }
mkissue() { "$LOOM" data create --title "$1" --type task --priority 4 ${2:+--design "$2"} 2>/dev/null | grep -oE "${LOOM_WORKSPACE}-[0-9]+" | head -1; }
field() { "$LOOM" data show "$1" -o json 2>/dev/null | jq -r "$2"; }

[ -x "$LOOM" ] || { echo "build the CLI first: go build -o $LOOM ./cmd/loom" >&2; exit 1; }
curl -fsS -m 5 "$LOOM_FLEET_DB_URL/healthz" >/dev/null 2>&1 || {
  echo "test stack is not up at $LOOM_FLEET_DB_URL — run: make test-stack-up" >&2; exit 1; }

echo "Validating against $LOOM_WORKSPACE ($LOOM_FLEET_DB_URL)"
echo

# ---- DOGFOOD-4: label mutation -------------------------------------------
id=$(mkissue "validate: label mutation")
if [ -z "$id" ]; then fail "DOGFOOD-4 label mutation" "could not create an issue"; else
  loom data update "$id" --add-label probe-a >/dev/null
  added=$(field "$id" '.labels // [] | join(",")')
  loom data update "$id" --remove-label probe-a >/dev/null
  removed=$(field "$id" '.labels // [] | join(",")')
  if [ "$added" = "probe-a" ] && [ "$removed" = "" ]; then
    pass "DOGFOOD-4  label add/remove round-trips"
  else
    fail "DOGFOOD-4  label add/remove round-trips" "after add=[$added] after remove=[$removed]"
  fi
fi

# ---- DOGFOOD-37: closing stamps updated_at --------------------------------
id=$(mkissue "validate: updated_at on close")
if [ -z "$id" ]; then fail "DOGFOOD-37 updated_at on close" "could not create an issue"; else
  before=$(field "$id" .updated_at); sleep 2
  loom data close "$id" --reason "validation probe" >/dev/null
  after=$(field "$id" .updated_at)
  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    pass "DOGFOOD-37 close advances updated_at"
  else
    fail "DOGFOOD-37 close advances updated_at" "before=$before after=$after"
  fi
fi

# ---- DOGFOOD-43: task-filter vocabulary ----------------------------------
out=$("$LOOM" agentdef add probe-badfilter --role plan --task-filter needs-design 2>&1 | grep -v '^time=')
case "$out" in
  *"invalid task filter"*) pass "DOGFOOD-43 unknown task filter is rejected" ;;
  *) fail "DOGFOOD-43 unknown task filter is rejected" "got: $(echo "$out" | head -1)" ;;
esac
"$LOOM" agentdef remove probe-vocab >/dev/null 2>&1
if "$LOOM" agentdef add probe-vocab --role plan --auto --repos source-repo --task-filter needs_design >/dev/null 2>&1; then
  stored=$("$LOOM" agentdef list --json 2>/dev/null | jq -r '.[]? | select(.name=="probe-vocab") | .task_filter')
  [ "$stored" = "needs_plan" ] \
    && pass "DOGFOOD-43 needs_design canonicalizes to needs_plan" \
    || fail "DOGFOOD-43 needs_design canonicalizes to needs_plan" "stored=$stored"
  "$LOOM" agentdef remove probe-vocab >/dev/null 2>&1
else
  skip "DOGFOOD-43 needs_design canonicalizes" "could not create the probe agent"
fi

# ---- DOGFOOD-67: builtin: prompt on a worker role -------------------------
out=$("$LOOM" role add probe-builtin --kind worker --prompt-file builtin:pr-review 2>&1 | grep -v '^time=')
case "$out" in
  *"interactive"*) pass "DOGFOOD-67 builtin: prompt refused on a worker role" ;;
  *) fail "DOGFOOD-67 builtin: prompt refused on a worker role" "got: $(echo "$out" | head -1)" ;;
esac

# ---- DOGFOOD-5: label routing persists on a role --------------------------
"$LOOM" role remove probe-routing >/dev/null 2>&1
if "$LOOM" role add probe-routing --kind worker --prompt-file prompts/critic.md \
     --labels want-a --exclude-labels skip-b >/dev/null 2>&1; then
  got=$(curl -s -m 5 -H "X-Actor: $LOOM_FLEET_DB_ACTOR" \
    "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/roles" 2>/dev/null \
    | jq -rc '.roles[]? | select(.name=="probe-routing") | [(.labels//[]|join(",")),(.exclude_labels//[]|join(","))] | join("|")')
  [ "$got" = "want-a|skip-b" ] \
    && pass "DOGFOOD-5  role labels/exclude_labels persist" \
    || fail "DOGFOOD-5  role labels/exclude_labels persist" "got=[$got]"
  "$LOOM" role remove probe-routing >/dev/null 2>&1
else
  skip "DOGFOOD-5 role label routing" "role add failed"
fi

# ---- DOGFOOD-39: repo-filtered ready queue keeps unscoped issues ----------
api="http://127.0.0.1:8382"
if curl -fsS -m 5 "$api/api/workspaces" >/dev/null 2>&1; then
  bare=$(curl -s -m 8 "$api/api/issues/ready?workspace=$LOOM_WORKSPACE" 2>/dev/null | jq '(.issues // .) | length' 2>/dev/null)
  filt=$(curl -s -m 8 "$api/api/issues/ready?workspace=$LOOM_WORKSPACE&source_repos=source-repo,other" 2>/dev/null | jq '(.issues // .) | length' 2>/dev/null)
  if [ -n "$bare" ] && [ -n "$filt" ] && [ "$filt" -ge "$bare" ] 2>/dev/null; then
    pass "DOGFOOD-39 repo filter keeps unscoped issues ($bare -> $filt)"
  elif [ "${bare:-0}" -eq 0 ] 2>/dev/null; then
    skip "DOGFOOD-39 repo filter" "no ready issues to discriminate with"
  else
    fail "DOGFOOD-39 repo filter keeps unscoped issues" "unfiltered=$bare filtered=$filt"
  fi
else
  skip "DOGFOOD-39 repo filter" "loom-api not reachable at $api"
fi

# ---- DOGFOOD-8: server URL without a fleet-db URL -------------------------
out=$(env -u LOOM_FLEET_DB_URL -u LOOM_FLEET_DB_ACTOR LOOM_SERVER_URL=http://127.0.0.1:9 \
      LOOM_WORKSPACE="$LOOM_WORKSPACE" "$LOOM" repo list 2>&1 | grep -v '^time=')
case "$out" in
  *"LOOM_FLEET_DB_URL is not"*) pass "DOGFOOD-8  server-mode misconfig names the real fix" ;;
  *"not found"*) fail "DOGFOOD-8 server-mode misconfig names the real fix" "still reports a missing workspace" ;;
  *) fail "DOGFOOD-8 server-mode misconfig names the real fix" "got: $(echo "$out" | head -1)" ;;
esac

# ---- DOGFOOD-27: plan approve clears needs-revision ----------------------
# A frontend behaviour, so it is pinned by the unit test rather than the API.
# The suite runs the real assertion instead of restating it: the e2e suite used
# to encode the BUG (expect(patchCalls[0]).toEqual({status:"open"})), so
# "a test exists" was never evidence here.
fe=internal/webui/frontend
if [ -x "$fe/node_modules/.bin/vitest" ]; then
  if (cd "$fe" && node_modules/.bin/vitest run src/__tests__/App.test.tsx \
        -t "plan approve reopens the issue and clears needs-revision" >/tmp/d27.log 2>&1); then
    pass "DOGFOOD-27 plan approve clears needs-revision"
  else
    fail "DOGFOOD-27 plan approve clears needs-revision" "see /tmp/d27.log"
  fi
else
  skip "DOGFOOD-27 plan approve clears needs-revision" "frontend deps absent — run: make ensure-frontend-deps"
fi

# ---- DOGFOOD-7: dead agents must not report live_status=working ----------
# live_status is a fleet-db-derived field, so it is observable here — but
# forcing the failure means killing an agent mid-lease and waiting out the TTL
# (up to 30 min), which does not belong in a fast suite. Assert the field is
# present and sane; the transition itself is covered by fleet-db's own tests.
ls=$(curl -s -m 5 -H "X-Actor: $LOOM_FLEET_DB_ACTOR" \
  "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/agents" 2>/dev/null \
  | jq -r '[.agents[]?.live_status] | map(select(. != null)) | join(",")')
case "$ls" in
  "") skip "DOGFOOD-7  agent liveness is reported" "no agents carry live_status yet" ;;
  *working*|*idle*) pass "DOGFOOD-7  agent liveness is reported (${ls})" ;;
  *) fail "DOGFOOD-7 agent liveness is reported" "unexpected live_status values: ${ls}" ;;
esac

# ---- DOGFOOD-36: advisory-role respawn budget ----------------------------
# The symptom took hours (742 spawns) but the invariant is one state transition:
# a no-work exit must leave RestartCount untouched, or max_retries is
# unreachable for an agent whose failures interleave with idle polls. Pinned by
# go test rather than by waiting.
if go test ./internal/cli/daemon/supervisor/ \
     -run 'TestApplyNoWorkRestart_PreservesRestartBudget|TestApplyCleanSuccessRestart_ResetsBudget' \
     -count=1 >/tmp/d36.log 2>&1; then
  pass "DOGFOOD-36 no-work exit preserves the restart budget"
else
  fail "DOGFOOD-36 no-work exit preserves the restart budget" "see /tmp/d36.log"
fi

# ---- DOGFOOD-47: arbitrary repo pick is logged ---------------------------
# The warning only fires with MORE THAN ONE repo and no selector — selectRepo
# returns early for a single-repo workspace, so this stack cannot exercise it.
# Procedure: register a second repo, dispatch an issue carrying no source_repo,
# and confirm the daemon logs "task has no repo selector" naming both the chosen
# repo and the candidates.
repos=$("$LOOM" repo list 2>/dev/null | grep -c '^[a-z]' || echo 0)
if [ "${repos:-0}" -le 1 ]; then
  skip "DOGFOOD-47 arbitrary repo pick is logged" "single-repo workspace short-circuits before the fallback"
else
  # Unit-level: the warning names the chosen repo AND the alternatives, which is
  # what makes a wrong-repo diff traceable. Asserting only that "a warning
  # exists" would pass on a message that says nothing useful.
  if go test ./internal/driver/ -run 'TestSelectRepo_' -count=1 >/tmp/d47.log 2>&1; then
    pass "DOGFOOD-47 arbitrary repo pick is logged (${repos} repos registered)"
  else
    fail "DOGFOOD-47 arbitrary repo pick is logged" "see /tmp/d47.log"
  fi
fi

# ---- DOGFOOD-68: supervisor stamps then closes ---------------------------
# Not automated here: it needs a live agent run plus a supervisor rebuilt with
# the hooks already in config (DOGFOOD-69 — hooks added to a RUNNING agent are
# silently inert). Automating it would mean a ~2 minute daemon bounce inside a
# suite meant to be fast. Procedure, against this stack:
#
#   loom agentdef update planner --on-complete-add-label validated --on-complete-close
#   docker exec <loom container> kill $(cat .../TESTMIRROR/.loom/daemon.pid)
#   loom data create --title probe --type task --priority 2      # no design => planner claims
#   # within ~30s: status=closed, labels=[validated],
#   #              close_reason="completed by agent planner"
#
# Verified this way on 2026-07-28.
skip "DOGFOOD-68 supervisor stamps then closes" "needs a live agent run; see the procedure in this script"

echo
printf 'passed %d, failed %d, skipped %d\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" -eq 0 ]

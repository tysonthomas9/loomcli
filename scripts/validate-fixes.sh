#!/usr/bin/env bash
# Re-validate landed fixes against the throwaway TESTMIRROR stack.
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

# Refuse to run anywhere but a known throwaway stack. Those defaults are only
# defaults: the suite honours ambient LOOM_FLEET_DB_URL/LOOM_WORKSPACE, so a
# shell already pointed at a real deployment would get probe issues written into
# it — permanently, because issues have no delete endpoint — plus agentdef and
# role mutations. Same two axes the throwaway stacks isolate on: a loopback URL
# and a throwaway workspace key. VALIDATE_ALLOW_TARGET=1 is the deliberate
# override for anyone who really means it.
if [ "${VALIDATE_ALLOW_TARGET:-0}" != "1" ]; then
  refuse=""
  case "$LOOM_WORKSPACE" in
    TESTMIRROR|LOOMTEST) ;;
    *) refuse="workspace $LOOM_WORKSPACE is not a throwaway workspace" ;;
  esac
  case "$LOOM_FLEET_DB_URL" in
    http://127.0.0.1:*|http://localhost:*) ;;
    *) refuse="${refuse:+$refuse; }$LOOM_FLEET_DB_URL is not a loopback address" ;;
  esac
  if [ -n "$refuse" ]; then
    echo "refusing to run: $refuse" >&2
    echo "This suite writes permanent probe issues and mutates agentdefs and roles." >&2
    echo "Point it at the throwaway stack (make test-stack-up), or set" >&2
    echo "VALIDATE_ALLOW_TARGET=1 if you really mean this target." >&2
    exit 1
  fi
fi

LOOM="${LOOM_BIN:-./loom-validate}"
PASS=0; FAIL=0; SKIP=0

pass() { printf '  \033[32mPASS\033[0m  %s\n' "$1"; PASS=$((PASS+1)); }
fail() { printf '  \033[31mFAIL\033[0m  %s\n      %s\n' "$1" "${2:-}"; FAIL=$((FAIL+1)); }
skip() { printf '  \033[33mSKIP\033[0m  %s — %s\n' "$1" "${2:-}"; SKIP=$((SKIP+1)); }

# go_test_pinned <label> <package> <run-pattern> <logfile>
#
# `go test -run PATTERN` exits 0 with "[no tests to run]" when PATTERN matches
# nothing, so a check pinned to a test that is not in this tree reports PASS
# with zero evidence — the "silently does nothing" failure this suite exists to
# refuse. Enumerate first with `go test -list`: no match means the check is not
# armed, which is a FAIL, not a quiet pass.
go_test_pinned() {
  local label=$1 pkg=$2 pattern=$3 log=$4 listed
  listed=$(go test -list "$pattern" "$pkg" 2>"$log" | grep -cE '^Test' || true)
  if [ "${listed:-0}" -eq 0 ]; then
    fail "$label" "no test matches /$pattern/ in $pkg — the check is not armed (see $log)"
    return
  fi
  if go test "$pkg" -run "$pattern" -count=1 >"$log" 2>&1; then
    pass "$label (${listed} pinned test(s))"
  else
    fail "$label" "see $log"
  fi
}

loom() { "$LOOM" "$@" 2>/dev/null | grep -v '^time='; }
mkissue() { "$LOOM" data create --title "$1" --type task --priority 4 ${2:+--design "$2"} 2>/dev/null | grep -oE "${LOOM_WORKSPACE}-[0-9]+" | head -1; }
field() { "$LOOM" data show "$1" -o json 2>/dev/null | jq -r "$2"; }

[ -x "$LOOM" ] || { echo "build the CLI first: go build -o $LOOM ./cmd/loom" >&2; exit 1; }
# jq is load-bearing: without it every field()/API read comes back empty, which
# reads as a plausible SKIP reason rather than a missing tool.
command -v jq >/dev/null 2>&1 || { echo "jq is required (brew install jq)" >&2; exit 1; }
curl -fsS -m 5 "$LOOM_FLEET_DB_URL/healthz" >/dev/null 2>&1 || {
  echo "test stack is not up at $LOOM_FLEET_DB_URL — run: make test-stack-up" >&2; exit 1; }

echo "Validating against $LOOM_WORKSPACE ($LOOM_FLEET_DB_URL)"
echo

# ---- label mutation -------------------------------------------
id=$(mkissue "validate: label mutation")
if [ -z "$id" ]; then fail "label mutation" "could not create an issue"; else
  loom data update "$id" --add-label probe-a >/dev/null
  added=$(field "$id" '.labels // [] | join(",")')
  loom data update "$id" --remove-label probe-a >/dev/null
  removed=$(field "$id" '.labels // [] | join(",")')
  if [ "$added" = "probe-a" ] && [ "$removed" = "" ]; then
    pass "label add/remove round-trips"
  else
    fail "label add/remove round-trips" "after add=[$added] after remove=[$removed]"
  fi
fi

# ---- closing stamps updated_at --------------------------------
id=$(mkissue "validate: updated_at on close")
if [ -z "$id" ]; then fail "updated_at on close" "could not create an issue"; else
  before=$(field "$id" .updated_at); sleep 2
  loom data close "$id" --reason "validation probe" >/dev/null
  after=$(field "$id" .updated_at)
  if [ -n "$before" ] && [ "$before" != "$after" ]; then
    pass "close advances updated_at"
  else
    fail "close advances updated_at" "before=$before after=$after"
  fi
fi

# ---- task-filter vocabulary ----------------------------------
out=$("$LOOM" agentdef add probe-badfilter --role plan --task-filter needs-design 2>&1 | grep -v '^time=')
case "$out" in
  *"invalid task filter"*) pass "unknown task filter is rejected" ;;
  *) fail "unknown task filter is rejected" "got: $(echo "$out" | head -1)" ;;
esac
"$LOOM" agentdef remove probe-vocab >/dev/null 2>&1
if "$LOOM" agentdef add probe-vocab --role plan --auto --repos source-repo --task-filter needs_design >/dev/null 2>&1; then
  stored=$("$LOOM" agentdef list --json 2>/dev/null | jq -r '.[]? | select(.name=="probe-vocab") | .task_filter')
  [ "$stored" = "needs_plan" ] \
    && pass "needs_design canonicalizes to needs_plan" \
    || fail "needs_design canonicalizes to needs_plan" "stored=$stored"
  "$LOOM" agentdef remove probe-vocab >/dev/null 2>&1
else
  skip "needs_design canonicalizes" "could not create the probe agent"
fi

# ---- builtin: prompt on a worker role -------------------------
out=$("$LOOM" role add probe-builtin --kind worker --prompt-file builtin:pr-review 2>&1 | grep -v '^time=')
case "$out" in
  *"interactive"*) pass "builtin: prompt refused on a worker role" ;;
  *) fail "builtin: prompt refused on a worker role" "got: $(echo "$out" | head -1)" ;;
esac

# ---- label routing persists on a role --------------------------
"$LOOM" role remove probe-routing >/dev/null 2>&1
if "$LOOM" role add probe-routing --kind worker --prompt-file prompts/critic.md \
     --labels want-a --exclude-labels skip-b >/dev/null 2>&1; then
  got=$(curl -s -m 5 -H "X-Actor: $LOOM_FLEET_DB_ACTOR" \
    "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/roles" 2>/dev/null \
    | jq -rc '.roles[]? | select(.name=="probe-routing") | [(.labels//[]|join(",")),(.exclude_labels//[]|join(","))] | join("|")')
  [ "$got" = "want-a|skip-b" ] \
    && pass "role labels/exclude_labels persist" \
    || fail "role labels/exclude_labels persist" "got=[$got]"
  "$LOOM" role remove probe-routing >/dev/null 2>&1
else
  skip "role label routing" "role add failed"
fi

# ---- repo-filtered ready queue keeps unscoped issues ----------
# Comparing unfiltered and filtered COUNTS does not discriminate: on a stack
# whose ready queue holds one issue scoped to source-repo, a filter naming
# source-repo returns 1 -> 1 and the check passes without ever exercising the
# fallback. Seed an issue with NO source_repo, filter on a repo name that
# matches nothing, and assert that specific issue survives — which is exactly
# the behaviour ("an empty source_repo is not a mismatch") being guarded.
api="http://127.0.0.1:8382"
ready_ids() { # <query-suffix>
  curl -s -m 8 "$api/api/issues/ready?workspace=$LOOM_WORKSPACE${1:-}" 2>/dev/null \
    | jq -r '(.issues // .)[]? | .id' 2>/dev/null
}
if ! curl -fsS -m 5 "$api/api/workspaces" >/dev/null 2>&1; then
  skip "repo filter" "loom-api not reachable at $api"
else
  probe=$(mkissue "validate: unscoped repo-filter probe")
  scope=$([ -n "$probe" ] && field "$probe" '.source_repo // ""')
  if [ -z "$probe" ]; then
    skip "repo filter" "could not create the unscoped probe issue"
  elif [ -n "$scope" ]; then
    skip "repo filter" "probe $probe came back scoped to '$scope' — nothing unscoped to test with"
  elif ! ready_ids | grep -qx "$probe"; then
    skip "repo filter" "probe $probe never reached the ready queue (claimed or not ready)"
  elif ready_ids "&source_repos=validate-no-such-repo" | grep -qx "$probe"; then
    pass "repo filter keeps unscoped issues ($probe survives a non-matching filter)"
  else
    fail "repo filter keeps unscoped issues" \
      "$probe is in the unfiltered ready queue but dropped by source_repos=validate-no-such-repo"
  fi
fi

# ---- server URL without a fleet-db URL -------------------------
out=$(env -u LOOM_FLEET_DB_URL -u LOOM_FLEET_DB_ACTOR LOOM_SERVER_URL=http://127.0.0.1:9 \
      LOOM_WORKSPACE="$LOOM_WORKSPACE" "$LOOM" repo list 2>&1 | grep -v '^time=')
case "$out" in
  *"LOOM_FLEET_DB_URL is not"*) pass "server-mode misconfig names the real fix" ;;
  *"not found"*) fail "server-mode misconfig names the real fix" "still reports a missing workspace" ;;
  *) fail "server-mode misconfig names the real fix" "got: $(echo "$out" | head -1)" ;;
esac

# ---- plan approve clears needs-revision ----------------------
# A frontend behaviour, so it is pinned by the unit test rather than the API.
# The suite runs the real assertion instead of restating it: the e2e suite used
# to encode the BUG (expect(patchCalls[0]).toEqual({status:"open"})), so
# "a test exists" was never evidence here.
fe=internal/webui/frontend
spec=src/__tests__/App.test.tsx
tname="plan approve reopens the issue and clears needs-revision"
# Armed-ness is a property of the tree, so it is checked BEFORE the toolchain:
# `vitest -t` with a name that matches nothing must not be reachable via the
# deps-absent skip. A pinned test that does not exist is a FAIL.
if ! grep -qF "$tname" "$fe/$spec" 2>/dev/null; then
  fail "plan approve clears needs-revision" \
    "no test named \"$tname\" in $fe/$spec — the check is not armed"
elif [ ! -x "$fe/node_modules/.bin/vitest" ]; then
  skip "plan approve clears needs-revision" "frontend deps absent — run: make ensure-frontend-deps"
elif (cd "$fe" && node_modules/.bin/vitest run "$spec" -t "$tname" >/tmp/validate-plan-approve.log 2>&1); then
  pass "plan approve clears needs-revision"
else
  fail "plan approve clears needs-revision" "see /tmp/validate-plan-approve.log"
fi

# ---- dead agents must not report live_status=working ----------
# live_status is a fleet-db-derived field, so it is observable here — but
# forcing the failure means killing an agent mid-lease and waiting out the TTL
# (up to 30 min), which does not belong in a fast suite. Assert the field is
# present and sane; the transition itself is covered by fleet-db's own tests.
ls=$(curl -s -m 5 -H "X-Actor: $LOOM_FLEET_DB_ACTOR" \
  "$LOOM_FLEET_DB_URL/api/v1/$LOOM_WORKSPACE/agents" 2>/dev/null \
  | jq -r '[.agents[]?.live_status] | map(select(. != null)) | join(",")')
case "$ls" in
  "") skip "agent liveness is reported" "no agents carry live_status yet" ;;
  *working*|*idle*) pass "agent liveness is reported (${ls})" ;;
  *) fail "agent liveness is reported" "unexpected live_status values: ${ls}" ;;
esac

# ---- advisory-role respawn budget ----------------------------
# The symptom took hours (742 spawns) but the invariant is one state transition:
# a no-work exit must leave RestartCount untouched, or max_retries is
# unreachable for an agent whose failures interleave with idle polls. Pinned by
# go test rather than by waiting.
go_test_pinned "no-work exit preserves the restart budget" \
  ./internal/cli/daemon/supervisor/ \
  'TestApplyNoWorkRestart_PreservesRestartBudget|TestApplyCleanSuccessRestart_ResetsBudget' \
  /tmp/validate-respawn-budget.log

# ---- arbitrary repo pick is logged ---------------------------
# The warning only fires with MORE THAN ONE repo and no selector — selectRepo
# returns early for a single-repo workspace, so this stack cannot exercise it.
# Procedure: register a second repo, dispatch an issue carrying no source_repo,
# and confirm the daemon logs "task has no repo selector" naming both the chosen
# repo and the candidates.
# `grep -c` prints the count AND exits 1 when it is zero, so `|| echo 0` used to
# append a second line and make $repos two lines — the arithmetic test below
# then errored and fell into the else branch instead of skipping.
repos=$("$LOOM" repo list 2>/dev/null | grep -c '^[a-z]' || true)
if [ "${repos:-0}" -le 1 ]; then
  skip "arbitrary repo pick is logged" "single-repo workspace short-circuits before the fallback"
else
  # Unit-level: the warning names the chosen repo AND the alternatives, which is
  # what makes a wrong-repo diff traceable. Asserting only that "a warning
  # exists" would pass on a message that says nothing useful.
  go_test_pinned "arbitrary repo pick is logged (${repos} repos registered)" \
    ./internal/driver/ 'TestSelectRepo_' /tmp/validate-repo-fallback.log
fi

# ---- supervisor stamps then closes ---------------------------
# Not automated here: it needs a live agent run plus a supervisor rebuilt with
# the hooks already in config (— hooks added to a RUNNING agent are
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
skip "supervisor stamps then closes" "needs a live agent run; see the procedure in this script"

echo
printf 'passed %d, failed %d, skipped %d\n' "$PASS" "$FAIL" "$SKIP"
[ "$FAIL" -eq 0 ]

#!/usr/bin/env bash
# P1.25 (D29 4): a task whose agent changed nothing closes as "No changes":
# no review, no apply, no PR. Real TaskRuns record the revisions (the
# deterministic Codex fixture writes a file only when the design asks), the
# product routes refuse every verdict and publication, and a later attempt
# with changes is reviewed, applied and published normally.
set -euo pipefail

phase="$1"
workspace="$2"
api="$AFT_BASE_URL/api/workspaces/$workspace"
lower="$(printf '%s' "$workspace" | tr '[:upper:]' '[:lower:]')"
repo="$AFT_WORK_DIR/$lower-repo"
remote="$AFT_WORK_DIR/$lower-origin.git"
lead_ref="refs/heads/loom/ws/$workspace/interactive/lead"
work="$AFT_WORK_DIR/$workspace"

newest() { python3 -c 'import json,sys; r=json.load(open(sys.argv[1]))["data"][0]; print(r["change_id"], r["number"], r["head_sha"])' "$1"; }
revisions() { curl -fsS "$api/issues/$(cat "$work-task")/revisions" > "$1"; }
pulls() { curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))'; }

wait_revisions() {
  # wait_revisions <count>: the task's TaskRun recorded <count> revisions.
  for _ in $(seq 1 60); do
    revisions "$work-revisions.json"
    python3 -c 'import json,sys; sys.exit(0 if len(json.load(open(sys.argv[1]))["data"]) >= int(sys.argv[2]) else 1)' \
      "$work-revisions.json" "$1" && return 0
    sleep 2
  done
  echo "the task recorded fewer than $1 revisions:" >&2
  cat "$work-revisions.json" >&2
  return 1
}

run_epic() {
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$(cat "$work-epic")\",\"runner\":\"local-task-runner\"}" >/dev/null
}

case "$phase" in
setup)
  # setup <ws> <stack|trunk>: a repo with a fake-forge origin and a workspace
  # in the given delivery mode.
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$repo"
  printf 'base\n' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
  git -C "$repo" remote add origin git@github.com:owner/repo.git
  git -C "$repo" push -q origin main
  git -C "$repo" rev-parse main > "$work-trunk"
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$lower\",\"type\":\"empty\",\"repos\":[\"$repo\"]}" >/dev/null
  if [[ "${3:-stack}" == trunk ]]; then
    LOOM_WORKSPACE="$workspace" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" delivery-mode trunk --workspace "$workspace" >/dev/null
  fi
  ;;
teardown)
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh"
  curl -s -X DELETE "$api" >/dev/null || true
  ;;
empty)
  # empty <ws>: one epic with one task whose agent changes nothing; waits for
  # the TaskRun to record its revision.
  epic="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d '{"title":"no changes epic","issue_type":"epic","priority":2}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])')"
  task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"no changes task\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"Check the README; nothing needs to change.\"}" |
    python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])')"
  printf '%s\n' "$epic" > "$work-epic"
  printf '%s\n' "$task" > "$work-task"
  run_epic
  wait_revisions 1
  ;;
closed)
  # closed <ws>: the newest revision is "No changes" and every route that
  # would review, apply or publish it is refused, with nothing applied or
  # opened on the forge.
  revisions "$work-revisions.json"
  python3 - "$work-revisions.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
assert len(data) == 1, data
r = data[0]
assert r["no_changes"] is True and not r["incomplete"], r
assert not r.get("verdict") and not r["applied"] and not r["needs_working_area"], r
PY
  read -r change number sha < <(newest "$work-revisions.json")
  printf '%s\n' "$change" > "$work-change"
  for verdict in approve reject override; do
    status="$(curl -s -o "$work-$verdict.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" \
      -H 'Content-Type: application/json' \
      -d "{\"head_sha\":\"$sha\",\"verdict\":\"$verdict\",\"reason\":\"aft\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}")"
    test "$status" = 409 || { echo "$verdict: HTTP $status" >&2; cat "$work-$verdict.json" >&2; exit 1; }
    grep -q '"error":"no_changes"' "$work-$verdict.json"
  done
  lead_status="$(curl -s -o "$work-lead-approve.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" \
    -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"lead\",\"id\":\"lead\"},\"lead\":\"lead\"}")"
  test "$lead_status" = 409 && grep -q '"error":"no_changes"' "$work-lead-approve.json"
  publish_status="$(curl -s -o "$work-publish.json" -w '%{http_code}' -X POST "$api/agents/lead/git/pr" \
    -H 'Content-Type: application/json' -d "{\"change_id\":\"$change\"}")"
  if [[ "$publish_status" == 2* ]] || grep -q '"created":true' "$work-publish.json"; then
    echo "publishing an empty change was not refused: HTTP $publish_status" >&2
    cat "$work-publish.json" >&2
    exit 1
  fi
  # The workspace creates the lead's working area at trunk; nothing may be
  # applied on top of it.
  lead_tip="$(git -C "$repo" rev-parse --verify --quiet "$lead_ref" || true)"
  if [[ -n "$lead_tip" && "$lead_tip" != "$(cat "$work-trunk")" ]]; then
    echo "the empty revision was applied to the lead working area: $lead_tip" >&2
    exit 1
  fi
  test "$(pulls)" = 0
  test -z "$(git --git-dir="$remote" for-each-ref "refs/heads/loom/ws/$workspace/")"
  curl -fsS "$api/issues/$(cat "$work-task")/diff" | python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert len(d) == 1 and d[0]["files"] == [], d'
  ;;
retry)
  # retry <ws> <file>: the same task runs again and this time writes <file>.
  # The empty run closed the task (and possibly its epic); reopen both.
  task="$(cat "$work-task")"
  for issue in "$task" "$(cat "$work-epic")"; do
    state="$(curl -fsS "$api/issues/$issue" | python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["status"])')"
    if [[ "$state" == closed ]]; then
      curl -fsS -X POST "$api/issues/$issue/reopen" -H 'Content-Type: application/json' -d '{}' >/dev/null
    fi
  done
  curl -fsS -X PATCH "$api/issues/$task" -H 'Content-Type: application/json' \
    -d "{\"status\":\"open\",\"design\":\"STUB_CODEX_PATCH=$3\"}" >/dev/null
  run_epic
  wait_revisions 2
  python3 - "$work-revisions.json" "$(cat "$work-change")" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
newest, empty = data[0], data[-1]
assert newest["change_id"] == sys.argv[2] and newest["number"] > empty["number"], data
assert newest["no_changes"] is False and not newest["superseded"] and not newest.get("verdict"), newest
assert empty["no_changes"] is True and empty["superseded"], empty
PY
  ;;
reviewed)
  # reviewed <ws> <file>: the attempt with changes is approved and applied,
  # then published as the task's PR, which contains only <file>.
  read -r change number sha < <(newest "$work-revisions.json")
  curl -fsS -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" > "$work-approve.json"
  grep -q '"status":"applied"' "$work-approve.json"
  test "$(git -C "$repo" rev-parse "$lead_ref")" != "$(cat "$work-trunk")"
  curl -fsS -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' \
    -d "{\"change_id\":\"$change\"}" > "$work-publish-changed.json"
  grep -q '"created":true' "$work-publish-changed.json"
  test "$(pulls)" = 1
  curl -fsS "$api/issues/$(cat "$work-task")/diff" |
    python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert len(d) == 1 and [f["path"] for f in d[0]["files"]] == [sys.argv[1]], d' "$3"
  ;;
*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac

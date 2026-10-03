#!/usr/bin/env bash
# P2.19 (D29): Approve opens the task's PR straight away. Stacked PRs: each
# approved task's PR is the next PR of the stack; Approve only applies without
# a PR and the task then offers Create PR. PR per task: each approval opens its
# own PR to trunk, a lead approval does too under Lead may approve, and with
# that policy off the lead's approval is refused.
set -euo pipefail

phase="$1"
case_name="$2"
case_dir="$AFT_WORK_DIR/approve-pr-$case_name"
if [[ "$case_name" == stack ]]; then workspace="E2E-WS-APPROVEPR-STACK"; else workspace="E2E-WS-APPROVEPR-TRUNK"; fi
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo="$case_dir/approve-repo"
remote="$case_dir/origin.git"
slug="owner/approve-pr-$case_name"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

loom() {
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" --workspace "$workspace" "$@"
}

browser() {
  agent-browser --session "$AFT_SESSION" "$@"
}

json() {
  python3 -c "import json,sys; d=json.load(open(sys.argv[1])); print($2)" "$1"
}

if [[ "$phase" == setup ]]; then
  mkdir -p "$case_dir"
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$repo"
  printf 'base\n' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
  git -C "$repo" remote add origin "git@github.com:$slug.git"
  git -C "$repo" push -q origin main
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"remotes":{sys.argv[2]:sys.argv[1]},"native_stacks":False,"preserve":True}))' "$remote" "$slug" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$(tr '[:upper:]' '[:lower:]' <<<"$workspace")\",\"type\":\"empty\",\"repos\":[\"$repo\"]}" >/dev/null
  curl -fsS -X POST "$api/agents" -H 'Content-Type: application/json' \
    -d '{"name":"lead","role_name":"lead","auto":false,"cross_repo":true,"repos":[],"backend":"codex"}' >/dev/null
  exit 0
fi

if [[ "$phase" == teardown ]]; then
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh"
  curl -s -X DELETE "$api/agents/lead" >/dev/null || true
  curl -s -X DELETE "$api" >/dev/null || true
  exit 0
fi

# Run each named task through the real TaskRun workflow until it records a
# revision. With chain, each task depends on the previous one. No `loom stack`
# stack is declared: Approve and create PR publishes through the lead's stack.
run_tasks() {
  local chain="$1"
  shift
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"approve-pr epic $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  epic="$(json "$case_dir/epic.json" 'd["data"]["id"]')"
  for name in "$@"; do
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
      -d "{\"title\":\"approve-pr $name $RUN_ID\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"STUB_CODEX_PATCH=approve-$name.txt\"}" > "$case_dir/task-$name.json"
    json "$case_dir/task-$name.json" 'd["data"]["id"]' > "$case_dir/task-$name.id"
  done
  if [[ "$chain" == chain ]]; then
    previous=""
    for name in "$@"; do
      task="$(cat "$case_dir/task-$name.id")"
      if [[ -n "$previous" ]]; then
        curl -fsS -X POST "$api/issues/$task/dependencies" -H 'Content-Type: application/json' \
          -d "{\"depends_on_id\":\"$previous\",\"dep_type\":\"blocks\"}" >/dev/null
      fi
      previous="$task"
    done
  fi
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$case_dir/workflow.json"
  for name in "$@"; do
    task="$(cat "$case_dir/task-$name.id")"
    for _ in $(seq 1 90); do
      curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$name.json"
      if grep -q '"head_sha"' "$case_dir/revisions-$name.json"; then break; fi
      sleep 2
    done
    grep -q '"head_sha"' "$case_dir/revisions-$name.json"
  done
}

# newest prints "change number head_sha" for the task's newest revision.
newest() {
  curl -fsS "$api/issues/$(cat "$case_dir/task-$1.id")/revisions" > "$case_dir/newest-$1.json"
  python3 -c 'import json,sys; items=json.load(open(sys.argv[1]))["data"]; r=max(items,key=lambda i:i["number"]); print(r["change_id"],r["number"],r["head_sha"])' "$case_dir/newest-$1.json"
}

# verdict posts an approval as actor kind/id for the task's newest revision and
# prints the HTTP status. Extra JSON fields (e.g. approve_only) go in $4.
verdict() {
  local name="$1" kind="$2" id="$3" extra="${4:-}"
  read -r change number sha < <(newest "$name")
  printf '%s\n' "$change" > "$case_dir/change-$name.id"
  curl -sS -o "$case_dir/verdict-$name-$kind.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" \
    -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\"$extra,\"actor\":{\"kind\":\"$kind\",\"id\":\"$id\"}}"
}

# applied_if_waiting applies an approval that waited for the lead working area,
# the same as the task's Apply button.
applied_if_waiting() {
  local name="$1" file="$2"
  if grep -q 'approved_waiting_for_working_area' "$file"; then
    read -r change number _ < <(newest "$name")
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$number,\"lead\":\"lead\"}" > "$case_dir/apply-$name.json"
    grep -q '"success":true' "$case_dir/apply-$name.json"
  fi
}

pulls() {
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/$1"
}

# wait_pulls waits until the fake forge has $1 PRs, published by the approval
# itself or, after a held apply, by the background reconciler.
wait_pulls() {
  for _ in $(seq 1 30); do
    pulls "pulls-$1.json"
    if [[ "$(json "$case_dir/pulls-$1.json" 'len(d)')" == "$1" ]]; then return 0; fi
    sleep 1
  done
  echo "expected $1 PRs, got: $(cat "$case_dir/pulls-$1.json")" >&2
  return 1
}

open_task() {
  browser open "$AFT_BASE_URL/ws/$workspace/kanban" >/dev/null
  browser wait '[data-testid="board-toolbar"]' >/dev/null
  browser open "$AFT_BASE_URL/ws/$workspace/issues/$(cat "$case_dir/task-$1.id")" >/dev/null
  browser wait '[data-testid="revisions-section"]' >/dev/null
}

if [[ "$case_name" == stack ]]; then
  run_tasks chain a b c

  # A: approved over the API (as the lead's UI and agents do); the PR opens
  # once the approval is applied, based on trunk.
  test "$(verdict a human aft-operator)" = 200
  applied_if_waiting a "$case_dir/verdict-a-human.json"
  wait_pulls 1
  json "$case_dir/pulls-1.json" 'd[0]["base"]["ref"]' | grep -qx main

  # B: Approve and create PR in the browser opens the next PR of the stack.
  open_task b
  browser wait '[data-testid="approve-menu-toggle"]:not([disabled])' >/dev/null
  browser screenshot "$case_dir/stack-awaiting-review.png" >/dev/null
  browser click '[data-testid="approve-menu-toggle"]' >/dev/null
  browser wait '[data-testid="approve-only"]' >/dev/null
  browser screenshot "$case_dir/stack-approve-menu.png" >/dev/null
  browser click '[data-testid="approve-menu-toggle"]' >/dev/null
  browser click '[data-testid="approve-create-pr"]:not([disabled])' >/dev/null
  browser wait '[data-testid="revision-pr"]' >/dev/null
  browser screenshot "$case_dir/stack-pr-open.png" >/dev/null
  test "$(browser eval "document.querySelectorAll('[data-testid=\"approve-menu-toggle\"]').length")" = 0
  wait_pulls 2
  python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert p[1]["base"]["ref"]==p[0]["head"]["ref"], p' "$case_dir/pulls-2.json"

  # C: Approve only applies without a PR; Create PR then opens it on PR B.
  open_task c
  browser click '[data-testid="approve-menu-toggle"]:not([disabled])' >/dev/null
  browser click '[data-testid="approve-only"]' >/dev/null
  browser wait '[data-testid="create-pr"]' >/dev/null
  browser screenshot "$case_dir/stack-in-working-area.png" >/dev/null
  sleep 3
  pulls pulls-after-approve-only.json
  test "$(json "$case_dir/pulls-after-approve-only.json" 'len(d)')" = 2
  browser click '[data-testid="create-pr"]' >/dev/null
  if ! browser wait '[data-testid="revision-pr"]' >/dev/null; then
    browser eval "document.querySelector('[data-testid=\"revisions-section\"] [role=alert]')?.textContent" >&2 || true
    curl -sS -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' \
      -d "{\"change_id\":\"$(newest c | cut -d' ' -f1)\"}" >&2 || true
    exit 1
  fi
  wait_pulls 3
  python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert p[2]["base"]["ref"]==p[1]["head"]["ref"], p' "$case_dir/pulls-3.json"
  # PR C's head is C's layer commit, the top of the lead working area.
  head_ref="$(json "$case_dir/pulls-3.json" 'd[2]["head"]["ref"]')"
  test "$(git -C "$remote" rev-parse "refs/heads/$head_ref")" = "$(git -C "$repo" rev-parse "refs/heads/loom/ws/$workspace/interactive/lead")"
  # Approving again, or the reconciler running again, opens no duplicate PRs.
  sleep 3
  pulls pulls-final.json
  test "$(json "$case_dir/pulls-final.json" 'len(d)')" = 3

  # G: a `loom stack` stack is declared for the repo. Approve and create PR
  # applies G but never opens a PR in a second, parallel stack; the task says
  # why, once, with no retries.
  run_tasks none g
  loom stack init aft-declared --repo approve-repo --base main >/dev/null
  loom stack add "$(cat "$case_dir/task-g.id")" --stack aft-declared >/dev/null
  test "$(verdict g human aft-operator)" = 200
  applied_if_waiting g "$case_dir/verdict-g-human.json"
  reason="not published: declared stack aft-declared is active; publish it with loom stack"
  for _ in $(seq 1 30); do
    curl -fsS "$api/issues/$(cat "$case_dir/task-g.id")/revisions" > "$case_dir/revisions-g-final.json"
    if grep -q '"publish_status":"not_published"' "$case_dir/revisions-g-final.json"; then break; fi
    sleep 1
  done
  # A revision Apply derived from the approved one reports the same outcome;
  # the task shows it once, on the newest revision.
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; r=[i for i in d if i.get("publish_status")]; assert r and d[0] in r and all(i["publish_status"]=="not_published" and i["publish_reason"]==sys.argv[2] for i in r), r' \
    "$case_dir/revisions-g-final.json" "$reason"
  open_task g
  browser wait '[data-testid="revision-publish-status"]' >/dev/null
  test "$(browser eval "document.querySelectorAll('[data-testid=\"revision-publish-status\"]').length")" = 1
  browser eval "document.querySelector('[data-testid=\"revision-publish-status\"]').textContent" | grep -qF "declared stack aft-declared is active"
  browser screenshot "$case_dir/stack-declared-not-published.png" >/dev/null
  sleep 3
  pulls pulls-declared.json
  test "$(json "$case_dir/pulls-declared.json" 'len(d)')" = 3
  exit 0
fi

# PR per task: each approval opens its own PR to trunk.
test "$(curl -sS -o "$case_dir/mode.json" -w '%{http_code}' -X PUT "$api/git/settings" -H 'Content-Type: application/json' \
  -d '{"actor":{"kind":"human","id":"aft-operator"},"delivery_mode":"trunk","lead_may_approve_publish":true}')" = 200
run_tasks none d e f

test "$(verdict d human aft-operator)" = 200
applied_if_waiting d "$case_dir/verdict-d-human.json"
wait_pulls 1
json "$case_dir/pulls-1.json" 'd[0]["base"]["ref"]' | grep -qx main

# The lead approves under Lead may approve: its PR opens too, to trunk.
test "$(verdict e lead lead)" = 200
json "$case_dir/verdict-e-lead.json" 'd["data"]["Kind"]' | grep -qx policy
wait_pulls 2
python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert p[1]["base"]["ref"]=="main" and sys.argv[2] in p[1]["head"]["ref"], p' "$case_dir/pulls-2.json" "$(cat "$case_dir/change-e.id")"

# With Lead may approve off, the lead's approval is refused and opens nothing.
test "$(curl -sS -o "$case_dir/policy-off.json" -w '%{http_code}' -X PUT "$api/git/settings" -H 'Content-Type: application/json' \
  -d '{"actor":{"kind":"human","id":"aft-operator"},"lead_may_approve_publish":false}')" = 200
test "$(verdict f lead lead)" = 409
grep -q 'review_required' "$case_dir/verdict-f-lead.json"
sleep 3
pulls pulls-refused.json
test "$(json "$case_dir/pulls-refused.json" 'len(d)')" = 2

# The task in the browser shows its own PR to trunk.
open_task e
browser wait '[data-testid="revision-pr"]' >/dev/null
browser screenshot "$case_dir/trunk-pr-open.png" >/dev/null

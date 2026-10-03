#!/usr/bin/env bash
# P3.14 (D29 (3)): Approve on a task whose PR is open is an approval to merge,
# pinned to the PR head the human saw.
#   loom    Loom stack: a PR above the bottom waits ("merges after #N"), the
#           bottom is blocked by a pending required check and merges once it
#           passes, the waiting PR then merges after its restack; Cancel
#           auto-merge stops one; Approve and merge in the task merges the next
#   native  native stack: the waiting PR merges after the bottom one
#   trunk   PR per task: a PR someone else pushed to is not merged
#           (stale_subject); another merges exactly once; the lead never merges
#   cross   cross-repo lead: each repo's stack ID is shown by Create PR and
#           loom stack list, and the task's Approve merges without one
set -Eeuo pipefail

phase="$1"
case_name="$2"
case_dir="$AFT_WORK_DIR/approve-merge-$case_name"
upper="$(printf '%s' "$case_name" | tr '[:lower:]' '[:upper:]')"
workspace="E2E-WS-APPROVEMERGE-$upper"
api="$AFT_BASE_URL/api/workspaces/$workspace"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

native=false
[[ "$case_name" == native ]] && native=true
repos=("approve-merge-$case_name")
[[ "$case_name" == cross ]] && repos=("approve-merge-cross-api" "approve-merge-cross-app")

loom() {
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" "$@" --workspace "$workspace"
}

browser() {
  agent-browser --session "$AFT_SESSION" "$@"
}

json() {
  python3 -c "import json,sys; v=json.load(open(sys.argv[1])); $2" "$1" "${@:3}"
}

if [[ "$phase" == setup ]]; then
  mkdir -p "$case_dir"
  remotes="{}"
  paths=()
  for name in "${repos[@]}"; do
    remote="$case_dir/$name.git"
    repo="$case_dir/$name"
    git init -q --bare "$remote"
    git -C "$remote" symbolic-ref HEAD refs/heads/main
    git init -q -b main "$repo"
    printf 'base %s\n' "$name" > "$repo/README.md"
    git -C "$repo" add README.md
    git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
    git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
    git -C "$repo" remote add origin "git@github.com:owner/$name.git"
    git -C "$repo" push -q origin main
    remotes="$(python3 -c 'import json,sys; v=json.loads(sys.argv[1]); v["owner/"+sys.argv[2]]=sys.argv[3]; print(json.dumps(v))' "$remotes" "$name" "$remote")"
    paths+=("$repo")
  done
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"remotes":json.loads(sys.argv[2]),"native_stacks":sys.argv[3]=="true","preserve":True}))' \
    "$case_dir/${repos[0]}.git" "$remotes" "$native" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  python3 -c 'import json,sys; print(json.dumps({"name":sys.argv[1],"type":"empty","repos":sys.argv[2:]}))' "$(tr '[:upper:]' '[:lower:]' <<<"$workspace")" "${paths[@]}" |
    curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' -d @- >/dev/null
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

# run_tasks <chain|none> <name:repo>... runs one real task per argument through
# the epic runner until each has a reviewable revision. With chain, each task
# depends on the one before it, so their PRs form one stack.
run_tasks() {
  local chain="$1"
  shift
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"approve-merge epic $case_name $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  local epic previous="" spec name repo task
  epic="$(json "$case_dir/epic.json" 'print(v["data"]["id"])')"
  for spec in "$@"; do
    name="${spec%%:*}"
    repo="${spec#*:}"
    python3 -c 'import json,sys; print(json.dumps({"title":"approve-merge "+sys.argv[1]+" "+sys.argv[2],"issue_type":"task","priority":2,"parent":sys.argv[3],"source_repo":sys.argv[4],"design":"STUB_CODEX_PATCH=approve-merge-"+sys.argv[1]+".txt"}))' \
      "$name" "$RUN_ID" "$epic" "$repo" |
      curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$case_dir/task-$name.json"
    task="$(json "$case_dir/task-$name.json" 'print(v["data"]["id"])')"
    printf '%s\n' "$task" > "$case_dir/task-$name.id"
    if [[ "$chain" == chain && -n "$previous" ]]; then
      curl -fsS -X POST "$api/issues/$task/dependencies" -H 'Content-Type: application/json' \
        -d "{\"depends_on_id\":\"$previous\",\"dep_type\":\"blocks\"}" >/dev/null
    fi
    previous="$task"
  done
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$case_dir/workflow.json"
  for spec in "$@"; do
    name="${spec%%:*}"
    task="$(cat "$case_dir/task-$name.id")"
    for _ in $(seq 1 90); do
      curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$name.json"
      if grep -q '"head_sha"' "$case_dir/revisions-$name.json"; then break; fi
      sleep 2
    done
    grep -q '"head_sha"' "$case_dir/revisions-$name.json"
  done
}

# newest prints "change number head_sha pr_head" for the task's newest revision.
newest() {
  curl -fsS "$api/issues/$(cat "$case_dir/task-$1.id")/revisions" > "$case_dir/newest-$1.json"
  json "$case_dir/newest-$1.json" 'r=max(v["data"],key=lambda i:i["number"]); print(r["change_id"],r["number"],r["head_sha"],r.get("pr_head") or "-")'
}

# approve <name> [extra json]: approves the task's newest revision as a human
# (Approve and create PR), applying it if it waited for the lead working area.
approve() {
  local name="$1" extra="${2:-}" change number sha code
  read -r change number sha _ < <(newest "$name")
  printf '%s\n' "$change" > "$case_dir/change-$name.id"
  code="$(curl -sS -o "$case_dir/verdict-$name.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\"$extra,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}")"
  # A PR that did not open at once stands approved; the reconciler retries it.
  [[ "$code" == 200 ]] || { [[ "$code" == 409 ]] && grep -q '"publish_failed"' "$case_dir/verdict-$name.json"; }
  if grep -q 'approved_waiting_for_working_area' "$case_dir/verdict-$name.json"; then
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$number,\"lead\":\"lead\"}" > "$case_dir/apply-$name.json"
    grep -q '"success":true' "$case_dir/apply-$name.json"
  fi
}

# wait_pr <name>: waits until the task's revision shows its open PR.
wait_pr() {
  for _ in $(seq 1 45); do
    read -r _ _ _ pr_head < <(newest "$1")
    [[ "$pr_head" != "-" ]] && return 0
    sleep 2
  done
  echo "task $1 has no open PR: $(cat "$case_dir/newest-$1.json")" >&2
  return 1
}

# merge_approve <name> [kind] [id]: Approve and merge of the task's open PR at
# the PR head shown in the task. Prints the HTTP status.
merge_approve() {
  local name="$1" kind="${2:-human}" id="${3:-aft-operator}" change pr_head
  read -r change _ _ pr_head < <(newest "$name")
  curl -sS -o "$case_dir/merge-approval-$name.json" -w '%{http_code}' -X POST "$api/changes/$change/merge-approval" \
    -H 'Content-Type: application/json' \
    -d "{\"lead\":\"lead\",\"head_sha\":\"$pr_head\",\"actor\":{\"kind\":\"$kind\",\"id\":\"$id\"}}"
}

# approval_state <name>: prints "status|reason" of the task's Approve and merge.
approval_state() {
  local change
  read -r change _ _ _ < <(newest "$1")
  curl -fsS "$api/changes/$change/merge-approval" > "$case_dir/approval-$1.json"
  json "$case_dir/approval-$1.json" 'd=v.get("data",v); print((d.get("status") or "")+"|"+(d.get("reason") or ""))'
}

wait_state() { # wait_state <name> <status> [reason substring]
  local got=""
  for _ in $(seq 1 60); do
    got="$(approval_state "$1")"
    if [[ "${got%%|*}" == "$2" && "${got#*|}" == *"${3:-}"* ]]; then return 0; fi
    sleep 2
  done
  echo "task $1: merge approval '$got', want '$2' with '${3:-}'" >&2
  return 1
}

pull_of() { # pull_of <name>: the task's PR number on the fake forge
  local change
  change="$(cat "$case_dir/change-$1.id")"
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-now.json"
  json "$case_dir/pulls-now.json" 'print([x for x in v if x["head"]["ref"].endswith("/change/"+sys.argv[2])][0]["number"])' "$change"
}

merged() { # merged <name>: 1 when the task's PR is merged on the fake forge
  local change
  change="$(cat "$case_dir/change-$1.id")"
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-now.json"
  json "$case_dir/pulls-now.json" 'print("1" if [x for x in v if x["head"]["ref"].endswith("/change/"+sys.argv[2])][0]["merged_at"] else "0")' "$change"
}

wait_merged() { # wait_merged <name>...
  local name
  for name in "$@"; do
    for _ in $(seq 1 60); do
      [[ "$(merged "$name")" == 1 ]] && break
      sleep 2
    done
    [[ "$(merged "$name")" == 1 ]] || { echo "PR of task $name did not merge: $(cat "$case_dir/pulls-now.json")" >&2; return 1; }
  done
}

hold_unmerged() { # hold_unmerged <seconds> <name>...: none of them merges meanwhile
  local seconds="$1" name
  shift
  for _ in $(seq 1 "$seconds"); do
    for name in "$@"; do
      [[ "$(merged "$name")" == 0 ]] || { echo "PR of task $name merged early" >&2; return 1; }
    done
    sleep 1
  done
}

merge_puts() { # merge_puts <pr number>: merge-async PUTs the provider received
  curl -fsS "$AFT_FAKE_GH_BASE/__requests" > "$case_dir/requests.json"
  json "$case_dir/requests.json" 'print(sum(1 for r in v if r["method"]=="PUT" and r["path"].endswith("/pulls/"+sys.argv[2]+"/merge-async")))' "$1"
}

pr_status() { # pr_status <number> <json fields>
  curl -fsS -X POST "$AFT_FAKE_GH_BASE/__pr_status" -H 'Content-Type: application/json' -d "{\"number\":$1,$2}" >/dev/null
}

open_task() {
  browser open "$AFT_BASE_URL/ws/$workspace/kanban" >/dev/null
  browser wait '[data-testid="board-toolbar"]' >/dev/null
  browser open "$AFT_BASE_URL/ws/$workspace/issues/$(cat "$case_dir/task-$1.id")" >/dev/null
  browser wait '[data-testid="revisions-section"]' >/dev/null
}

merge_status_text() {
  browser eval "document.querySelector('[data-testid=\"merge-status\"]')?.textContent || ''"
}

wait_merge_status() { # wait_merge_status <text>: the open task shows it
  for _ in $(seq 1 30); do
    if merge_status_text | grep -qF "$1"; then return 0; fi
    sleep 1
    browser eval "location.reload()" >/dev/null || true
    browser wait '[data-testid="revisions-section"]' >/dev/null || true
  done
  echo "task shows '$(merge_status_text)', want '$1'" >&2
  return 1
}

# diagnose dumps each task's revisions and Approve and merge state, the forge
# PRs and the Loom Git stacks into the case directory when a step fails.
diagnose() {
  local file="$case_dir/diagnosis.txt" id name change
  {
    for id in "$case_dir"/task-*.id; do
      name="$(basename "$id" .id)"
      name="${name#task-}"
      echo "== task $name revisions"
      curl -sS "$api/issues/$(cat "$id")/revisions" || true
      echo
      if [[ -f "$case_dir/change-$name.id" ]]; then
        change="$(cat "$case_dir/change-$name.id")"
        echo "== task $name merge approval"
        curl -sS "$api/changes/$change/merge-approval" || true
        echo
      fi
    done
    echo "== pulls"
    curl -sS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" || true
    echo
    echo "== loom stack list"
    loom stack list 2>&1 || true
    for verdict in "$case_dir"/verdict-*.json; do
      [[ -f "$verdict" ]] && { echo "== $(basename "$verdict")"; cat "$verdict"; echo; }
    done
  } > "$file" 2>&1
  echo "diagnosis: $file" >&2
}
trap 'diagnose' ERR

case "$case_name" in
  loom)
    run_tasks chain a:"${repos[0]}" b:"${repos[0]}" c:"${repos[0]}" d:"${repos[0]}"
    for name in a b c d; do approve "$name"; wait_pr "$name"; done
    pa="$(pull_of a)"; pb="$(pull_of b)"; pc="$(pull_of c)"

    # B is above A: its approval waits and names the PR below.
    test "$(merge_approve b)" = 200
    wait_state b waiting "merges after #$pa"
    open_task b
    wait_merge_status "Approved, merges after #$pa"
    test "$(browser eval "document.querySelectorAll('[data-testid=\"approve-menu-toggle\"]').length")" = 0
    browser screenshot "$case_dir/loom-merges-after.png" >/dev/null

    # D waits too; Cancel auto-merge in the task stops it for good.
    test "$(merge_approve d)" = 200
    wait_state d waiting "merges after #$pa, #$pb, #$pc"
    open_task d
    wait_merge_status "Approved, merges after #$pa, #$pb, #$pc"
    browser click '[data-testid="cancel-auto-merge"]' >/dev/null
    wait_merge_status "Auto-merge cancelled"
    browser wait '[data-testid="approve-merge"]' >/dev/null
    browser screenshot "$case_dir/loom-cancelled.png" >/dev/null
    wait_state d cancelled "auto-merge cancelled"

    # A is the bottom, but a required check is pending: blocked, retried.
    pr_status "$pa" '"checks":"PENDING","merge_state":"BLOCKED"'
    test "$(merge_approve a)" = 200
    wait_state a blocked "waiting for required checks"
    open_task a
    wait_merge_status "merge blocked: waiting for required checks"
    browser screenshot "$case_dir/loom-blocked.png" >/dev/null
    hold_unmerged 6 a b
    test "$(merge_puts "$pa")" = 0

    # The check passes: A merges, then B after its restack, each once; C and
    # D stay open.
    pr_status "$pa" '"checks":"SUCCESS","merge_state":"CLEAN"'
    wait_merged a b
    wait_state a merged
    wait_state b merged
    test "$(merge_puts "$pa")" -le 1
    test "$(merge_puts "$pb")" -le 1
    hold_unmerged 4 c d

    # C is now the bottom: Approve and merge in the task merges it.
    open_task c
    browser wait '[data-testid="approve-merge"]:not([disabled])' >/dev/null
    browser eval "document.querySelector('[data-testid=\"approve-merge\"]').textContent" | grep -qx '"*Approve and merge"*'
    browser screenshot "$case_dir/loom-approve-and-merge.png" >/dev/null
    browser click '[data-testid="approve-merge"]' >/dev/null
    wait_merged c
    wait_merge_status "Merged"
    browser screenshot "$case_dir/loom-merged.png" >/dev/null
    hold_unmerged 4 d
    ;;

  native)
    run_tasks chain a:"${repos[0]}" b:"${repos[0]}"
    for name in a b; do approve "$name"; wait_pr "$name"; done
    pa="$(pull_of a)"
    test "$(merge_approve b)" = 200
    wait_state b waiting "merges after #$pa"
    hold_unmerged 4 a b
    test "$(merge_approve a)" = 200
    wait_merged a b
    wait_state a merged
    wait_state b merged
    open_task b
    wait_merge_status "Merged"
    browser screenshot "$case_dir/native-merged.png" >/dev/null
    ;;

  trunk)
    test "$(curl -sS -o "$case_dir/mode.json" -w '%{http_code}' -X PUT "$api/git/settings" -H 'Content-Type: application/json' \
      -d '{"actor":{"kind":"human","id":"aft-operator"},"delivery_mode":"trunk"}')" = 200
    run_tasks none d:"${repos[0]}" e:"${repos[0]}"
    for name in d e; do approve "$name"; wait_pr "$name"; done
    pd="$(pull_of d)"; pe="$(pull_of e)"

    # The lead never merges, even on a green PR.
    test "$(merge_approve e lead lead)" = 409
    hold_unmerged 3 e

    # Someone else pushes to D's PR after the human looked at it; the human's
    # Approve and merge, pinned to the head they saw, does not merge it.
    ref="$(json "$case_dir/pulls-now.json" 'print([x for x in v if x["number"]==int(sys.argv[2])][0]["head"]["ref"])' "$pd")"
    remote="$case_dir/${repos[0]}.git"
    read -r _ _ _ seen < <(newest d)
    foreign="$(git --git-dir="$remote" commit-tree -p "$seen" -m "someone else" "$seen^{tree}")"
    git --git-dir="$remote" update-ref "refs/heads/$ref" "$foreign"
    test "$(merge_approve d)" = 200
    wait_state d stale_subject "someone else pushed"
    hold_unmerged 4 d
    test "$(merge_puts "$pd")" = 0
    open_task d
    wait_merge_status "Not merged: someone else pushed to the PR after it was approved"
    test "$(browser eval "document.querySelectorAll('[data-testid=\"approve-merge\"]').length")" = 0
    browser screenshot "$case_dir/trunk-stale-subject.png" >/dev/null

    # E merges exactly once, pinned to the head the human saw.
    test "$(merge_approve e)" = 200
    wait_merged e
    wait_state e merged
    test "$(merge_puts "$pe")" = 1
    open_task e
    wait_merge_status "Merged"
    browser screenshot "$case_dir/trunk-merged.png" >/dev/null
    ;;

  cross)
    run_tasks none one:"${repos[0]}" two:"${repos[1]}"
    approve one
    wait_pr one
    # Approve only, then Create PR: the response names the repo's stack.
    approve two ',"approve_only":true'
    read -r change _ _ _ < <(newest two)
    curl -fsS -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' \
      -d "{\"change_id\":\"$change\"}" > "$case_dir/create-pr-two.json"
    stack_two="$(json "$case_dir/create-pr-two.json" 'd=v.get("data",v); print(d.get("stack_id",""))')"
    test -n "$stack_two"
    wait_pr two
    loom stack list > "$case_dir/stack-list.txt"
    grep -E "^$stack_two  repo=${repos[1]} loom-git PRs=#[0-9]+" "$case_dir/stack-list.txt"
    grep -E "  repo=${repos[0]} loom-git PRs=#[0-9]+" "$case_dir/stack-list.txt" | grep -v "^$stack_two "

    # The task's Approve and merge needs no stack ID: each repo's PR merges
    # through its own stack.
    test "$(merge_approve two)" = 200
    wait_merged two
    hold_unmerged 3 one
    test "$(merge_approve one)" = 200
    wait_merged one
    loom stack list > "$case_dir/stack-list-after.txt"
    grep -E "^$stack_two  repo=${repos[1]} loom-git PRs=#[0-9]+\(merged\)" "$case_dir/stack-list-after.txt"
    ;;
esac

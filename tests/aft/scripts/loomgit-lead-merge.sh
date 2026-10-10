#!/usr/bin/env bash
# Lead merge paths on a published stack (P3.11, P3.12, P3.13, P3.16):
#   queue    the lead's loom merge is refused while Lead may merge is off (nothing
#            queued); once a human turns it on, one loom merge queues the whole
#            stack, the PR page shows it, and it lands bottom up once green (AFT-G2)
#   green    when_green waits for required checks, stops below changes_requested, then continues
#   later    a human's Merge up to here merges a Loom stack up to B, then a later one merges C
#   deps     a cross-repo dependency keeps loom/dependencies pending until its predecessor lands
set -euo pipefail
source "$AFT_TESTS_DIR/scripts/loomgit-lib.sh"

phase="$1"
case_name="$2"
case_dir="$AFT_WORK_DIR/lead-merge-$case_name"
upper="$(printf '%s' "$case_name" | tr '[:lower:]' '[:upper:]')"
workspace="E2E-WS-LEAD-$upper"
api="$AFT_BASE_URL/api/workspaces/$workspace"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

case "$case_name" in
  queue | green) backend=native ;;
  *) backend=loom ;;
esac
# Each case has its own owner/<repo> so the fake forge routes every PR to the
# right bare remote even while earlier cases' PRs are still open.
app_repo="lead-$case_name"
api_repo=""
repos=("$app_repo")
if [[ "$case_name" == deps ]]; then app_repo="lead-deps-app"; api_repo="lead-deps-api"; repos=("$api_repo" "$app_repo"); fi

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
  single="$case_dir/${repos[0]}.git"
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"remotes":json.loads(sys.argv[2]),"native_stacks":sys.argv[3]=="native","preserve":True}))' \
    "$single" "$remotes" "$backend" "$case_name" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  python3 -c 'import json,sys; print(json.dumps({"name":sys.argv[1],"type":"empty","repos":sys.argv[2:]}))' "e2e-ws-lead-$case_name" "${paths[@]}" |
    curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' -d @- >/dev/null
  curl -fsS -X POST "$api/agents" -H 'Content-Type: application/json' \
    -d '{"name":"lead","role_name":"lead","auto":false,"cross_repo":true,"repos":[],"backend":"codex"}' >/dev/null
  exit 0
fi

if [[ "$phase" == teardown ]]; then
  loom git-settings --auto-merge off >/dev/null 2>&1 || true
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh"
  curl -s -X DELETE "$api/agents/lead" >/dev/null || true
  curl -s -X DELETE "$api" >/dev/null || true
  exit 0
fi

# make_tasks <stack|none> <repo:file>... creates one real task per argument and
# runs them through the epic runner until each has a reviewable revision.
make_tasks() {
  local stack="$1"
  shift
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"lead merge epic $case_name $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  local epic previous="" layer=0 spec repo file task
  epic="$(json "$case_dir/epic.json" 'print(v["data"]["id"])')"
  if [[ "$stack" != none && "$stack" != deps ]]; then loom stack init "$stack" --repo "${repos[0]}" --base main >/dev/null; fi
  for spec in "$@"; do
    layer=$((layer + 1))
    repo="${spec%%:*}"
    file="${spec#*:}"
    python3 -c 'import json,sys; print(json.dumps({"title":"lead merge "+sys.argv[1]+" "+sys.argv[2],"issue_type":"task","priority":2,"parent":sys.argv[3],"source_repo":sys.argv[4],"design":"STUB_CODEX_PATCH="+sys.argv[5]}))' \
      "$file" "$RUN_ID" "$epic" "$repo" "$file" |
      curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$case_dir/task-$layer.json"
    task="$(json "$case_dir/task-$layer.json" 'print(v["data"]["id"])')"
    printf '%s\n' "$task" > "$case_dir/task-$layer.id"
    if [[ "$stack" != none && "$stack" != deps ]]; then
      if [[ -n "$previous" ]]; then
        curl -fsS -X POST "$api/issues/$task/dependencies" -H 'Content-Type: application/json' \
          -d "{\"depends_on_id\":\"$previous\",\"dep_type\":\"blocks\"}" >/dev/null
        loom stack add "$task" --stack "$stack" --after "$previous" >/dev/null
      else
        loom stack add "$task" --stack "$stack" >/dev/null
      fi
    fi
    previous="$task"
  done
  printf '%s\n' "$layer" > "$case_dir/layers"
  if [[ "$stack" == deps ]]; then
    # Task 2 is blocked by task 1 before any TaskRun starts: the issue store
    # refuses new dependencies on running or closed tasks.
    curl -fsS -X POST "$api/issues/$(cat "$case_dir/task-2.id")/dependencies" -H 'Content-Type: application/json' \
      -d "{\"depends_on_id\":\"$(cat "$case_dir/task-1.id")\",\"dep_type\":\"blocks\"}" >/dev/null
    # The app change gets a named stack so a human can ask Loom to merge it.
    loom stack init deps-app --repo "${repos[1]}" --base main >/dev/null
    loom stack add "$(cat "$case_dir/task-2.id")" --stack deps-app >/dev/null
  fi
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$case_dir/workflow.json"
  for layer in $(seq 1 "$(cat "$case_dir/layers")"); do
    task="$(cat "$case_dir/task-$layer.id")"
    for _ in $(seq 1 90); do
      curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$layer.json"
      if grep -q '"head_sha"' "$case_dir/revisions-$layer.json"; then break; fi
      sleep 2
    done
    grep -q '"head_sha"' "$case_dir/revisions-$layer.json"
  done
}

# approve_and_apply approves each revision as a human, applies it into the lead
# area and approves the Apply-derived head, as a stack merge requires.
approve_and_apply() {
  local layer task change revision sha
  for layer in $(seq 1 "$(cat "$case_dir/layers")"); do
    task="$(cat "$case_dir/task-$layer.id")"
    read -r change revision sha < <(json "$case_dir/revisions-$layer.json" 'i=v["data"][0]; print(i["change_id"],i["number"],i["head_sha"])')
    printf '%s\n' "$change" > "$case_dir/change-$layer.id"
    curl -sS --fail-with-body -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
      -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" >/dev/null
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$revision,\"lead\":\"lead\"}" > "$case_dir/apply-$layer.json"
    grep -q '"success":true' "$case_dir/apply-$layer.json"
    curl -fsS "$api/issues/$task/revisions" > "$case_dir/applied-$layer.json"
    read -r revision sha < <(json "$case_dir/applied-$layer.json" 'i=v["data"][0]; print(i["number"],i["head_sha"])')
    curl -fsS -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
      -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" >/dev/null
  done
}

publish_stack() {
  local changes=()
  for layer in $(seq 1 "$(cat "$case_dir/layers")"); do changes+=("$(cat "$case_dir/change-$layer.id")"); done
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" pr-stack merge-chain lead "${changes[@]}" --workspace "$workspace" > "$case_dir/publish.txt"
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-published.json"
  json "$case_dir/pulls-published.json" 'assert len(v)==int(sys.argv[2]), v' "${#changes[@]}"
}

pull_number() { # pull_number <layer>
  json "$case_dir/pulls-published.json" 'print(v[int(sys.argv[2])-1]["number"])' "$1"
}

pulls_now() {
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-now.json"
}

merged_layers() { # prints which published layers are merged, e.g. "1 1 0"
  pulls_now
  json "$case_dir/pulls-now.json" 'print(" ".join("1" if x["merged_at"] else "0" for x in v))'
}

merge_puts() { # number of merge-async PUTs the provider received for a PR
  curl -fsS "$AFT_FAKE_GH_BASE/__requests" > "$case_dir/requests.json"
  json "$case_dir/requests.json" 'print(sum(1 for r in v if r["method"]=="PUT" and r["path"].endswith("/pulls/"+sys.argv[2]+"/merge-async")))' "$1"
}

wait_merged() { # wait_merged <expected "1 1 0"> <label>
  local got=""
  for _ in $(seq 1 60); do
    got="$(merged_layers)"
    [[ "$got" == "$1" ]] && return 0
    sleep 2
  done
  echo "$2: merged layers '$got', want '$1'" >&2
  return 1
}

hold_unmerged() { # hold_unmerged <seconds> <expected> <label>: nothing new merges meanwhile
  local got=""
  for _ in $(seq 1 "$1"); do
    got="$(merged_layers)"
    [[ "$got" == "$2" ]] || { echo "$3: merged layers '$got', want '$2'" >&2; return 1; }
    sleep 1
  done
}

pr_status() { # pr_status <number> <json fields>
  curl -fsS -X POST "$AFT_FAKE_GH_BASE/__pr_status" -H 'Content-Type: application/json' -d "{\"number\":$1,$2}" >/dev/null
}

merge_queue() { # merge_queue <file>: the workspace's queued stack merges
  curl -fsS "$api/git/merge-queue" > "$1"
}

# lead_merge <task>: the lead runs loom merge in its agent session.
lead_merge() {
  LOOM_AGENT_NAME=lead loom merge "$1"
}

# merge_up_to_here <change> <file>: a human presses Merge up to here on the
# change's PR (the button's API); prints the HTTP status.
merge_up_to_here() {
  curl -sS -o "$2" -w '%{http_code}' -X POST "$api/changes/$1/merge-up-to" -H 'Content-Type: application/json' \
    -d '{"actor":{"kind":"human","id":"aft-operator"}}'
}

# wait_merge_phase <change> <phase> <file>: the merge up to change reaches phase.
wait_merge_phase() {
  for _ in $(seq 1 60); do
    curl -fsS "$api/changes/$1/merge-up-to" > "$3" 2>/dev/null || true
    json "$3" 'sys.exit(0 if v.get("phase")==sys.argv[2] else 1)' "$2" 2>/dev/null && return 0
    sleep 2
  done
  echo "merge up to $1 is not $2: $(cat "$3")" >&2
  return 1
}

case "$case_name" in
  queue)
    make_tasks merge-chain "$app_repo":lead-queue-1.txt "$app_repo":lead-queue-2.txt
    approve_and_apply
    publish_stack
    one="$(pull_number 1)"
    two="$(pull_number 2)"
    task="$(cat "$case_dir/task-2.id")"
    target="$(cat "$case_dir/change-2.id")"
    # Layer one's required check is still running, so a queued merge waits.
    pr_status "$one" '"checks":"PENDING","merge_state":"BLOCKED"'

    # Lead may merge is off: the lead's loom merge is refused and nothing is queued.
    loom git-settings > "$case_dir/settings-off.txt"
    grep -q '^auto-merge: off$' "$case_dir/settings-off.txt"
    if lead_merge "$task" > "$case_dir/lead-merge-off.txt" 2>&1; then
      cat "$case_dir/lead-merge-off.txt"; echo "the lead queued a merge while Lead may merge is off" >&2; exit 1
    fi
    cat "$case_dir/lead-merge-off.txt"
    grep -q 'Lead may merge is off' "$case_dir/lead-merge-off.txt"
    merge_queue "$case_dir/queue-off.json"
    json "$case_dir/queue-off.json" 'assert v==[], v'
    hold_unmerged 4 "0 0" "refused lead merge"
    test "$(merge_puts "$one")" = 0
    browser open "$AFT_BASE_URL/ws/$workspace/settings" >/dev/null
    browser find role button click --name Git --exact >/dev/null
    browser wait '[data-testid="git-lead-may-merge"]:not([disabled])' >/dev/null
    test "$(browser eval "document.querySelector('[data-testid=\"git-lead-may-merge\"]').value" | tr -d '"')" = off
    browser screenshot "$case_dir/lead-may-merge-off-settings.png" >/dev/null
    browser open "$AFT_BASE_URL/ws/$workspace/prs" >/dev/null
    browser wait 3000 >/dev/null
    test "$(browser eval "document.querySelectorAll('[data-testid=merge-queue-entry]').length")" = 0
    browser screenshot "$case_dir/lead-may-merge-off-prs.png" >/dev/null

    # A task agent cannot use loom merge at all.
    if LOOM_TASK_ID="$task" LOOM_TASK_RUN_ID=run-1 loom merge "$task" > "$case_dir/agent-merge.txt" 2>&1; then
      echo "a task agent queued a merge" >&2; exit 1
    fi
    grep -q "lead's command" "$case_dir/agent-merge.txt"

    # A human turns Lead may merge on; one loom merge queues the whole stack.
    loom git-settings --auto-merge on > "$case_dir/settings-on.txt" 2> "$case_dir/settings-on-warning.txt"
    grep -q '^auto-merge: on$' "$case_dir/settings-on.txt"
    hold_unmerged 4 "0 0" "Lead may merge on, layer one pending"
    merge_queue "$case_dir/queue-before.json"
    json "$case_dir/queue-before.json" 'assert v==[], v'
    lead_merge "$task" > "$case_dir/lead-merge-on.txt"
    cat "$case_dir/lead-merge-on.txt"
    grep -q "merge queued: stack merge-chain up to $target" "$case_dir/lead-merge-on.txt"
    merge_queue "$case_dir/queue-on.json"
    json "$case_dir/queue-on.json" 'assert len(v)==1 and v[0]["target"]==sys.argv[2] and v[0]["queued_by"]=="lead" and v[0]["pr_number"]==int(sys.argv[3]) and v[0]["stack_id"]=="merge-chain", v' "$target" "$two"
    lead_merge "$task" > "$case_dir/lead-merge-again.txt"
    merge_queue "$case_dir/queue-again.json"
    json "$case_dir/queue-again.json" 'assert len(v)==1 and v[0]["target"]==sys.argv[2], v' "$target"
    browser open "$AFT_BASE_URL/ws/$workspace/prs" >/dev/null
    browser wait '[data-testid="merge-queue-entry"]' >/dev/null
    browser get text '[data-testid="merge-queue"]' > "$case_dir/queue-ui.txt"
    cat "$case_dir/queue-ui.txt"
    grep -q "Merge up to $target" "$case_dir/queue-ui.txt"
    grep -q "#$two" "$case_dir/queue-ui.txt"
    grep -q 'Queued by the lead' "$case_dir/queue-ui.txt"
    browser screenshot "$case_dir/lead-merge-queued-prs.png" >/dev/null
    hold_unmerged 4 "0 0" "queued merge, layer one pending"
    test "$(merge_puts "$one")" = 0

    # Checks pass: the queue lands the stack bottom up.
    pr_status "$one" '"checks":"SUCCESS","merge_state":"CLEAN"'
    wait_merged "1 1" "lead's queued merge"
    wait_merge_phase "$target" done "$case_dir/merge-done.json"
    merge_queue "$case_dir/queue-done.json"
    json "$case_dir/queue-done.json" 'assert v==[], v'
    for layer in 1 2; do git --git-dir="$case_dir/$app_repo.git" show "main:lead-queue-$layer.txt" >/dev/null; done
    loom git-settings --auto-merge off >/dev/null
    ;;

  green)
    make_tasks merge-chain "$app_repo":lead-green-1.txt "$app_repo":lead-green-2.txt
    approve_and_apply
    publish_stack
    one="$(pull_number 1)"
    two="$(pull_number 2)"
    pr_status "$one" '"checks":"PENDING","merge_state":"BLOCKED"'
    pr_status "$two" '"review":"CHANGES_REQUESTED","merge_state":"BLOCKED"'
    loom git-settings --auto-merge on > "$case_dir/policy.txt" 2> "$case_dir/policy-warning.txt"
    grep -q '^auto-merge: on$' "$case_dir/policy.txt"
    hold_unmerged 10 "0 0" "required check pending"
    test "$(merge_puts "$one")" = 0
    pr_status "$one" '"checks":"SUCCESS","merge_state":"CLEAN"'
    wait_merged "1 0" "green layer one"
    hold_unmerged 10 "1 0" "layer two changes_requested"
    test "$(merge_puts "$two")" = 0
    pr_status "$two" '"review":"APPROVED","merge_state":"CLEAN"'
    wait_merged "1 1" "layer two approved"
    loom git-settings --auto-merge off > "$case_dir/policy-off.txt"
    for layer in 1 2; do git --git-dir="$case_dir/$app_repo.git" show "main:lead-green-$layer.txt" >/dev/null; done
    ;;

  later)
    make_tasks merge-chain "$app_repo":lead-later-a.txt "$app_repo":lead-later-b.txt "$app_repo":lead-later-c.txt
    approve_and_apply
    publish_stack
    b="$(cat "$case_dir/change-2.id")"
    c="$(cat "$case_dir/change-3.id")"
    test "$(merge_up_to_here "$b" "$case_dir/merge-b.json")" = 200 || { cat "$case_dir/merge-b.json"; exit 1; }
    wait_merged "1 1 0" "merge up to B"
    wait_merge_phase "$b" done "$case_dir/status-b.json"
    json "$case_dir/status-b.json" 'assert v["backend"]=="loom" and [l["state"] for l in v["layers"]]==["done","done"], v'
    rc="$(merge_up_to_here "$c" "$case_dir/merge-c.json")"
    printf 'merge up to C after B returned %s:\n' "$rc"
    cat "$case_dir/merge-c.json"
    test "$rc" = 200
    wait_merged "1 1 1" "later merge up to C"
    git --git-dir="$case_dir/$app_repo.git" show main:lead-later-c.txt >/dev/null
    ;;

  deps)
    make_tasks deps "$api_repo":lead-deps-api.txt "$app_repo":lead-deps-app.txt
    approve_and_apply
    # The api change is published from the lead's Git panel (one change), the
    # app change as the named deps-app stack.
    curl -sS --fail-with-body -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' -d "{\"change_id\":\"$(cat "$case_dir/change-1.id")\"}" > "$case_dir/publish-1.json" || { cat "$case_dir/publish-1.json"; exit 1; }
    grep -q '"created":true' "$case_dir/publish-1.json"
    app_change="$(cat "$case_dir/change-2.id")"
    loom pr-stack deps-app lead "$app_change" > "$case_dir/publish-2.txt"
    api_number="$(json "$case_dir/publish-1.json" 'print(v["url"].rsplit("/",1)[-1])')"
    pulls_now
    app_number="$(json "$case_dir/pulls-now.json" 'print([x for x in v if x["head"]["ref"].endswith("/change/"+sys.argv[2])][0]["number"])' "$app_change")"
    app_head="$(git --git-dir="$case_dir/$app_repo.git" rev-parse "refs/heads/loom/ws/$workspace/change/$app_change")"
    for _ in $(seq 1 45); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-pending.json"
      json "$case_dir/statuses-pending.json" 'sys.exit(0 if any(x["context"]=="loom/dependencies" and x["state"]=="pending" for x in v) else 1)' && break
      sleep 2
    done
    json "$case_dir/statuses-pending.json" 'p=[x for x in v if x["context"]=="loom/dependencies"]; assert p and p[-1]["state"]=="pending" and ("owner/"+sys.argv[3]+"#"+sys.argv[2]) in p[-1]["description"] and p[-1]["repo"]=="owner/"+sys.argv[4], v' "$api_number" "$api_repo" "$app_repo"
    # A human asks Loom to merge the app before the api PR has landed: Loom
    # refuses or holds the request, and the app PR must not merge.
    early_code="$(merge_up_to_here "$app_change" "$case_dir/merge-early.json")"
    printf 'app merge requested before owner/%s#%s landed returned %s:\n' "$api_repo" "$api_number" "$early_code"
    cat "$case_dir/merge-early.json"
    hold_seconds=10
    for _ in $(seq 1 "$hold_seconds"); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-hold.json"
      json "$case_dir/statuses-hold.json" 'assert not any(x["context"]=="loom/dependencies" and x["state"]=="success" for x in v), v'
      pulls_now
      json "$case_dir/pulls-now.json" 'p=[x for x in v if x["number"]==int(sys.argv[2])][0]; assert p["state"]=="open" and not p["merged_at"], p' "$app_number"
      sleep 1
    done
    test "$(merge_puts "$app_number")" = 0
    api_change="$(cat "$case_dir/change-1.id")"
    git -C "$case_dir/$api_repo" fetch -q origin "loom/ws/$workspace/change/$api_change"
    sha="$(git -C "$case_dir/$api_repo" rev-parse FETCH_HEAD)"
    git -C "$case_dir/$api_repo" push -q origin FETCH_HEAD:refs/heads/main
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__merge" -H 'Content-Type: application/json' -d "{\"number\":$api_number,\"sha\":\"$sha\"}" > "$case_dir/api-merge.json"
    grep -q '"state":"closed"' "$case_dir/api-merge.json"
    landed=""
    for _ in $(seq 1 45); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-final.json"
      json "$case_dir/statuses-final.json" 'p=[x for x in v if x["context"]=="loom/dependencies"]; sys.exit(0 if p and p[-1]["state"]=="success" else 1)' && { landed=1; break; }
      sleep 2
    done
    if [[ -z "$landed" ]]; then
      cat "$case_dir/statuses-final.json"
      echo "loom/dependencies stayed pending after owner/$api_repo#$api_number landed" >&2
      exit 1
    fi
    # A refused early request is asked again now; a held one proceeds by itself.
    if [[ "$early_code" != 200 ]]; then
      test "$(merge_up_to_here "$app_change" "$case_dir/merge-app.json")" = 200 || { cat "$case_dir/merge-app.json"; exit 1; }
    fi
    for _ in $(seq 1 60); do
      pulls_now
      json "$case_dir/pulls-now.json" 'p=[x for x in v if x["number"]==int(sys.argv[2])][0]; sys.exit(0 if p["merged_at"] else 1)' "$app_number" && break
      sleep 2
    done
    json "$case_dir/pulls-now.json" 'p=[x for x in v if x["number"]==int(sys.argv[2])][0]; assert p["merged_at"], p' "$app_number"
    git --git-dir="$case_dir/$app_repo.git" show main:lead-deps-app.txt >/dev/null
    ;;
esac

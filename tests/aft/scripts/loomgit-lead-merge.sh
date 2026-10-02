#!/usr/bin/env bash
# Lead merge paths on a published stack (P3.11, P3.12, P3.13):
#   request  lead requests a merge, an agent cannot confirm it, a human confirms it in the UI
#   green    when_green waits for required checks, stops below changes_requested, then continues
#   later    Loom backend merges up to B, then a later request merges C
#   deps     a cross-repo dependency keeps loom/dependencies pending until its predecessor lands
set -euo pipefail

phase="$1"
case_name="$2"
case_dir="$AFT_WORK_DIR/lead-merge-$case_name"
upper="$(printf '%s' "$case_name" | tr '[:lower:]' '[:upper:]')"
workspace="E2E-WS-LEAD-$upper"
api="$AFT_BASE_URL/api/workspaces/$workspace"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

case "$case_name" in
  request | green) backend=native ;;
  *) backend=loom ;;
esac
# Each case has its own owner/<repo> so the fake forge routes every PR to the
# right bare remote even while earlier cases' PRs are still open.
app_repo="lead-$case_name"
api_repo=""
repos=("$app_repo")
if [[ "$case_name" == deps ]]; then app_repo="lead-deps-app"; api_repo="lead-deps-api"; repos=("$api_repo" "$app_repo"); fi

loom() {
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" "$@" --workspace "$workspace"
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
  loom lead-may-merge off >/dev/null 2>&1 || true
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
# area and approves the Apply-derived head, as merge-up-to does.
approve_and_apply() {
  local layer task change revision sha
  for layer in $(seq 1 "$(cat "$case_dir/layers")"); do
    task="$(cat "$case_dir/task-$layer.id")"
    read -r change revision sha < <(json "$case_dir/revisions-$layer.json" 'i=v["data"][0]; print(i["change_id"],i["number"],i["head_sha"])')
    printf '%s\n' "$change" > "$case_dir/change-$layer.id"
    curl -sS --fail-with-body -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
      -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" >/dev/null
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$revision,\"lead\":\"lead\"}" > "$case_dir/apply-$layer.json"
    grep -q '"success":true' "$case_dir/apply-$layer.json"
    curl -fsS "$api/issues/$task/revisions" > "$case_dir/applied-$layer.json"
    read -r revision sha < <(json "$case_dir/applied-$layer.json" 'i=v["data"][0]; print(i["number"],i["head_sha"])')
    curl -fsS -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
      -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" >/dev/null
  done
}

publish_stack() {
  local changes=()
  for layer in $(seq 1 "$(cat "$case_dir/layers")"); do changes+=("$(cat "$case_dir/change-$layer.id")"); done
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" pr-stack lead-chain lead "${changes[@]}" --workspace "$workspace" > "$case_dir/publish.txt"
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

case "$case_name" in
  request)
    make_tasks lead-chain "$app_repo":lead-request-1.txt "$app_repo":lead-request-2.txt
    approve_and_apply
    publish_stack
    target="$(cat "$case_dir/change-2.id")"
    curl -fsS -X POST "$api/agents/lead/git/merge-requests" -H 'Content-Type: application/json' \
      -d "{\"stack_id\":\"lead-chain\",\"target\":\"$target\",\"actor\":{\"kind\":\"lead\",\"id\":\"lead\"}}" > "$case_dir/request.json"
    json "$case_dir/request.json" 'assert v["status"]=="pending" and v["requested_kind"]=="lead" and len(v["layers"])==2, v'
    request_id="$(json "$case_dir/request.json" 'print(v["id"])')"
    hold_unmerged 6 "0 0" "lead request alone"
    for actor in '{"kind":"lead","id":"lead"}' '{"kind":"agent","id":"impl-1"}'; do
      code="$(curl -s -o "$case_dir/agent-confirm.json" -w '%{http_code}' -X POST "$api/agents/lead/git/merge-requests/$request_id/confirm" \
        -H 'Content-Type: application/json' -d "{\"actor\":$actor}")"
      test "$code" = 409
      grep -q 'confirmed by a human' "$case_dir/agent-confirm.json"
    done
    hold_unmerged 4 "0 0" "agent confirmation"
    test "$(merge_puts "$(pull_number 2)")" = 0
    agent-browser --session "$AFT_SESSION" open "$AFT_BASE_URL/ws/$workspace/agents/lead" >/dev/null
    agent-browser --session "$AFT_SESSION" find role button click --name Git --exact >/dev/null
    agent-browser --session "$AFT_SESSION" wait --text "asks to merge lead-chain up to $target" >/dev/null
    agent-browser --session "$AFT_SESSION" screenshot "$case_dir/request-card.png" >/dev/null
    agent-browser --session "$AFT_SESSION" find role button click --name "Confirm merge up to $target" >/dev/null
    wait_merged "1 1" "human-confirmed merge"
    curl -fsS "$api/agents/lead/git/merge-requests" > "$case_dir/requests-after.json"
    json "$case_dir/requests-after.json" 'r=[x for x in v if x["id"]==sys.argv[2]][0]; assert r["status"]=="confirmed" and r["confirmed_by"] and r["requested_by"]=="lead", r; assert "requested by lead lead, confirmed by "+r["confirmed_by"] in r["audit"], r' "$request_id"
    for layer in 1 2; do git --git-dir="$case_dir/$app_repo.git" show "main:lead-request-$layer.txt" >/dev/null; done
    ;;

  green)
    make_tasks lead-chain "$app_repo":lead-green-1.txt "$app_repo":lead-green-2.txt
    approve_and_apply
    publish_stack
    one="$(pull_number 1)"
    two="$(pull_number 2)"
    pr_status "$one" '"checks":"PENDING","merge_state":"BLOCKED"'
    pr_status "$two" '"review":"CHANGES_REQUESTED","merge_state":"BLOCKED"'
    loom lead-may-merge when_green > "$case_dir/policy.txt" 2> "$case_dir/policy-warning.txt"
    grep -q when_green "$case_dir/policy.txt"
    hold_unmerged 10 "0 0" "required check pending"
    test "$(merge_puts "$one")" = 0
    pr_status "$one" '"checks":"SUCCESS","merge_state":"CLEAN"'
    wait_merged "1 0" "green layer one"
    hold_unmerged 10 "1 0" "layer two changes_requested"
    test "$(merge_puts "$two")" = 0
    pr_status "$two" '"review":"APPROVED","merge_state":"CLEAN"'
    wait_merged "1 1" "layer two approved"
    loom lead-may-merge off > "$case_dir/policy-off.txt"
    for layer in 1 2; do git --git-dir="$case_dir/$app_repo.git" show "main:lead-green-$layer.txt" >/dev/null; done
    ;;

  later)
    make_tasks lead-chain "$app_repo":lead-later-a.txt "$app_repo":lead-later-b.txt "$app_repo":lead-later-c.txt
    approve_and_apply
    publish_stack
    b="$(cat "$case_dir/change-2.id")"
    c="$(cat "$case_dir/change-3.id")"
    printf 'merge %s\n' "$b" | loom merge-up-to lead-chain lead "$b" > "$case_dir/merge-b.txt"
    wait_merged "1 1 0" "merge up to B"
    for _ in $(seq 1 60); do
      loom merge-up-to lead-chain lead "$b" --status > "$case_dir/status-b.txt" 2>&1 || true
      grep -q '^merge: done' "$case_dir/status-b.txt" && break
      sleep 2
    done
    grep -q '^merge: done' "$case_dir/status-b.txt"
    set +e
    printf 'merge %s\n' "$c" | loom merge-up-to lead-chain lead "$c" > "$case_dir/merge-c.txt" 2>&1
    rc=$?
    set -e
    printf 'merge up to C after B exited %s:\n' "$rc"
    cat "$case_dir/merge-c.txt"
    test "$rc" = 0
    wait_merged "1 1 1" "later merge up to C"
    git --git-dir="$case_dir/$app_repo.git" show main:lead-later-c.txt >/dev/null
    ;;

  deps)
    make_tasks deps "$api_repo":lead-deps-api.txt "$app_repo":lead-deps-app.txt
    approve_and_apply
    for layer in 1 2; do
      change="$(cat "$case_dir/change-$layer.id")"
      curl -sS --fail-with-body -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' -d "{\"change_id\":\"$change\"}" > "$case_dir/publish-$layer.json" || { cat "$case_dir/publish-$layer.json"; exit 1; }
      grep -q '"created":true' "$case_dir/publish-$layer.json"
    done
    api_number="$(json "$case_dir/publish-1.json" 'print(v["url"].rsplit("/",1)[-1])')"
    app_change="$(cat "$case_dir/change-2.id")"
    app_head="$(git --git-dir="$case_dir/$app_repo.git" rev-parse "refs/heads/loom/ws/$workspace/change/$app_change")"
    for _ in $(seq 1 45); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-pending.json"
      json "$case_dir/statuses-pending.json" 'sys.exit(0 if any(x["context"]=="loom/dependencies" and x["state"]=="pending" for x in v) else 1)' && break
      sleep 2
    done
    json "$case_dir/statuses-pending.json" 'p=[x for x in v if x["context"]=="loom/dependencies"]; assert p and p[-1]["state"]=="pending" and ("owner/"+sys.argv[3]+"#"+sys.argv[2]) in p[-1]["description"] and p[-1]["repo"]=="owner/"+sys.argv[4], v' "$api_number" "$api_repo" "$app_repo"
    hold_seconds=6
    for _ in $(seq 1 "$hold_seconds"); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-hold.json"
      json "$case_dir/statuses-hold.json" 'assert not any(x["context"]=="loom/dependencies" and x["state"]=="success" for x in v), v'
      sleep 1
    done
    api_change="$(cat "$case_dir/change-1.id")"
    git -C "$case_dir/$api_repo" fetch -q origin "loom/ws/$workspace/change/$api_change"
    sha="$(git -C "$case_dir/$api_repo" rev-parse FETCH_HEAD)"
    git -C "$case_dir/$api_repo" push -q origin FETCH_HEAD:refs/heads/main
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__merge" -H 'Content-Type: application/json' -d "{\"number\":$api_number,\"sha\":\"$sha\"}" > "$case_dir/api-merge.json"
    grep -q '"state":"closed"' "$case_dir/api-merge.json"
    for _ in $(seq 1 45); do
      curl -fsS "$AFT_FAKE_GH_BASE/__statuses?sha=$app_head" > "$case_dir/statuses-final.json"
      json "$case_dir/statuses-final.json" 'p=[x for x in v if x["context"]=="loom/dependencies"]; sys.exit(0 if p and p[-1]["state"]=="success" else 1)' && exit 0
      sleep 2
    done
    cat "$case_dir/statuses-final.json"
    echo "loom/dependencies stayed pending after owner/$api_repo#$api_number landed" >&2
    exit 1
    ;;
esac

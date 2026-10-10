#!/usr/bin/env bash
set -euo pipefail
source "$AFT_TESTS_DIR/scripts/loomgit-lib.sh"

phase="$1"
case_dir="$AFT_WORK_DIR/merge-stale"
workspace="E2E-WS-MERGE-STALE"
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo="$case_dir/repo"
remote="$case_dir/origin.git"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

if [[ "$phase" == setup ]]; then
  mkdir -p "$case_dir"
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$repo"
  printf 'base\n' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
  git -C "$repo" remote add origin git@github.com:owner/repo.git
  git -C "$repo" push -q origin main
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"native_stacks":False}))' "$remote" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"e2e-ws-merge-stale\",\"type\":\"empty\",\"repos\":[\"$repo\"]}" >/dev/null
  curl -fsS -X POST "$api/agents" -H 'Content-Type: application/json' \
    -d '{"name":"lead","role_name":"lead","auto":false,"cross_repo":true,"repos":[],"backend":"codex"}' >/dev/null
  exit 0
fi

if [[ "$phase" == teardown ]]; then
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh"
  curl -s -X DELETE "$api" >/dev/null || true
  exit 0
fi

if [[ "$phase" == seed ]]; then
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"stale merge epic $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  epic="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/epic.json")"
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"stale merge task $RUN_ID\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"STUB_CODEX_PATCH=merge-stale.txt\"}" > "$case_dir/task.json"
  task="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/task.json")"
  printf '%s\n' "$task" > "$case_dir/task.id"
  loom stack init stale-card --repo repo --base main >/dev/null
  loom stack add "$task" --stack stale-card >/dev/null
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" >/dev/null
  for attempt in $(seq 1 45); do
    curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions.json"
    if grep -q '"head_sha"' "$case_dir/revisions.json"; then break; fi
    sleep 2
  done
  grep -q '"head_sha"' "$case_dir/revisions.json" || { echo "no revision after $attempt polls" >&2; exit 1; }
  read -r change revision head < <(python3 -c 'import json,sys; item=json.load(open(sys.argv[1]))["data"][0]; print(item["change_id"],item["number"],item["head_sha"])' "$case_dir/revisions.json")
  printf '%s\n' "$change" > "$case_dir/change.id"
  curl -fsS -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$head\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" > "$case_dir/approve.json"
  grep -q '"status":"applied"' "$case_dir/approve.json"
  test "$(git -C "$repo" rev-parse refs/heads/loom/ws/$workspace/interactive/lead)" = "$head"
  loom pr-stack stale-card lead "$change" --workspace "$workspace" > "$case_dir/publish-before.txt"
  curl -fsS "$api/changes/$change/merge-up-to" > "$case_dir/preview-before.json"
  python3 -c 'import json,sys; view=json.load(open(sys.argv[1])); assert view["backend"]=="loom" and not view["phase"] and len(view["layers"])==1, view' "$case_dir/preview-before.json"
  exit 0
fi

if [[ "$phase" == advance ]]; then
  change="$(cat "$case_dir/change.id")"
  old_head="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["layers"][0]["head"])' "$case_dir/preview-before.json")"
  printf 'foreign trunk edit\n' > "$repo/foreign.txt"
  git -C "$repo" add foreign.txt
  git -C "$repo" -c user.name=Foreign -c user.email=foreign@example.test commit -q -m 'foreign trunk advance'
  git -C "$repo" push -q origin main
  git --git-dir="$remote" rev-parse refs/heads/main > "$case_dir/trunk-after"
  curl -fsS -X POST "$api/agents/lead/git/sync" > "$case_dir/sync.json"
  task="$(cat "$case_dir/task.id")"
  curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-after.json"
  head="$(python3 -c 'import json,sys; item=json.load(open(sys.argv[1]))["data"][0]; assert item["verdict"]=="carried", item; print(item["head_sha"])' "$case_dir/revisions-after.json")"
  test "$head" != "$old_head"
  loom pr-stack stale-card lead "$change" --workspace "$workspace" > "$case_dir/publish-after.txt"
  curl -fsS "$api/changes/$change/merge-up-to" > "$case_dir/preview-after.json"
  python3 -c 'import json,sys; before=json.load(open(sys.argv[1])); after=json.load(open(sys.argv[2])); assert before["layers"][0]["head"]!=after["layers"][0]["head"] and not after["phase"], (before,after)' "$case_dir/preview-before.json" "$case_dir/preview-after.json"
  test "$(git --git-dir="$remote" rev-parse "refs/heads/loom/ws/$workspace/change/$change")" = "$head"
  printf '%s\n' "$head" > "$case_dir/head-after"
  exit 0
fi

# There is no confirmation step to go stale (D38): Merge up to here queues the
# merge of the head now on the PR, and the old head never lands.
if [[ "$phase" == verify ]]; then
  change="$(cat "$case_dir/change.id")"
  old_head="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["layers"][0]["head"])' "$case_dir/preview-before.json")"
  new_head="$(cat "$case_dir/head-after")"
  curl -sS --fail-with-body -X POST "$api/changes/$change/merge-up-to" -H 'Content-Type: application/json' \
    -d '{"actor":{"kind":"human","id":"aft"}}' > "$case_dir/queued.json"
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); assert v["layers"][0]["head"]==sys.argv[2], v' "$case_dir/queued.json" "$new_head"
  for attempt in $(seq 1 60); do
    curl -fsS "$api/changes/$change/merge-up-to" > "$case_dir/final.json"
    python3 -c 'import json,sys; sys.exit(0 if json.load(open(sys.argv[1]))["phase"]=="done" else 1)' "$case_dir/final.json" && break
    sleep 2
  done
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); assert v["phase"]=="done", v' "$case_dir/final.json"
  git --git-dir="$remote" merge-base --is-ancestor "$(cat "$case_dir/trunk-after")" refs/heads/main
  git --git-dir="$remote" show main:merge-stale.txt >/dev/null
  git --git-dir="$remote" show main:foreign.txt >/dev/null
  test "$(git --git-dir="$remote" rev-parse "main^{tree}")" = "$(git --git-dir="$remote" rev-parse "$new_head^{tree}")"
  test "$(git --git-dir="$remote" rev-parse "main^{tree}")" != "$(git --git-dir="$remote" rev-parse "$old_head^{tree}")"
  exit 0
fi

echo "unknown phase: $phase" >&2
exit 1

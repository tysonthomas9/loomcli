#!/usr/bin/env bash
set -euo pipefail

phase="$1"
backend="$2"
case_dir="$AFT_WORK_DIR/merge-$backend"
if [[ "$backend" == native ]]; then workspace="E2E-WS-MERGE-NATIVE"; else workspace="E2E-WS-MERGE-LOOM"; fi
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo="$case_dir/merge-repo"
remote="$case_dir/origin.git"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

loom() {
  LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" --workspace "$workspace" "$@"
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
  git -C "$repo" remote add origin git@github.com:owner/repo.git
  git -C "$repo" push -q origin main
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"native_stacks":sys.argv[2]=="native","preserve":sys.argv[2]=="loom"}))' "$remote" "$backend" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"e2e-ws-merge-$backend\",\"type\":\"empty\",\"repos\":[\"$repo\"]}" >/dev/null
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

curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
  -d "{\"title\":\"merge epic $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
epic="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/epic.json")"
previous=""
loom stack init aft-chain --repo merge-repo --base main >/dev/null
for layer in 1 2 3 4; do
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"merge layer $layer $RUN_ID\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"STUB_CODEX_PATCH=merge-$layer.txt\"}" > "$case_dir/task-$layer.json"
  task="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/task-$layer.json")"
  printf '%s\n' "$task" > "$case_dir/task-$layer.id"
  if [[ -n "$previous" ]]; then
    curl -fsS -X POST "$api/issues/$task/dependencies" -H 'Content-Type: application/json' \
      -d "{\"depends_on_id\":\"$previous\",\"dep_type\":\"blocks\"}" >/dev/null
    loom stack add "$task" --stack aft-chain --after "$previous" >/dev/null
  else
    loom stack add "$task" --stack aft-chain >/dev/null
  fi
  previous="$task"
done
curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
  -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$case_dir/workflow.json"

for layer in 1 2 3 4; do
  task="$(cat "$case_dir/task-$layer.id")"
  for attempt in $(seq 1 90); do
    curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$layer.json"
    if grep -q '"head_sha"' "$case_dir/revisions-$layer.json"; then break; fi
    sleep 2
  done
  grep -q '"head_sha"' "$case_dir/revisions-$layer.json"
done

for layer in 1 2 3 4; do
  task="$(cat "$case_dir/task-$layer.id")"
  read -r change revision sha < <(python3 -c 'import json,sys; item=json.load(open(sys.argv[1]))["data"][0]; print(item["change_id"],item["number"],item["head_sha"])' "$case_dir/revisions-$layer.json")
  printf '%s\n' "$change" > "$case_dir/change-$layer.id"
  curl -sS --fail-with-body -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" > "$case_dir/verdict-$layer.json"
  curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
    -d "{\"change\":\"$change\",\"revision\":$revision,\"lead\":\"lead\"}" > "$case_dir/apply-$layer.json"
  grep -q '"success":true' "$case_dir/apply-$layer.json"
done

for layer in 1 2 3 4; do
  task="$(cat "$case_dir/task-$layer.id")"
  change="$(cat "$case_dir/change-$layer.id")"
  curl -fsS "$api/issues/$task/revisions" > "$case_dir/applied-revisions-$layer.json"
  read -r applied_revision applied_sha < <(python3 -c 'import json,sys; item=json.load(open(sys.argv[1]))["data"][0]; print(item["number"],item["head_sha"])' "$case_dir/applied-revisions-$layer.json")
  curl -fsS -X POST "$api/changes/$change/revisions/$applied_revision/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$applied_sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" >/dev/null
done

LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" pr-stack aft-chain lead \
  "$(cat "$case_dir/change-1.id")" "$(cat "$case_dir/change-2.id")" \
  "$(cat "$case_dir/change-3.id")" "$(cat "$case_dir/change-4.id")" \
  --workspace "$workspace" > "$case_dir/publish.txt"
curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-before.json"
python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert len(p)==4, p; assert [x["base"]["ref"] for x in p]==["main"]+[x["head"]["ref"] for x in p[:3]], p' "$case_dir/pulls-before.json"
target="$(cat "$case_dir/change-3.id")"
curl -fsS "$api/agents/lead/git/merge-up-to?stack_id=aft-chain&target=$target" > "$case_dir/preview.json"
python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); assert len(v["layers"])==4 and v["backend"]==sys.argv[2], v' "$case_dir/preview.json" "$backend"

if [[ "$backend" == native ]]; then
  python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); print(json.dumps({"stack_id":"aft-chain","target":sys.argv[2],"heads":[x["head"] for x in v["layers"]]}))' "$case_dir/preview.json" "$target" |
    curl -fsS -X POST "$api/agents/lead/git/merge-up-to" -H 'Content-Type: application/json' -d @- > "$case_dir/request.json"
else
  printf 'merge %s\n' "$target" | LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" merge-up-to aft-chain lead "$target" --workspace "$workspace" > "$case_dir/request.txt"
fi

for attempt in $(seq 1 90); do
  curl -fsS "$api/agents/lead/git/merge-up-to?stack_id=aft-chain&target=$target" > "$case_dir/final.json"
  if python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); sys.exit(0 if v["phase"]=="done" else 1)' "$case_dir/final.json"; then break; fi
  sleep 2
done
printf 'merge status after %s polls: ' "$attempt"
python3 -c 'import json,sys; v=json.load(open(sys.argv[1])); assert v["phase"]=="done", v; assert [x["state"] for x in v["layers"][:3]]==["done"]*3, v' "$case_dir/final.json"
agent-browser --session "$AFT_SESSION" open "$AFT_BASE_URL/ws/$workspace/agents/lead" >/dev/null
agent-browser --session "$AFT_SESSION" find role button click --name Git --exact >/dev/null
agent-browser --session "$AFT_SESSION" find role button click --name 'Merge stack' >/dev/null
agent-browser --session "$AFT_SESSION" find label 'Stack ID' fill aft-chain >/dev/null
agent-browser --session "$AFT_SESSION" find label 'Up to layer' fill "$target" >/dev/null
agent-browser --session "$AFT_SESSION" find role button click --name 'Show merge state' >/dev/null
agent-browser --session "$AFT_SESSION" wait --text "$backend merge: done" >/dev/null
agent-browser --session "$AFT_SESSION" get text body > "$case_dir/ui-state.txt"
for layer in 1 2 3 4; do
  change="$(cat "$case_dir/change-$layer.id")"
  grep -q "$change:" "$case_dir/ui-state.txt"
done
agent-browser --session "$AFT_SESSION" screenshot "$case_dir/merge-ui.png" >/dev/null
curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-after.json"
python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert all(x["merged_at"] for x in p[:3]), p; assert p[3]["state"]=="open" and p[3]["base"]["ref"]=="main", p' "$case_dir/pulls-after.json"
for layer in 1 2 3; do git --git-dir="$remote" show "main:merge-$layer.txt" >/dev/null; done
if git --git-dir="$remote" show main:merge-4.txt >/dev/null 2>&1; then exit 1; fi

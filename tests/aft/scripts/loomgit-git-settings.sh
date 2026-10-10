#!/usr/bin/env bash
# P2.20: the Settings Git section (delivery mode, lead may approve, lead may
# merge) reads and writes the server, follows the CLI, keeps the two lead
# permissions human only, and a mode switch leaves open PRs alone.
# S3 (AFT-CLI1): loom --help lists only the small Git CLI, loom git-settings
# changes the same three settings as the UI, and the hidden loom apply works.
set -euo pipefail
source "$AFT_TESTS_DIR/scripts/loomgit-lib.sh"

phase="$1"
case_name="$2"
case_dir="$AFT_WORK_DIR/git-settings-$case_name"
if [[ "$case_name" == ui ]]; then workspace="E2E-WS-GITSET-UI"; else workspace="E2E-WS-GITSET-SWITCH"; fi
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo="$case_dir/settings-repo"
remote="$case_dir/origin.git"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

# loom_cli runs the CLI as a human at a shell (no agent markers).
loom_cli() {
  env -u LOOM_AGENT_NAME -u LOOM_ORCHESTRATOR_SESSION_ID -u LOOM_AGENT_TERMINAL_ID \
    LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" "$@"
}

git_settings() {
  loom_cli git-settings --workspace "$workspace" "$@"
}

# expect_cli_settings checks loom git-settings prints delivery, auto-merge and
# lead-may-approve as given.
expect_cli_settings() {
  git_settings > "$case_dir/cli-settings.txt"
  printf 'delivery: %s\nauto-merge: %s\nlead-may-approve: %s\n' "$1" "$2" "$3" | diff -u - "$case_dir/cli-settings.txt"
}

settings() {
  curl -fsS "$api/git/settings"
}

expect_settings() {
  settings > "$case_dir/settings.json"
  python3 -c 'import json,sys; s=json.load(open(sys.argv[1])); want=dict(delivery_mode=sys.argv[2],lead_may_approve_publish=sys.argv[3]=="true",lead_may_merge=sys.argv[4]); assert s==want, (s, want)' \
    "$case_dir/settings.json" "$1" "$2" "$3"
}

put_settings() {
  curl -sS -o "$case_dir/put.json" -w '%{http_code}' -X PUT "$api/git/settings" -H 'Content-Type: application/json' -d "$1"
}

open_git_settings() {
  browser open "$AFT_BASE_URL/ws/$workspace/settings" >/dev/null
  browser find role button click --name Git --exact >/dev/null
  browser wait '[data-testid="git-delivery-mode"]:not([disabled])' >/dev/null
}

ui_select() {
  browser select "[data-testid=\"$1\"]" "$2" >/dev/null
  browser wait "[data-testid=\"$1\"]:not([disabled])" >/dev/null
}

ui_value() {
  browser eval "document.querySelector('[data-testid=\"$1\"]').value" | tr -d '"'
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
  git -C "$repo" remote add origin "git@github.com:owner/gitset-$case_name.git"
  git -C "$repo" push -q origin main
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"remotes":{"owner/gitset-"+sys.argv[2]:sys.argv[1]},"native_stacks":False,"preserve":True}))' "$remote" "$case_name" |
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

# Revisions are recorded in the journal the settings live in, so drive one task
# through the product before reading settings on a fresh workspace.
run_tasks() {
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"settings epic $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  epic="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/epic.json")"
  for name in "$@"; do
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
      -d "{\"title\":\"settings $name $RUN_ID\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"STUB_CODEX_PATCH=settings-$name.txt\"}" > "$case_dir/task-$name.json"
    python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"]["id"])' "$case_dir/task-$name.json" > "$case_dir/task-$name.id"
  done
  if [[ $# -gt 1 ]]; then
    loom stack init aft-settings --repo settings-repo --base main >/dev/null
    loom stack add "$(cat "$case_dir/task-$1.id")" --stack aft-settings >/dev/null
    curl -fsS -X POST "$api/issues/$(cat "$case_dir/task-$2.id")/dependencies" -H 'Content-Type: application/json' \
      -d "{\"depends_on_id\":\"$(cat "$case_dir/task-$1.id")\",\"dep_type\":\"blocks\"}" >/dev/null
    loom stack add "$(cat "$case_dir/task-$2.id")" --stack aft-settings --after "$(cat "$case_dir/task-$1.id")" >/dev/null
  fi
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$case_dir/workflow.json"
}

# wait_task <name>: the task records a reviewable revision. A stacked task runs
# only once the task below it is approved (P1.26).
wait_task() {
  task="$(cat "$case_dir/task-$1.id")"
  for _ in $(seq 1 90); do
    curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$1.json"
    if grep -q '"head_sha"' "$case_dir/revisions-$1.json"; then return 0; fi
    sleep 2
  done
  echo "task $1 recorded no revision" >&2
  return 1
}

approve() {
  read -r change revision sha < <(python3 -c 'import json,sys; item=json.load(open(sys.argv[1]))["data"][0]; print(item["change_id"],item["number"],item["head_sha"])' "$1")
  printf '%s\n' "$change" > "$2"
  curl -sS --fail-with-body -X POST "$api/changes/$change/revisions/$revision/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" > "$2.verdict.json"
}

if [[ "$case_name" == ui ]]; then
  run_tasks only
  wait_task only
  expect_settings stack true off
  open_git_settings
  test "$(ui_value git-delivery-mode)" = stack
  test "$(ui_value git-lead-may-approve)" = on
  test "$(ui_value git-lead-may-merge)" = off
  browser get text '[data-testid="git-settings-panel"]' > "$case_dir/ui-defaults.txt"
  grep -q 'Stacked PRs' "$case_dir/ui-defaults.txt"
  browser screenshot "$case_dir/ui-defaults.png" >/dev/null

  ui_select git-delivery-mode trunk
  ui_select git-lead-may-approve off
  ui_select git-lead-may-merge when_green
  browser wait '[data-testid="git-merge-warning"]' >/dev/null
  expect_settings trunk false when_green
  open_git_settings
  test "$(ui_value git-delivery-mode)" = trunk
  test "$(ui_value git-lead-may-approve)" = off
  test "$(ui_value git-lead-may-merge)" = when_green
  expect_cli_settings pr-per-task on off
  browser screenshot "$case_dir/ui-saved.png" >/dev/null

  # The CLI changes the same three settings the UI shows.
  git_settings --delivery stack --auto-merge off --lead-may-approve on > "$case_dir/cli-set.txt"
  expect_cli_settings stack off on
  expect_settings stack true off
  open_git_settings
  test "$(ui_value git-delivery-mode)" = stack
  test "$(ui_value git-lead-may-approve)" = on
  test "$(ui_value git-lead-may-merge)" = off
  browser screenshot "$case_dir/ui-after-cli.png" >/dev/null
  git_settings --lead-may-approve off >/dev/null

  # The lead may change delivery only, from the CLI as from the API.
  lead_cli() { LOOM_AGENT_NAME=lead LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" git-settings --workspace "$workspace" "$@"; }
  if lead_cli --auto-merge on > "$case_dir/lead-cli.txt" 2>&1; then echo "lead turned auto-merge on" >&2; exit 1; fi
  grep -q 'only a human' "$case_dir/lead-cli.txt"
  expect_cli_settings stack off off

  # loom --help lists only the small Git CLI; the plumbing is hidden but runs.
  loom_cli --help > "$case_dir/help.txt"
  python3 - "$case_dir/help.txt" <<'PY'
import sys
text = open(sys.argv[1]).read()
section = text.split("Git Operations:\n", 1)[1].split("\n\n", 1)[0]
shown = sorted(line.split()[0] for line in section.splitlines())
assert shown == ["abandon", "approve", "git-settings", "merge", "reject", "sync", "unapply"], shown
PY
  for hidden in apply pr pr-stack pull restack; do loom_cli "$hidden" --help >/dev/null; done
  for gone in push delivery-mode lead-may-merge retention-sweep merge-up-to request-merge confirm-merge; do
    loom_cli "$gone" > "$case_dir/gone-$gone.txt" 2>&1 || true
    grep -q "unknown command \"$gone\"" "$case_dir/gone-$gone.txt"
  done

  test "$(put_settings '{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"trunk"}')" = 200
  test "$(put_settings '{"actor":{"kind":"lead","id":"lead"},"lead_may_approve_publish":true}')" = 403
  test "$(put_settings '{"actor":{"kind":"lead","id":"lead"},"lead_may_merge":"when_green"}')" = 403
  test "$(put_settings '{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"stack","lead_may_merge":"when_green"}')" = 403
  expect_settings trunk false off
  exit 0
fi

# Mode switch: a stacked pair is published, the lead switches to PR per task,
# the next approved task gets its own PR to trunk and the open PRs are unchanged.
run_tasks a b c
expect_settings stack true off
for name in a b; do
  wait_task "$name"
  approve "$case_dir/revisions-$name.json" "$case_dir/change-$name.id"
  change="$(cat "$case_dir/change-$name.id")"
  revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["data"][0]["number"])' "$case_dir/revisions-$name.json")"
  if [[ "$name" == a ]]; then
    # S3: the hidden loom apply (was push) applies an approved revision.
    loom_cli apply "$change" "$revision" --lead lead --workspace "$workspace" > "$case_dir/apply-$name.txt"
    grep -q 'Applied revision to local working area' "$case_dir/apply-$name.txt"
  else
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$revision,\"lead\":\"lead\"}" > "$case_dir/apply-$name.json"
    grep -q '"success":true' "$case_dir/apply-$name.json"
  fi
  curl -fsS "$api/issues/$(cat "$case_dir/task-$name.id")/revisions" > "$case_dir/applied-$name.json"
  approve "$case_dir/applied-$name.json" "$case_dir/applied-change-$name.id"
done
loom pr-stack aft-settings lead "$(cat "$case_dir/change-a.id")" "$(cat "$case_dir/change-b.id")" > "$case_dir/publish.txt"
curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-before.json"
python3 -c 'import json,sys; p=json.load(open(sys.argv[1])); assert len(p)==2, p; assert p[0]["base"]["ref"]=="main" and p[1]["base"]["ref"]==p[0]["head"]["ref"], p' "$case_dir/pulls-before.json"

test "$(put_settings '{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"trunk"}')" = 200
expect_settings trunk true off
expect_cli_settings pr-per-task off on

wait_task c
approve "$case_dir/revisions-c.json" "$case_dir/change-c.id"
change="$(cat "$case_dir/change-c.id")"
curl -sS --fail-with-body -X POST "$api/agents/lead/git/pr" -H 'Content-Type: application/json' \
  -d "{\"change_id\":\"$change\"}" > "$case_dir/publish-c.json"
curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-after.json"
python3 - "$case_dir/pulls-before.json" "$case_dir/pulls-after.json" "$change" <<'PY'
import json, sys
before, after, change = json.load(open(sys.argv[1])), json.load(open(sys.argv[2])), sys.argv[3]
key = lambda pr: (pr["number"], pr["head"]["ref"], pr["base"]["ref"], pr["state"], pr["head"].get("sha"))
assert [key(pr) for pr in after[:2]] == [key(pr) for pr in before], (before, after)
assert len(after) == 3, after
assert after[2]["base"]["ref"] == "main" and change in after[2]["head"]["ref"], after[2]
PY

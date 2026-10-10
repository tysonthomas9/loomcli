#!/usr/bin/env bash
# P1.28 (D23): a rerun of a daemon-managed agent starts from the lead's current
# working area. The walk-through repro (F7): the coder's attempt is approved,
# then unapplied; the task is reopened and the daemon reruns it in the agent's
# reused checkout. The new attempt's base must be the lead's head, not the
# agent's leftover commit, so approving it applies without a conflict.
#
# The scenario owns a real `loom daemon` for its workspace, with one coder on
# the deterministic local-mode backend (test/local-mode/loom-backend-localdogfood):
# it appends five lines to local-mode-agent-output.txt, commits, and closes.
set -euo pipefail

phase="$1"
workspace="$2"
api="$AFT_BASE_URL/api/workspaces/$workspace"
lower="$(printf '%s' "$workspace" | tr '[:upper:]' '[:lower:]')"
source_repo="$AFT_WORK_DIR/$lower-repo"
remote="$AFT_WORK_DIR/$lower-origin.git"
work="$AFT_WORK_DIR/$workspace"
bin="$AFT_WORK_DIR/$lower-bin"
repo_root="$(cd "$AFT_TESTS_DIR/../.." && pwd)"
daemon_log="${AFT_REPORT_DIR:-$AFT_WORK_DIR}/$lower-daemon.log"
output_file="local-mode-agent-output.txt"

# The workspace's own repo name and checkout, read from the API at setup.
repo_name="$(cat "$work-repo-name" 2>/dev/null || true)"
repo="$(cat "$work-repo-path" 2>/dev/null || true)"

loom_cli() {
  LOOM_WORKSPACE="$workspace" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" PATH="$bin:$PATH" "$AFT_LOOM_BIN" "$@"
}
id_of() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])'; }
field() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; print(d[sys.argv[2]])' "$@"; }
issue() { curl -fsS "$api/issues/$1" > "$work-issue.json"; }
wait_status() {
  # wait_status <id> <status>
  for _ in $(seq 1 90); do
    issue "$1"
    [[ "$(field "$work-issue.json" status)" == "$2" ]] && return 0
    sleep 2
  done
  echo "task $1 is '$(field "$work-issue.json" status)', want '$2'" >&2
  return 1
}
# revision <task> <number>: prints "<change> <head>" of that revision once frozen.
revision() {
  for _ in $(seq 1 120); do
    curl -fsS "$api/issues/$1/revisions" > "$work-revisions.json"
    if out="$(python3 - "$work-revisions.json" "$2" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
for r in data:
    if r["number"] == int(sys.argv[2]) and r.get("head_sha"):
        print(r["change_id"], r["head_sha"])
        sys.exit(0)
sys.exit(1)
PY
)"; then
      printf '%s\n' "$out"
      return 0
    fi
    sleep 2
  done
  echo "task $1 recorded no revision $2" >&2
  cat "$work-revisions.json" >&2
  tail -60 "$daemon_log" >&2 || true
  return 1
}
lead_head() { git -C "$repo" rev-parse "refs/heads/loom/ws/$workspace/interactive/lead"; }
stop_daemon() {
  [[ -f "$work-daemon.pid" ]] || return 0
  pid="$(cat "$work-daemon.pid")"
  rm -f "$work-daemon.pid"
  [[ "$pid" =~ ^[1-9][0-9]*$ ]] || return 0
  kill -INT "$pid" 2>/dev/null || return 0
  for _ in $(seq 1 45); do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 1
  done
  kill -KILL "$pid" 2>/dev/null || true
}

case "$phase" in
setup)
  # setup <ws> <stack|trunk>: repo, workspace, one auto coder, and its daemon.
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$source_repo"
  printf 'base\n' > "$source_repo/README.md"
  git -C "$source_repo" add README.md
  git -C "$source_repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  # A plain local origin: the rerun path needs no Git provider.
  git -C "$source_repo" remote add origin "$remote"
  git -C "$source_repo" push -q origin main
  curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' \
    -d "{\"name\":\"$lower\",\"type\":\"empty\",\"repos\":[\"$source_repo\"]}" >/dev/null
  curl -fsS "$api" > "$work-workspace.json"
  python3 - "$work-workspace.json" "$work" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
d = d.get("data", d)
repo = d["repos"][0]
open(sys.argv[2] + "-ws-path", "w").write(d["path"] + "\n")
open(sys.argv[2] + "-repo-name", "w").write(repo["name"] + "\n")
open(sys.argv[2] + "-repo-path", "w").write(repo["path"] + "\n")
PY
  repo_name="$(cat "$work-repo-name")"
  repo="$(cat "$work-repo-path")"
  if [[ "${3:-stack}" == trunk ]]; then
    loom_cli delivery-mode trunk --workspace "$workspace" >/dev/null
  fi
  mkdir -p "$bin"
  ln -sf "$AFT_LOOM_BIN" "$bin/loom"
  ln -sf "$repo_root/test/local-mode/loom-backend-localdogfood" "$bin/loom-backend-localdogfood"
  loom_cli agentdef add rerun-coder --role task --auto --backend localdogfood --repos "$repo_name" >/dev/null
  ws_path="$(cat "$work-ws-path")"
  [[ -d "$ws_path" ]] || { echo "workspace $workspace has no local path: '$ws_path'" >&2; cat "$work-workspace.json" >&2; exit 1; }
  : > "$daemon_log"
  (
    cd "$ws_path"
    exec env -u LOOM_WORKSPACE_RUNTIME_DIR -u LOOM_AGENT_NAME -u LOOM_AGENT_ROLE -u LOOM_SESSION_ID \
      -u LOOM_FLEET_DB_URL -u GITHUB_TOKEN -u GH_TOKEN \
      PATH="$bin:$PATH" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" LOOM_WORKSPACE="$workspace" \
      LOOM_SERVER_URL="${AFT_API_URL:-$AFT_BASE_URL}" LOOM_ISSUE_BACKEND=fleetdb \
      LOOM_FLEET_DB_ACTOR="loom-aft-rerun-$lower" LOOM_LOCAL_MODE_STEP_DELAY=0 \
      "$AFT_LOOM_BIN" daemon
  ) >>"$daemon_log" 2>&1 &
  printf '%s\n' "$!" > "$work-daemon.pid"
  for _ in $(seq 1 30); do
    grep -q "Loom Agent Supervisor" "$daemon_log" && exit 0
    kill -0 "$(cat "$work-daemon.pid")" 2>/dev/null || break
    sleep 1
  done
  echo "daemon for $workspace did not start" >&2
  tail -40 "$daemon_log" >&2
  exit 1
  ;;
teardown)
  stop_daemon
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh" || true
  curl -s -X DELETE "$api" >/dev/null || true
  ;;
attempt)
  # attempt <ws>: a task the daemon's coder claims; its attempt freezes revision 1.
  task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"rerun from lead\",\"issue_type\":\"task\",\"priority\":0,\"source_repo\":\"$repo_name\",\"design\":\"Approved design: append a short result to $output_file, commit it, then close the task.\"}" | id_of)"
  printf '%s\n' "$task" > "$work-task"
  read -r change head < <(revision "$task" 1)
  printf '%s\n' "$change" > "$work-change"
  wait_status "$task" review
  ;;
approve)
  # approve <ws> <number>: Approve applies the revision into the lead's
  # working area, with no conflict, and the lead then holds the agent's file.
  task="$(cat "$work-task")"
  read -r change head < <(revision "$task" "$3")
  curl -fsS -X POST "$api/changes/$change/revisions/$3/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$head\",\"verdict\":\"approve\",\"reason\":\"aft\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" \
    > "$work-approve-$3.json" || { cat "$work-approve-$3.json" >&2; exit 1; }
  grep -q '"status":"applied"' "$work-approve-$3.json" || { cat "$work-approve-$3.json" >&2; exit 1; }
  git -C "$repo" cat-file -e "$(lead_head):$output_file"
  wait_status "$task" closed
  ;;
unapply)
  # unapply <ws>: the lead removes the applied layer; the file leaves the lead.
  before="$(lead_head)"
  loom_cli unapply "$repo_name" "$(cat "$work-change")" --workspace "$workspace" > "$work-unapply.txt"
  after="$(lead_head)"
  [[ "$after" != "$before" ]] || { echo "unapply left the lead at $before" >&2; exit 1; }
  ! git -C "$repo" cat-file -e "$after:$output_file" 2>/dev/null
  printf '%s\n' "$after" > "$work-lead-after-unapply"
  ;;
rerun)
  # rerun <ws>: reopen the task; the daemon reruns it in the same reused agent
  # checkout. Revision 2's base is the lead's current head, and the agent's
  # leftover commit from attempt 1 is not in it.
  task="$(cat "$work-task")"
  curl -fsS -X POST "$api/issues/$task/reopen" -H 'Content-Type: application/json' -d '{"reason":"aft rerun"}' >/dev/null
  read -r change head < <(revision "$task" 2)
  lead="$(lead_head)"
  [[ "$lead" == "$(cat "$work-lead-after-unapply")" ]] || { echo "the lead moved during the rerun" >&2; exit 1; }
  base="$(git -C "$repo" rev-parse "refs/loom/ws/$workspace/change/$change/2/base")"
  [[ "$base" == "$lead" ]] || {
    echo "revision 2 base is $base, want the lead's current head $lead" >&2
    git -C "$repo" log --oneline -5 "$base" >&2
    exit 1
  }
  # The rerun wrote the whole file onto the lead's state: five lines, not ten.
  [[ "$(git -C "$repo" show "$head:$output_file" | wc -l | tr -d ' ')" == 5 ]]
  wait_status "$task" review
  ;;
*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac

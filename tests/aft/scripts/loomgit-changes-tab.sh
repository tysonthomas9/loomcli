#!/usr/bin/env bash
# P2.24 (D43): API side of the agent's one Changes tab. The workspace comes
# from loomgit-rerun-from-lead.sh setup (AFT_WITH_LEAD=1 AFT_DONE_DELAY=N): a
# real daemon coder, rerun-coder, on the deterministic backend, which pauses
# N seconds after committing its file so the test sees it still working, and
# a lead agent named lead.
set -euo pipefail

phase="$1"
workspace="$2"
slot="${3:-one}"
api="$AFT_BASE_URL/api/workspaces/$workspace"
work="$AFT_WORK_DIR/$workspace"
repo_name="$(cat "$work-repo-name")"
output_file="local-mode-agent-output.txt"

id_of() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])'; }
field() { python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; print(d.get(sys.argv[2]) or "")' "$@"; }
issue() { curl -fsS "$api/issues/$1" > "$work-issue.json"; }
wait_status() {
  for _ in $(seq 1 90); do
    issue "$1"
    [[ "$(field "$work-issue.json" status)" == "$2" ]] && return 0
    sleep 2
  done
  echo "task $1 is '$(field "$work-issue.json" status)', want '$2'" >&2
  return 1
}
task="$(cat "$work-$slot-task" 2>/dev/null || true)"

case "$phase" in
claimed)
  # claimed <ws> <slot> <blocker|lead|trunk>: the coder claims a new task;
  # while it works, the task reports where its attempt started.
  task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"changes tab $slot\",\"issue_type\":\"task\",\"priority\":0,\"source_repo\":\"$repo_name\",\"design\":\"Approved design: append a short result to $output_file, commit it, then close the task.\"}" | id_of)"
  printf '%s\n' "$task" > "$work-$slot-task"
  wait_status "$task" in_progress
  for _ in $(seq 1 30); do
    curl -fsS "$api/issues/$task/started-from?lead=lead" > "$work-started.json"
    python3 - "$work-started.json" "$4" <<'PY' && exit 0
import json, sys
d = json.load(open(sys.argv[1]))["data"]
sys.exit(0 if d["kind"] == sys.argv[2] else 1)
PY
    sleep 1
  done
  echo "task $task started from $(cat "$work-started.json"), want $4" >&2
  exit 1
  ;;
live-files)
  # live-files <ws> <slot>: while the coder still works, its committed file is
  # in the agent's diff (needs B8: the daemon must run in the recorded checkout).
  for _ in $(seq 1 60); do
    issue "$task"
    if [[ "$(field "$work-issue.json" status)" == in_progress ]] &&
      curl -fsS "$api/agents/rerun-coder/diff/files?to=HEAD" > "$work-diff.json" 2>/dev/null &&
      grep -q "\"$output_file\"" "$work-diff.json"; then
      exit 0
    fi
    sleep 1
  done
  echo "rerun-coder never showed $output_file while task $task was in progress" >&2
  cat "$work-issue.json" "$work-diff.json" >&2 || true
  ws_path="$(cat "$work-ws-path")"
  find "$ws_path" -maxdepth 4 -type d -name '*rerun-coder*' 2>/dev/null | while read -r d; do
    echo "== $d" >&2
    git -C "$d" log --oneline --decorate -4 >&2 || true
  done
  exit 1
  ;;
done)
  # done <ws> <slot>: the attempt finished; the task waits for review with its revision.
  wait_status "$task" review
  curl -fsS "$api/issues/$task/revisions" > "$work-revisions.json"
  grep -q '"head_sha"' "$work-revisions.json"
  ;;
approve)
  # approve <ws> <slot>: Approve applies the code into the lead and closes the task as approved.
  read -r change number head < <(python3 - "$work-revisions.json" <<'PY'
import json, sys
r = max(json.load(open(sys.argv[1]))["data"], key=lambda i: i["number"])
print(r["change_id"], r["number"], r["head_sha"])
PY
)
  curl -fsS -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$head\",\"verdict\":\"approve\",\"reason\":\"aft\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}" \
    > "$work-approve.json" || { cat "$work-approve.json" >&2; exit 1; }
  grep -q '"status":"applied"' "$work-approve.json" || { cat "$work-approve.json" >&2; exit 1; }
  wait_status "$task" closed
  [[ "$(field "$work-issue.json" close_reason)" == "Approved: code applied" ]]
  ;;
approved-count)
  # approved-count <ws> <n>: the workspace has n tasks closed as approved.
  curl -fsS "$api/issues?status=closed" > "$work-closed.json"
  python3 - "$work-closed.json" "$3" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
items = d.get("data", d)
items = items.get("issues", items) if isinstance(items, dict) else items
n = sum(1 for i in items if (i.get("close_reason") or "").startswith("Approved"))
assert n == int(sys.argv[2]), (n, [(i["id"], i.get("close_reason")) for i in items])
PY
  ;;
*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac

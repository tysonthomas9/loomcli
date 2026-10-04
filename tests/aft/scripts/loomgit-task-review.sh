#!/usr/bin/env bash
# P1.26 (D29): a task whose attempt froze code stays open, in status review
# with the code-review label, until its revision is decided. Approve (once
# applied) closes it, Reject sets it back to open, and an empty attempt still
# closes as "No changes". While the task waits, its dependent stays blocked
# and its epic stays open. Real TaskRuns record the revisions.
set -euo pipefail

phase="$1"
workspace="$2"
api="$AFT_BASE_URL/api/workspaces/$workspace"
lower="$(printf '%s' "$workspace" | tr '[:upper:]' '[:lower:]')"
repo="$AFT_WORK_DIR/$lower-repo"
remote="$AFT_WORK_DIR/$lower-origin.git"
work="$AFT_WORK_DIR/$workspace"

id_of() { python3 -c 'import json,sys; print(json.load(sys.stdin)["data"]["id"])'; }
issue() { curl -fsS "$api/issues/$1" > "$work-issue.json"; }
# status <id> prints "<status> <has code-review label>".
status() {
  issue "$1"
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; print(d["status"], "code-review" in (d.get("labels") or []))' "$work-issue.json"
}
wait_status() {
  # wait_status <id> <status> <label True|False>
  for _ in $(seq 1 60); do
    [[ "$(status "$1")" == "$2 $3" ]] && return 0
    sleep 2
  done
  echo "task $1 is '$(status "$1")', want '$2 $3':" >&2
  cat "$work-issue.json" >&2
  return 1
}
newest() {
  curl -fsS "$api/issues/$1/revisions" > "$work-revisions.json"
  python3 -c 'import json,sys; r=max(json.load(open(sys.argv[1]))["data"], key=lambda i: i["number"]); print(r["change_id"], r["number"], r["head_sha"])' "$work-revisions.json"
}
wait_revision() {
  for _ in $(seq 1 90); do
    curl -fsS "$api/issues/$1/revisions" > "$work-revisions.json"
    grep -q '"head_sha"' "$work-revisions.json" && return 0
    sleep 2
  done
  echo "task $1 recorded no revision" >&2
  return 1
}
verdict() {
  # verdict <task> <approve|reject> [extra json]
  read -r change number sha < <(newest "$1")
  curl -fsS -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"$2\"${3:-},\"reason\":\"aft\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}"
}
# blocked <id> exits 0 when the task is listed as blocked and not as ready.
blocked() {
  curl -fsS "$api/blocked" > "$work-blocked.json"
  curl -fsS "$api/ready" > "$work-ready.json"
  grep -q "\"$1\"" "$work-blocked.json" && ! grep -q "\"$1\"" "$work-ready.json"
}

case "$phase" in
setup)
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$repo"
  printf 'base\n' > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
  git -C "$repo" remote add origin git@github.com:owner/repo.git
  git -C "$repo" push -q origin main
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
attempt)
  # attempt <ws> <slot> [file]: an epic with one task (writing <file>, or
  # nothing) and a task outside the epic that depends on it. A real TaskRun
  # runs the epic's task until it records its revision.
  slot="$3" file="${4:-}"
  design="Check the README; nothing needs to change."
  [[ -n "$file" ]] && design="STUB_CODEX_PATCH=$file"
  epic="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"task review $slot epic\",\"issue_type\":\"epic\",\"priority\":2}" | id_of)"
  task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"task review $slot\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"design\":\"$design\"}" | id_of)"
  dependent="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"task review $slot dependent\",\"issue_type\":\"task\",\"priority\":2}" | id_of)"
  curl -fsS -X POST "$api/issues/$dependent/dependencies" -H 'Content-Type: application/json' \
    -d "{\"depends_on_id\":\"$task\",\"dep_type\":\"blocks\"}" >/dev/null
  printf '%s\n' "$epic" > "$work-$slot-epic"
  printf '%s\n' "$task" > "$work-$slot-task"
  printf '%s\n' "$dependent" > "$work-$slot-dependent"
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" >/dev/null
  wait_revision "$task"
  ;;
in-review)
  # in-review <ws> <slot>: the finished attempt with code left its task in
  # review with the code-review label, its dependent blocked, its epic open.
  task="$(cat "$work-$3-task")"
  wait_status "$task" review True
  python3 - "$work-revisions.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
assert len(data) == 1 and data[0]["no_changes"] is False and not data[0].get("verdict"), data
PY
  blocked "$(cat "$work-$3-dependent")" || { echo "the dependent is not blocked by the task in review" >&2; cat "$work-blocked.json" "$work-ready.json" >&2; exit 1; }
  test "$(status "$(cat "$work-$3-epic")")" != "closed False"
  # The label belongs to Loom: an operator status edit keeps the task as is
  # only through Approve/Reject; agents are refused by the daemon (unit tested).
  ;;
approve)
  # approve <ws> <slot>: Approve applies the code and closes the task without
  # the label; the dependent becomes ready.
  task="$(cat "$work-$3-task")"
  verdict "$task" approve ',"approve_only":true' > "$work-approve.json"
  grep -q '"status":"applied"' "$work-approve.json" || { cat "$work-approve.json" >&2; exit 1; }
  wait_status "$task" closed False
  python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; r=d.get("close_reason"); assert r in (None, "", "Approved: code applied"), r' "$work-issue.json"
  dependent="$(cat "$work-$3-dependent")"
  for _ in $(seq 1 15); do blocked "$dependent" || break; sleep 1; done
  ! blocked "$dependent"
  grep -q "\"$dependent\"" "$work-ready.json"
  ;;
reject)
  # reject <ws> <slot>: Reject sets the task back to open without the label,
  # ready for another attempt; its dependent is still blocked.
  task="$(cat "$work-$3-task")"
  verdict "$task" reject > "$work-reject.json"
  wait_status "$task" open False
  blocked "$(cat "$work-$3-dependent")"
  ;;
no-changes)
  # no-changes <ws> <slot>: an empty attempt still closes its task.
  wait_status "$(cat "$work-$3-task")" closed False
  python3 - "$work-revisions.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
assert len(data) == 1 and data[0]["no_changes"] is True, data
PY
  ;;
*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac

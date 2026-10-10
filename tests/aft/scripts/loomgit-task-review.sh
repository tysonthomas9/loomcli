#!/usr/bin/env bash
# P1.26 (D29): a task whose attempt froze code stays open, in status review
# with the code-review label, until its revision is decided. Approve (once
# applied) closes it, Reject sets it back to open, and an empty attempt still
# closes as "No changes". While the task waits its epic stays open. A
# dependent in its stack starts on its frozen code (Tyson, 2026-10-09):
# approving the dependent first waits for it, and rejecting it makes the
# dependent stale until a Rebuild. A dependent outside its stack stays
# blocked until it closes. Real TaskRuns record the revisions.
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
json() { python3 -c "import json,sys; v=json.load(open(sys.argv[1])); $2" "$1" "${@:3}"; }
# revision_field <task> <field>: one field of the task's newest revision.
revision_field() {
  newest "$1" >/dev/null
  json "$work-revisions.json" 'r=max(v["data"],key=lambda i:i["number"]); f=r.get(sys.argv[2]); print("" if f is None else f)' "$2"
}
wait_field() { # wait_field <task> <field> <value>
  for _ in $(seq 1 60); do
    [[ "$(revision_field "$1" "$2")" == "$3" ]] && return 0
    sleep 2
  done
  echo "task $1 newest revision $2 is '$(revision_field "$1" "$2")', want '$3':" >&2
  cat "$work-revisions.json" >&2
  return 1
}
revision_count() {
  curl -fsS "$api/issues/$1/revisions" > "$work-revisions.json"
  json "$work-revisions.json" 'print(len(v["data"]))'
}
wait_revisions() { # wait_revisions <task> <count>
  for _ in $(seq 1 90); do
    [[ "$(revision_count "$1")" -ge "$2" ]] && return 0
    sleep 2
  done
  echo "task $1 has $(revision_count "$1") revision(s), want $2" >&2
  return 1
}
wait_run() { # wait_run <slot>: the slot's last epic runner has ended
  local prior="$work-$1-run"
  [[ -s "$prior" ]] || return 0
  for _ in $(seq 1 60); do
    curl -fsS "$api/runs/$(cat "$prior")" > "$work-run.json"
    json "$work-run.json" 'd=v.get("data",v); s=(d.get("status") or "").lower(); sys.exit(0 if s and s not in ("queued","pending","running","waiting","in_progress","starting") else 1)' && return 0
    sleep 2
  done
  echo "epic runner $(cat "$prior") did not end:" >&2
  cat "$work-run.json" >&2
  return 1
}
run_epic() { # run_epic <slot>: start the epic runner once the previous one ended
  local prior="$work-$1-run"
  wait_run "$1"
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$(cat "$work-$1-epic")\",\"runner\":\"local-task-runner\"}" > "$work-run.json"
  json "$work-run.json" 'd=v.get("data",v); print(d.get("id") or d.get("runId") or d.get("run_id") or "")' > "$prior"
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
  # review with the code-review label and its epic open. Its dependent is
  # outside the task's stack, so it cannot be built on the task's code and
  # stays blocked until the task closes (in-stack dependents start: chain).
  task="$(cat "$work-$3-task")"
  wait_status "$task" review True
  python3 - "$work-revisions.json" <<'PY'
import json, sys
data = json.load(open(sys.argv[1]))["data"]
assert len(data) == 1 and data[0]["no_changes"] is False and not data[0].get("verdict"), data
PY
  blocked "$(cat "$work-$3-dependent")" || { echo "the dependent outside the task's stack is not blocked by the task in review" >&2; cat "$work-blocked.json" "$work-ready.json" >&2; exit 1; }
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
chain)
  # chain <ws> <slot>: an epic with A and B, B blocked by A. B starts as soon
  # as A's agent finished, on A's frozen revision, while A waits in review.
  slot="$3"
  epic="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"task review $slot epic\",\"issue_type\":\"epic\",\"priority\":2}" | id_of)"
  printf '%s\n' "$epic" > "$work-$slot-epic"
  for name in a b; do
    task="$(curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
      -d "{\"title\":\"task review $slot $name\",\"issue_type\":\"task\",\"priority\":2,\"parent\":\"$epic\",\"source_repo\":\"$(basename "$repo")\",\"design\":\"STUB_CODEX_PATCH=trev-$slot-$name.txt\"}" | id_of)"
    printf '%s\n' "$task" > "$work-$slot-$name"
  done
  a="$(cat "$work-$slot-a")" b="$(cat "$work-$slot-b")"
  curl -fsS -X POST "$api/issues/$b/dependencies" -H 'Content-Type: application/json' \
    -d "{\"depends_on_id\":\"$a\",\"dep_type\":\"blocks\"}" >/dev/null
  rm -f "$work-$slot-run"
  run_epic "$slot"
  wait_revision "$a"
  wait_revision "$b"
  # A is still in review with no verdict: B did not wait for A's review.
  wait_status "$a" review True
  test -z "$(revision_field "$a" verdict)"
  wait_status "$b" review True
  test "$(revision_field "$b" depends_on)" = "$a"
  test -z "$(revision_field "$b" lineage_state)"
  ;;
approve-first)
  # approve-first <ws> <slot>: approving B before A waits, with the reason.
  a="$(cat "$work-$3-a")" b="$(cat "$work-$3-b")"
  verdict "$b" approve ',"approve_only":true' > "$work-approve-b.json"
  json "$work-approve-b.json" 'assert v.get("status")=="approved_waiting_for_dependency" and v.get("reason")=="waiting for "+sys.argv[2]+" to be approved", v' "$a"
  test "$(revision_field "$b" follow_status)" = waiting_for_dependency
  test "$(revision_field "$b" follow_reason)" = "waiting for $a to be approved"
  wait_status "$b" review True
  ;;
approve-blocker)
  # approve-blocker <ws> <slot>: approving A applies A, then B in order, and
  # closes both.
  a="$(cat "$work-$3-a")" b="$(cat "$work-$3-b")"
  verdict "$a" approve ',"approve_only":true' > "$work-approve-a.json"
  grep -q '"status":"applied"' "$work-approve-a.json" || { cat "$work-approve-a.json" >&2; exit 1; }
  wait_status "$a" closed False
  wait_field "$b" applied True
  wait_status "$b" closed False
  ;;
stale)
  # stale <ws> <slot>: rejecting A makes B stale: Approve on B is refused
  # with the reason, and nothing reruns B on its own.
  a="$(cat "$work-$3-a")" b="$(cat "$work-$3-b")"
  # The runner ended with both tasks in review, so nothing reruns A at once.
  wait_run "$3"
  verdict "$a" reject > "$work-reject-a.json"
  wait_status "$a" open False
  reason="built on $a's code, which was rejected: rebuild it once $a has new code"
  wait_field "$b" lineage_state stale
  test "$(revision_field "$b" lineage_reason)" = "$reason"
  test -z "$(revision_field "$b" rebuild_on)"
  read -r change number sha < <(newest "$b")
  code="$(curl -sS -o "$work-approve-stale.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"approve_only\":true,\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}")"
  test "$code" = 409
  json "$work-approve-stale.json" 'assert v["error"]=="stale" and v["message"]=="approve is refused: "+sys.argv[2], v' "$reason"
  wait_status "$b" review True
  test "$(revision_count "$b")" = 1
  ;;
rebuild)
  # rebuild <ws> <slot>: A's next attempt offers B a rebuild on it; Rebuild
  # reopens B and its next attempt is built on A's new revision.
  a="$(cat "$work-$3-a")" b="$(cat "$work-$3-b")"
  curl -fsS -X PATCH "$api/issues/$a" -H 'Content-Type: application/json' \
    -d "{\"design\":\"STUB_CODEX_PATCH=trev-$3-a2.txt\"}" >/dev/null
  run_epic "$3"
  wait_revisions "$a" 2
  wait_status "$a" review True
  wait_field "$b" rebuild_on 2
  test "$(revision_field "$b" lineage_reason)" = "built on $a's code, which was rejected: rebuild it on $a's new code"
  # Rebuild is a human action: an agent is refused.
  code="$(curl -sS -o "$work-rebuild-agent.json" -w '%{http_code}' -X POST "$api/issues/$b/rebuild" -H 'Content-Type: application/json' \
    -d '{"actor":{"kind":"agent","id":"aft-agent"}}')"
  test "$code" = 409 && grep -q '"review_required"' "$work-rebuild-agent.json"
  curl -fsS -X POST "$api/issues/$b/rebuild" -H 'Content-Type: application/json' \
    -d '{"actor":{"kind":"human","id":"aft-operator"}}' > "$work-rebuild.json"
  json "$work-rebuild.json" 'd=v["data"]; assert d["rebuild_on"]==2 and d["verdict"]=="reject" and d["depends_on"]==sys.argv[2], v' "$a"
  wait_status "$b" open False
  run_epic "$3"
  wait_revisions "$b" 2
  wait_status "$b" review True
  test "$(revision_field "$b" depends_on)" = "$a"
  test -z "$(revision_field "$b" lineage_state)"
  # A task in code review only leaves it through Approve or Reject, so the
  # suite's board teardown can close neither: reject both to reopen them.
  verdict "$b" reject > "$work-reject-b.json"
  wait_status "$b" open False
  verdict "$a" reject > "$work-reject-a.json"
  wait_status "$a" open False
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

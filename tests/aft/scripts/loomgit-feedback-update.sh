#!/usr/bin/env bash
# P2.19b (D29 (6)): review feedback updates the open PR without asking. A
# fix-up revision of a task whose PR is open replaces that task's layer, the
# layers above are replayed, and the PR is pushed with no Approve.
#   loom   Loom stack: A's fix-up is pushed to A's PR and B's PR is rebuilt on
#          it; B's pending "merge after #N" is cancelled by B's fix-up; a
#          fix-up of A that conflicts with B is held and the lead is told
#   trunk  PR per task: the fix-up is pushed to the task's own PR; a fix-up
#          that adds a secret-pattern path is never pushed
#
# The review feedback is recorded in the journal as the verified GitHub
# webhook would record it (the webhook's signature path has its own tests).
# With AFT_FEEDBACK_WEBHOOK=1 each case's first review instead arrives as a
# real HMAC-signed pull_request_review webhook through the product endpoint.
# Addressing it uses the product's address API, a real commit in the task copy
# it returns, and `loom feedback complete`, as the feedback agent does.
set -Eeuo pipefail

phase="$1"
case_name="$2"
stage="${3:-}"
case_dir="$AFT_WORK_DIR/feedback-update-$case_name"
upper="$(printf '%s' "$case_name" | tr '[:lower:]' '[:upper:]')"
workspace="E2E-WS-FEEDBACKUPDATE-$upper"
api="$AFT_BASE_URL/api/workspaces/$workspace"
repo_name="feedback-update-$case_name"
remote="$case_dir/$repo_name.git"
journal="$AFT_LOOM_CONFIG_DIR/loomgit/store.db"
export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
export GITHUB_TOKEN=aft-fixture-token

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
  repo="$case_dir/$repo_name"
  git init -q --bare "$remote"
  git -C "$remote" symbolic-ref HEAD refs/heads/main
  git init -q -b main "$repo"
  printf 'base %s\n' "$repo_name" > "$repo/README.md"
  git -C "$repo" add README.md
  git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
  git -C "$repo" config core.sshCommand "sh $AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh $remote"
  git -C "$repo" remote add origin "git@github.com:owner/$repo_name.git"
  git -C "$repo" push -q origin main
  python3 -c 'import json,sys; print(json.dumps({"remote":sys.argv[1],"remotes":{"owner/"+sys.argv[2]:sys.argv[1]},"preserve":True}))' "$remote" "$repo_name" |
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__reset" -H 'Content-Type: application/json' -d @- >/dev/null
  python3 -c 'import json,sys; print(json.dumps({"name":sys.argv[1],"type":"empty","repos":[sys.argv[2]]}))' "$(tr '[:upper:]' '[:lower:]' <<<"$workspace")" "$repo" |
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

# run_tasks <chain|none> <name>... runs one real task per argument through the
# epic runner until each has a reviewable revision.
run_tasks() {
  local chain="$1"
  shift
  curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' \
    -d "{\"title\":\"feedback-update epic $case_name $RUN_ID\",\"issue_type\":\"epic\",\"priority\":2}" > "$case_dir/epic.json"
  local epic previous="" name task
  epic="$(json "$case_dir/epic.json" 'print(v["data"]["id"])')"
  for name in "$@"; do
    python3 -c 'import json,sys; print(json.dumps({"title":"feedback-update "+sys.argv[1]+" "+sys.argv[2],"issue_type":"task","priority":2,"parent":sys.argv[3],"source_repo":sys.argv[4],"design":"STUB_CODEX_PATCH=feedback-update-"+sys.argv[1]+".txt"}))' \
      "$name" "$RUN_ID" "$epic" "$repo_name" |
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
  for name in "$@"; do
    task="$(cat "$case_dir/task-$name.id")"
    for _ in $(seq 1 90); do
      curl -fsS "$api/issues/$task/revisions" > "$case_dir/revisions-$name.json"
      if grep -q '"head_sha"' "$case_dir/revisions-$name.json"; then break; fi
      sleep 2
    done
    grep -q '"head_sha"' "$case_dir/revisions-$name.json"
  done
}

revisions() { # revisions <name>: the task's revisions into revisions-<name>.json
  curl -fsS "$api/issues/$(cat "$case_dir/task-$1.id")/revisions" > "$case_dir/revisions-$1.json"
}

# newest prints "change number head_sha pr_head" for the task's newest revision.
newest() {
  revisions "$1"
  json "$case_dir/revisions-$1.json" 'r=max(v["data"],key=lambda i:i["number"]); print(r["change_id"],r["number"],r["head_sha"],r.get("pr_head") or "-")'
}

# revision_field <name> <number> <field>: one field of one revision of the task.
revision_field() {
  revisions "$1"
  json "$case_dir/revisions-$1.json" 'r=[i for i in v["data"] if i["number"]==int(sys.argv[2])][0]; f=r.get(sys.argv[3]); print("" if f is None else f)' "$2" "$3"
}

approve() { # approve <name>: Approve and create PR of the newest revision
  local name="$1" change number sha code
  read -r change number sha _ < <(newest "$name")
  printf '%s\n' "$change" > "$case_dir/change-$name.id"
  code="$(curl -sS -o "$case_dir/verdict-$name.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}")"
  [[ "$code" == 200 ]] || { [[ "$code" == 409 ]] && grep -q '"publish_failed"' "$case_dir/verdict-$name.json"; }
  if grep -q 'approved_waiting_for_working_area' "$case_dir/verdict-$name.json"; then
    curl -fsS -X POST "$api/git/apply" -H 'Content-Type: application/json' \
      -d "{\"change\":\"$change\",\"revision\":$number,\"lead\":\"lead\"}" > "$case_dir/apply-$name.json"
    grep -q '"success":true' "$case_dir/apply-$name.json"
  fi
}

wait_pr() { # wait_pr <name>: the task's revision shows its open PR
  for _ in $(seq 1 45); do
    read -r _ _ _ pr_head < <(newest "$1")
    [[ "$pr_head" != "-" ]] && return 0
    sleep 2
  done
  echo "task $1 has no open PR: $(cat "$case_dir/revisions-$1.json")" >&2
  return 1
}

pr_ref() { # pr_ref <name>: the task's PR head branch on the fake forge
  local change
  change="$(cat "$case_dir/change-$1.id")"
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-now.json"
  json "$case_dir/pulls-now.json" 'print([x for x in v if x["head"]["ref"].endswith("/change/"+sys.argv[2])][0]["head"]["ref"])' "$change"
}

pull_of() { # pull_of <name>: the task's PR number on the fake forge
  local change
  change="$(cat "$case_dir/change-$1.id")"
  curl -fsS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" > "$case_dir/pulls-now.json"
  json "$case_dir/pulls-now.json" 'print([x for x in v if x["head"]["ref"].endswith("/change/"+sys.argv[2])][0]["number"])' "$change"
}

pr_head() { git --git-dir="$remote" rev-parse "refs/heads/$(pr_ref "$1")"; }

pr_file() { # pr_file <name> <path>: the file on the task's PR branch, or nothing
  git --git-dir="$remote" show "refs/heads/$(pr_ref "$1"):$2" 2>/dev/null || true
}

# signed_review <delivery> <change> <pr> <path> <head>: with
# AFT_FEEDBACK_WEBHOOK=1 the case's first review arrives as a real GitHub
# pull_request_review webhook, HMAC-signed with the trigger binding's secret,
# and must show up as pending feedback on the change.
signed_review() {
  local delivery="$1" change="$2" pr="$3" path="$4" head="$5" secret="aft-feedback-secret" code
  loom trigger bindings create --route-key github.pull_request_review.submitted \
    --workflow epic-runner --secret "$secret" > "$case_dir/binding.txt"
  python3 -c 'import json,sys; print(json.dumps({"action":"submitted","repository":{"full_name":"owner/"+sys.argv[1]},"sender":{"login":"reviewer"},"pull_request":{"number":int(sys.argv[2]),"head":{"sha":sys.argv[3]}},"review":{"state":"changes_requested","author_association":"MEMBER","body":"please fix "+sys.argv[4]}}))' \
    "$repo_name" "$pr" "$head" "$path" > "$case_dir/webhook-$delivery.json"
  code="$(curl -sS -o "$case_dir/webhook-response-$delivery.json" -w '%{http_code}' -X POST "$api/webhooks/github" \
    -H 'Content-Type: application/json' -H 'X-GitHub-Event: pull_request_review' -H "X-GitHub-Delivery: $delivery" \
    -H "X-Hub-Signature-256: sha256=$(openssl dgst -sha256 -hmac "$secret" -r < "$case_dir/webhook-$delivery.json" | cut -d' ' -f1)" \
    --data-binary @"$case_dir/webhook-$delivery.json")"
  test "$code" = 202 || { echo "signed review webhook: HTTP $code $(cat "$case_dir/webhook-response-$delivery.json")" >&2; return 1; }
  curl -fsS "$api/changes/$change/feedback" > "$case_dir/feedback-$delivery.json"
  json "$case_dir/feedback-$delivery.json" 'f=[x for x in v["feedback"] if x.get("delivery_id")==sys.argv[2]]; assert f and f[0].get("status")=="pending", v' "$delivery"
  touch "$case_dir/webhook-sent"
}

# fixup <name> <path> <body>: a trusted reviewer requests changes on the task's
# PR, and the feedback agent addresses it with one commit writing <path>.
# Prints the new revision number.
fixup() {
  local name="$1" path="$2" body="$3" change pr head delivery attempt target sha
  change="$(cat "$case_dir/change-$name.id")"
  pr="$(pull_of "$name")"
  head="$(pr_head "$name")"
  delivery="fb-$case_name-$name-$RUN_ID-$RANDOM"
  attempt="fixup-$RANDOM"
  if [[ "${AFT_FEEDBACK_WEBHOOK:-}" == 1 && ! -f "$case_dir/webhook-sent" ]]; then
    signed_review "$delivery" "$change" "$pr" "$path" "$head"
  else
    python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1],timeout=10); db.execute("insert into change_feedback(workspace,delivery_id,change_id,pr_number,kind,actor,association,body,head_sha,status) values (?,?,?,?,?,?,?,?,?,?)",(sys.argv[2],sys.argv[3],sys.argv[4],int(sys.argv[5]),"changes_requested","reviewer","MEMBER","please fix "+sys.argv[6],sys.argv[7],"pending")); db.commit()' \
      "$journal" "$workspace" "$delivery" "$change" "$pr" "$path" "$head"
  fi
  curl -fsS -X POST "$api/changes/$change/feedback/$delivery/address" -H 'Content-Type: application/json' \
    -d "{\"attempt\":\"$attempt\"}" > "$case_dir/address-$delivery.json"
  target="$(json "$case_dir/address-$delivery.json" 'print(v["target"])')"
  test "$(json "$case_dir/address-$delivery.json" 'print(v["base_sha"])')" = "$head"
  mkdir -p "$(dirname "$target/$path")"
  printf '%s\n' "$body" > "$target/$path"
  git -C "$target" add -- "$path"
  git -C "$target" -c user.name=AFT -c user.email=aft@example.test commit -q -m "address review: $path"
  sha="$(git -C "$target" rev-parse HEAD)"
  loom feedback complete "$delivery" --capture-sha "$sha" > "$case_dir/complete-$delivery.txt"
  sed -nE 's/^Feedback addressed by revision ([0-9]+) of .*/\1/p' "$case_dir/complete-$delivery.txt"
}

wait_feedback() { # wait_feedback <name> <revision> <status>
  local got=""
  for _ in $(seq 1 45); do
    got="$(revision_field "$1" "$2" feedback_status)"
    [[ "$got" == "$3" ]] && return 0
    sleep 2
  done
  echo "task $1 revision $2: feedback_status '$got', want '$3': $(cat "$case_dir/revisions-$1.json")" >&2
  return 1
}

wait_pr_file() { # wait_pr_file <name> <path> <body>
  for _ in $(seq 1 45); do
    [[ "$(pr_file "$1" "$2")" == "$3" ]] && return 0
    sleep 2
  done
  echo "PR of task $1 has '$2' = '$(pr_file "$1" "$2")', want '$3'" >&2
  return 1
}

human_verdicts() { # human_verdicts <name>: verdicts a human gave on the task
  local change
  change="$(cat "$case_dir/change-$1.id")"
  python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1],timeout=10); print(db.execute("select count(*) from review_verdicts where workspace=? and change_id=? and actor_kind=?",(sys.argv[2],sys.argv[3],"human")).fetchone()[0])' \
    "$journal" "$workspace" "$change"
}

attention() { # attention <key prefix>: lead notices queued with this key
  python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1],timeout=10); print(db.execute("select count(*) from journal_entries where operation=? and id like ?",("event",sys.argv[2]+"%")).fetchone()[0])' \
    "$journal" "$1"
}

merge_approve() { # merge_approve <name>: Approve and merge at the PR head shown
  local change pr_head
  read -r change _ _ pr_head < <(newest "$1")
  curl -sS -o "$case_dir/merge-approval-$1.json" -w '%{http_code}' -X POST "$api/changes/$change/merge-approval" \
    -H 'Content-Type: application/json' \
    -d "{\"lead\":\"lead\",\"head_sha\":\"$pr_head\",\"actor\":{\"kind\":\"human\",\"id\":\"aft-operator\"}}"
}

approval_state() { # approval_state <name>: "status|reason" of Approve and merge
  local change
  change="$(cat "$case_dir/change-$1.id")"
  curl -fsS "$api/changes/$change/merge-approval" > "$case_dir/approval-$1.json"
  json "$case_dir/approval-$1.json" 'd=v.get("data",v); print((d.get("status") or "")+"|"+(d.get("reason") or ""))'
}

wait_state() { # wait_state <name> <status> [reason substring]
  local got=""
  for _ in $(seq 1 40); do
    got="$(approval_state "$1")"
    if [[ "${got%%|*}" == "$2" && "${got#*|}" == *"${3:-}"* ]]; then return 0; fi
    sleep 2
  done
  echo "task $1: merge approval '$got', want '$2' with '${3:-}'" >&2
  return 1
}

open_task() {
  browser open "$AFT_BASE_URL/ws/$workspace/kanban" >/dev/null
  browser wait '[data-testid="board-toolbar"]' >/dev/null
  browser open "$AFT_BASE_URL/ws/$workspace/issues/$(cat "$case_dir/task-$1.id")" >/dev/null
  browser wait '[data-testid="revisions-section"]' >/dev/null
}

wait_text() { # wait_text <testid> <text>: the open task shows it
  local got=""
  for _ in $(seq 1 30); do
    got="$(browser eval "Array.from(document.querySelectorAll('[data-testid=\"$1\"]')).map(e => e.textContent).join(' | ')")"
    if grep -qF "$2" <<<"$got"; then return 0; fi
    sleep 1
    browser eval "location.reload()" >/dev/null || true
    browser wait '[data-testid="revisions-section"]' >/dev/null || true
  done
  echo "task shows $1 '$got', want '$2'" >&2
  return 1
}

diagnose() {
  local file="$case_dir/diagnosis.txt" id name
  {
    for id in "$case_dir"/task-*.id; do
      name="$(basename "$id" .id)"
      name="${name#task-}"
      echo "== task $name revisions"
      curl -sS "$api/issues/$(cat "$id")/revisions" || true
      echo
    done
    echo "== pulls"
    curl -sS "$AFT_FAKE_GH_BASE/__pulls?workspace=$workspace" || true
    echo
    echo "== feedback_updates"
    python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1],timeout=10); [print(r) for r in db.execute("select * from feedback_updates where workspace=?",(sys.argv[2],))]' "$journal" "$workspace" || true
    echo "== approval_follow"
    python3 -c 'import sqlite3,sys; db=sqlite3.connect(sys.argv[1],timeout=10); [print(r) for r in db.execute("select * from approval_follow where workspace=?",(sys.argv[2],))]' "$journal" "$workspace" || true
    echo "== loom stack list"
    loom stack list 2>&1 || true
  } > "$file" 2>&1
  echo "diagnosis: $file" >&2
}
trap 'diagnose' ERR

case "$case_name" in
  loom)
    case "$stage" in
      open)
        run_tasks chain a b
        for name in a b; do approve "$name"; wait_pr "$name"; done
        ;;
      push)
        # A's fix-up goes to A's PR with no Approve; B's PR is rebuilt on it.
        before_a="$(pr_head a)"; before_b="$(pr_head b)"; humans="$(human_verdicts a)"
        number="$(fixup a review-a.txt "fixed a")"
        wait_feedback a "$number" pushed
        wait_pr_file a review-a.txt "fixed a"
        wait_pr_file b review-a.txt "fixed a"
        test -n "$(pr_file b feedback-update-b.txt)"
        test "$(pr_head a)" != "$before_a"
        test "$(pr_head b)" != "$before_b"
        test "$(revision_field a "$number" verdict)" = feedback
        test "$(human_verdicts a)" = "$humans"
        # One layer per task: A's PR is its task commit plus the fix-up, and
        # B's PR sits directly on A's new head.
        test "$(git --git-dir="$remote" rev-list --count "main..refs/heads/$(pr_ref a)")" = 2
        test "$(git --git-dir="$remote" rev-parse "$(pr_head b)^")" = "$(pr_head a)"
        open_task a
        wait_text feedback-status "Pushed to PR #$(pull_of a) automatically"
        test "$(browser eval "document.querySelectorAll('[data-testid=\"approve-create-pr\"]').length")" = 0
        browser screenshot "$case_dir/loom-fixup-pushed.png" >/dev/null
        ;;
      cancel)
        # B waits to merge after A; B's fix-up changes the code, so that
        # approval is cancelled and the fix-up is still pushed.
        pa="$(pull_of a)"
        test "$(merge_approve b)" = 200
        wait_state b waiting "merges after #$pa"
        number="$(fixup b review-b.txt "fixed b")"
        wait_feedback b "$number" pushed
        wait_pr_file b review-b.txt "fixed b"
        wait_state b cancelled "review fix-ups"
        test "$(revision_field b "$number" feedback_merge_cancelled)" = True
        open_task b
        wait_text feedback-merge-cancelled "Auto-merge cancelled because the code changed. Approve again to merge."
        wait_text feedback-status "Pushed to PR #$(pull_of b) automatically"
        browser screenshot "$case_dir/loom-merge-after-cancelled.png" >/dev/null
        ;;
      held)
        # A fix-up of A writing B's file conflicts when B is replayed: it is
        # held, neither PR moves, and the lead is told once.
        before_a="$(pr_head a)"; before_b="$(pr_head b)"
        number="$(fixup a feedback-update-b.txt "clash")"
        wait_feedback a "$number" held
        test "$(attention "feedback-held:$workspace:$(cat "$case_dir/change-a.id"):$number:")" = 1
        sleep 6
        test "$(pr_head a)" = "$before_a"
        test "$(pr_head b)" = "$before_b"
        test "$(attention "feedback-held:$workspace:$(cat "$case_dir/change-a.id"):$number:")" = 1
        open_task a
        wait_text feedback-status "Held, not pushed to PR #$(pull_of a): it conflicts with the stack; the lead was told"
        browser screenshot "$case_dir/loom-fixup-held.png" >/dev/null
        ;;
      *) echo "unknown stage $stage" >&2; exit 2 ;;
    esac
    ;;

  trunk)
    case "$stage" in
      open)
        test "$(curl -sS -o "$case_dir/mode.json" -w '%{http_code}' -X PUT "$api/git/settings" -H 'Content-Type: application/json' \
          -d '{"actor":{"kind":"human","id":"aft-operator"},"delivery_mode":"trunk"}')" = 200
        run_tasks none e
        approve e
        wait_pr e
        ;;
      push)
        humans="$(human_verdicts e)"
        number="$(fixup e review-e.txt "fixed e")"
        wait_feedback e "$number" pushed
        wait_pr_file e review-e.txt "fixed e"
        test "$(human_verdicts e)" = "$humans"
        open_task e
        wait_text feedback-status "Pushed to PR #$(pull_of e) automatically"
        browser screenshot "$case_dir/trunk-fixup-pushed.png" >/dev/null
        ;;
      secret)
        # A fix-up adding a secret-pattern path is never pushed (D18).
        before="$(pr_head e)"
        number="$(fixup e config/.env "TOKEN=aft")"
        wait_feedback e "$number" not_pushed
        test "$(attention "feedback-not-pushed:$workspace:$(cat "$case_dir/change-e.id"):$number")" = 1
        sleep 6
        test "$(pr_head e)" = "$before"
        test -z "$(pr_file e config/.env)"
        open_task e
        wait_text feedback-status "Not pushed to PR #$(pull_of e): it adds the secret-pattern path config/.env"
        browser screenshot "$case_dir/trunk-secret-not-pushed.png" >/dev/null
        ;;
      *) echo "unknown stage $stage" >&2; exit 2 ;;
    esac
    ;;
esac

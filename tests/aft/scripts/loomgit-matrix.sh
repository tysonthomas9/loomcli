#!/usr/bin/env bash
# PX.7: the Git settings matrix (S1-S10) and the stacked PR walk-through (W1-W8).
#
# One driver for two forges, chosen by AFT_MATRIX_FORGE:
#   fake    (default) a per-case repo on tests/aft/fixtures/fake-github/forge-server.mjs,
#           registered with content_checks so a file containing FAIL makes its PR red,
#           and stub codex writing the task files.
#   github  the run's real private sandbox repo AFT_GITHUB_SANDBOX
#           (tysonthomas9/loom-aft-git-<yyyymmdd-hhmm>, created by real-github-repo.sh),
#           whose required Actions check "check" fails when a file contains FAIL, and
#           real codex writing the task files. Only the Loom server holds the token;
#           this script uses the operator's gh login for read-back, review comments and
#           the hand push, and never prints a token.
#
# Every case has its own Loom workspace, so its branches are loom/ws/<W>/...; the
# real tier shares one repo per run and each case writes its own files.
# P1.26: a task whose code is in review blocks its dependents, so a chain runs one
# task at a time: the next task starts only after the one below it is approved.
#
# Usage: loomgit-matrix.sh <phase> <case> [args...]
set -Eeuo pipefail
source "$AFT_TESTS_DIR/scripts/loomgit-lib.sh"
open_task() { open_issue "$(task_id "$1")"; }

phase="$1"
case_name="$2"
shift 2
forge="${AFT_MATRIX_FORGE:-fake}"
case "$forge" in fake | github) ;; *) echo "AFT_MATRIX_FORGE must be fake or github" >&2; exit 2 ;; esac
[[ "$case_name" =~ ^[a-z0-9]+$ ]] || { echo "case name must be lowercase letters and digits" >&2; exit 2; }

work="${AFT_WORK_DIR:?}/matrix-$case_name"
upper="$(printf '%s' "$case_name" | tr '[:lower:]' '[:upper:]')"
workspace="E2E-WS-MATRIX-$upper"
api="${AFT_BASE_URL:?}/api/workspaces/$workspace"
repo="$work/repo"
remote="$work/origin.git"
export GIT_TERMINAL_PROMPT=0

if [[ "$forge" == fake ]]; then
  : "${AFT_FAKE_GH_BASE:?run through run-aft.sh --suite loomgit-matrix-*}"
  forge_repo="owner/matrix-$case_name-${RUN_ID//[^a-zA-Z0-9]/}"
  export LOOM_CONNECTOR_GITHUB_BASE_URL="$AFT_FAKE_GH_BASE"
  export GITHUB_TOKEN=aft-fixture-token
else
  forge_repo="${AFT_GITHUB_SANDBOX:?run through run-aft.sh --real-github}"
  [[ "$forge_repo" =~ ^tysonthomas9/loom-aft-git-[0-9]{8}-[0-9]{4}$ ]] || { echo "refusing repo $forge_repo: only harness sandbox repos" >&2; exit 2; }
  unset GITHUB_TOKEN GH_TOKEN
fi
[[ -f "$work/forge-repo" ]] && forge_repo="$(cat "$work/forge-repo")"

say() { printf '[%s] %s\n' "$case_name" "$*"; }
fail() { echo "[$case_name] FAIL: $*" >&2; exit 1; }
# Waits scale for the real tier: Codex and GitHub Actions take minutes, not seconds.
scale=1
[[ "$forge" == github ]] && scale=4

# --- forge adapter -----------------------------------------------------------

gh_api() { env -u GITHUB_TOKEN -u GH_TOKEN gh api "$@"; }

# pulls: this case's PRs (head under loom/ws/<workspace>/) as a normalized JSON list.
pulls() {
  if [[ "$forge" == fake ]]; then
    curl -fsS --max-time 10 "$AFT_FAKE_GH_BASE/__pulls?repo=$forge_repo" > "$work/pulls-raw.json"
  else
    gh_api "repos/$forge_repo/pulls?state=all&per_page=100" > "$work/pulls-raw.json"
  fi
  python3 - "$work/pulls-raw.json" "$workspace" > "$work/pulls.json" <<'PY'
import json, sys
raw, ws = json.load(open(sys.argv[1])), sys.argv[2]
print(json.dumps([dict(number=p["number"], state=p["state"], merged=bool(p.get("merged_at")),
                       base=p["base"]["ref"], head=p["head"]["ref"], sha=p["head"]["sha"],
                       url=p.get("html_url", ""))
                  for p in raw if f"/ws/{ws}/" in p["head"]["ref"]]))
PY
}

ref_sha() { # ref_sha <branch>
  if [[ "$forge" == fake ]]; then
    git --git-dir="$remote" rev-parse "refs/heads/$1"
  else
    gh_api "repos/$forge_repo/git/ref/heads/$1" --jq .object.sha
  fi
}

# changed_files <base> <head>: files the head changes against the merge base with base.
changed_files() {
  if [[ "$forge" == fake ]]; then
    git --git-dir="$remote" diff --name-only "refs/heads/$1...refs/heads/$2"
  else
    gh_api "repos/$forge_repo/compare/$1...$2" --jq '.files[].filename'
  fi
}

file_at() { # file_at <ref> <path>
  if [[ "$forge" == fake ]]; then
    git --git-dir="$remote" show "$1:$2"
  else
    gh_api "repos/$forge_repo/contents/$2?ref=$1" --jq .content | base64 -d
  fi
}

# --- Loom helpers --------------------------------------------------------------

file_of() { # the file a slot's task writes: its own, or another slot's (chain's 4th field)
  local slot="$1"
  [[ -f "$work/writes-$slot" ]] && slot="$(cat "$work/writes-$slot")"
  printf 'matrix-%s-%s.txt' "$case_name" "$slot"
}

revisions() { # revisions <slot>: newest revision row into $work/rev-<slot>.json
  curl -fsS --max-time 10 "$api/issues/$(task_id "$1")/revisions" > "$work/revisions-$1.json"
  json "$work/revisions-$1.json" 'r=max(v["data"],key=lambda i:i["number"]) if v["data"] else {}; print(json.dumps(r))' > "$work/rev-$1.json"
}
rev_field() { revisions "$1"; json "$work/rev-$1.json" 'x=v.get(sys.argv[2]); print("" if x is None else x)' "$2"; }

pull_of() { # pull_of <slot>: the task's PR row ({} when none)
  local change
  change="$(cat "$work/change-$1.id" 2>/dev/null || true)"
  pulls
  json "$work/pulls.json" 'p=[x for x in v if sys.argv[2] and x["head"].endswith("/change/"+sys.argv[2])]; print(json.dumps(p[-1] if p else {}))' "$change" > "$work/pull-$1.json"
  cat "$work/pull-$1.json"
}
pull_field() { pull_of "$1" > /dev/null; json "$work/pull-$1.json" 'x=v.get(sys.argv[2]); print("" if x is None else x)' "$2"; }

# eq <want> <command...> / nonempty <command...>: re-run the command on every poll.
eq() { local want="$1"; shift; [[ "$("$@")" == "$want" ]]; }
nonempty() { [[ -n "$("$@")" ]]; }

wait_until() { # wait_until <seconds> <label> <command...>
  local seconds="$1" label="$2" deadline
  shift 2
  deadline=$((SECONDS + seconds))
  while ((SECONDS < deadline)); do
    if "$@" > /dev/null 2>&1; then return 0; fi
    sleep 2
  done
  "$@" || fail "$label (after ${seconds}s)"
}

settings_json() { curl -fsS --max-time 10 "$api/git/settings"; }

expect_settings() { # expect_settings <stack|trunk> <true|false> <off|when_green>
  settings_json > "$work/settings.json"
  json "$work/settings.json" 'want=dict(delivery_mode=sys.argv[2],lead_may_approve_publish=sys.argv[3]=="true",lead_may_merge=sys.argv[4]); assert v==want,(v,want)' "$1" "$2" "$3"
}

open_settings() {
  browser open "$AFT_BASE_URL/ws/$workspace/settings" > /dev/null
  browser find role button click --name Git --exact > /dev/null
  browser wait '[data-testid="git-delivery-mode"]:not([disabled])' > /dev/null
}
ui_value() { browser eval "document.querySelector('[data-testid=\"$1\"]').value" | tr -d '"'; }
ui_select() {
  [[ "$(ui_value "$1")" == "$2" ]] && return 0
  browser select "[data-testid=\"$1\"]" "$2" > /dev/null
  browser wait "[data-testid=\"$1\"]:not([disabled])" > /dev/null
}

verdict() { # verdict <slot> <kind> <id> [extra json] -> http code; body in verdict-<slot>.json
  local change number sha
  revisions "$1"
  read -r change number sha < <(json "$work/rev-$1.json" 'print(v["change_id"],v["number"],v["head_sha"])')
  printf '%s\n' "$change" > "$work/change-$1.id"
  curl -sS --max-time 60 -o "$work/verdict-$1.json" -w '%{http_code}' -X POST "$api/changes/$change/revisions/$number/verdict" \
    -H 'Content-Type: application/json' \
    -d "{\"head_sha\":\"$sha\",\"verdict\":\"approve\"${4:-},\"actor\":{\"kind\":\"$2\",\"id\":\"$3\"}}"
}

merge_approval() { # merge_approval <slot> <kind> <id> -> http code
  local change pr_head
  revisions "$1"
  change="$(json "$work/rev-$1.json" 'print(v["change_id"])')"
  pr_head="$(json "$work/rev-$1.json" 'print(v.get("pr_head") or "")')"
  curl -sS --max-time 60 -o "$work/merge-approval-$1.json" -w '%{http_code}' -X POST "$api/changes/$change/merge-approval" \
    -H 'Content-Type: application/json' \
    -d "{\"lead\":\"lead\",\"head_sha\":\"$pr_head\",\"actor\":{\"kind\":\"$2\",\"id\":\"$3\"}}"
}

approval_state() { # "status|reason" of the task's Approve and merge
  local change
  change="$(cat "$work/change-$1.id")"
  curl -fsS --max-time 10 "$api/changes/$change/merge-approval" > "$work/approval-$1.json"
  json "$work/approval-$1.json" 'd=v.get("data",v) or {}; print((d.get("status") or "")+"|"+(d.get("reason") or ""))'
}

has_state() { local got; got="$(approval_state "$1")"; [[ "${got%%|*}" == "$2" && "${got#*|}" == *"${3:-}"* ]]; }

pr_open() { [[ "$(pull_field "$1" state)" == open ]]; }
pr_merged() { [[ "$(pull_field "$1" merged)" == True ]]; }

publish_settled() { # the task's PR is open and Loom shows the same PR
  local number
  number="$(pull_field "$1" number)"
  [[ -n "$number" && "$(pull_field "$1" state)" == open ]] || return 1
  [[ "$(rev_field "$1" pr_number)" == "$number" ]]
}

record_pr() { # record_pr <slot>: PR number, URL and head for later comparisons and the report
  pull_of "$1" > /dev/null
  json "$work/pull-$1.json" 'print(v["number"])' > "$work/pr-$1.number"
  json "$work/pull-$1.json" 'print(v["sha"])' > "$work/pr-$1.sha"
  json "$work/pull-$1.json" 'print(v["url"])' > "$work/pr-$1.url"
  printf '%s %s slot=%s pr=#%s %s\n' "$(date -u +%FT%TZ)" "$case_name" "$1" "$(cat "$work/pr-$1.number")" "$(cat "$work/pr-$1.url")" >> "${AFT_WORK_DIR}/matrix-prs.log"
}

expect_base() { # expect_base <slot> <main|slot>
  local want="$2"
  [[ "$want" == main ]] || want="$(pull_field "$2" head)"
  wait_until $((30 * scale)) "PR of $1 based on $2 ($want)" eq "$want" pull_field "$1" base
}

# only_own_file <slot>: against its base, the PR changes exactly the task's file
# (and review fix-up files), so it was rebuilt on that base.
only_own_file() {
  local base head
  base="$(pull_field "$1" base)"
  head="$(pull_field "$1" head)"
  changed_files "$base" "$head" > "$work/changed-$1.txt"
  grep -qx "$(file_of "$1")" "$work/changed-$1.txt" || return 1
  ! grep -v -e "^$(file_of "$1")$" -e "^matrix-$case_name-$1-review" "$work/changed-$1.txt" | grep -q .
}

# Dependents (Tyson, 2026-10-09): a dependent starts as soon as its blocker's
# agent finishes, on the blocker's frozen, unreviewed revision; Approve, Apply
# and Publish still follow dependency order.
blocker_ready() { # blocker_ready <slot>: may a task blocked by <slot> start now?
  revisions "$1" 2> /dev/null || return 1
  json "$work/rev-$1.json" 'assert v.get("number"), v'
}

# --- the lead ------------------------------------------------------------------
# Real tier: a REAL codex lead (the workspace's Lead agent, controlled runtime as
# in live-interactive-suites/ll-lead-assignment) is started from its agent page,
# and each lead action is delivered to the RUNNING lead mid-session: the operator
# types the instruction into the lead's terminal. Whatever the lead then runs
# reaches Loom as the lead, and every expectation is checked on Loom and the
# forge, never on the model's wording.
# Fake tier: no model; the harness makes the same request with actor kind lead
# (a labelled stand-in), so the authorization rules are still exercised.

lead_say() { # lead_say <instruction>: type one line into the running lead's terminal
  browser open "$AFT_BASE_URL/ws/$workspace/agents/lead" > /dev/null
  browser wait '[data-testid="terminal-wrapper"] .wterm' > /dev/null
  browser click '[data-testid="terminal-wrapper"]' > /dev/null
  browser keyboard inserttext "$1" > /dev/null
  browser press Enter > /dev/null
  printf '%s %s\n' "$(date -u +%FT%TZ)" "$1" >> "$work/lead-instructions.log"
}

lead_terminal_text() {
  browser eval "Array.from(document.querySelectorAll('[data-testid=terminal-wrapper] .term-row')).map(e => e.textContent).join('\\n')" > "$work/lead-terminal.txt" 2> /dev/null || true
}

lead_mark() { lead_terminal_text; grep -cE "$1" "$work/lead-terminal.txt" > "$work/lead-mark.count" || true; }
lead_refused() { # the real lead ran the command and Loom's refusal is in its transcript
  local want="$1" before
  before="$(cat "$work/lead-mark.count" 2> /dev/null || echo 0)"
  seen() { lead_terminal_text; (( $(grep -cE "$want" "$work/lead-terminal.txt" || true) > before )); }
  wait_until $((90 * scale)) "the lead's transcript shows Loom refusing it (/$want/): $(tail -c 800 "$work/lead-terminal.txt" 2> /dev/null)" seen
  cp "$work/lead-terminal.txt" "$work/lead-refusal-$(date +%s).txt"
}

verdict_by_lead() { revisions "$1"; json "$work/rev-$1.json" 'assert v.get("verdict")=="policy", v'; }

# --- phases --------------------------------------------------------------------

diagnose() {
  {
    echo "== settings"; settings_json || true; echo
    for id in "$work"/task-*.id; do
      [[ -f "$id" ]] || continue
      slot="$(basename "$id" .id)"; slot="${slot#task-}"
      echo "== task $slot"; curl -sS "$api/issues/$(cat "$id")" || true; echo
      echo "== revisions $slot"; curl -sS "$api/issues/$(cat "$id")/revisions" || true; echo
      if [[ -f "$work/change-$slot.id" ]]; then
        echo "== merge approval $slot"; curl -sS "$api/changes/$(cat "$work/change-$slot.id")/merge-approval" || true; echo
      fi
    done
    echo "== pulls"; cat "$work/pulls.json" 2>/dev/null || true; echo
    echo "== stacks"; loom stack list 2>&1 || true
  } > "$work/diagnosis-$phase.txt" 2>&1
  echo "[$case_name] diagnosis: $work/diagnosis-$phase.txt" >&2
}
trap 'diagnose' ERR

case "$phase" in
setup)
  # setup <case> [no-native]: one repo, one workspace and its lead. GitHub always
  # uses native stacks (D41), so the fake forge offers them unless no-native.
  native=native
  [[ "${1:-}" == no-native ]] && native=""
  [[ "${1:-}" == no-native && "$forge" == github ]] && fail "no-native cannot be simulated on real GitHub"
  mkdir -p "$work"
  printf '%s\n' "$forge_repo" > "$work/forge-repo"
  if [[ "$forge" == fake ]]; then
    git init -q --bare "$remote"
    git -C "$remote" symbolic-ref HEAD refs/heads/main
    git init -q -b main "$repo"
    printf 'matrix %s\n' "$case_name" > "$repo/README.md"
    git -C "$repo" add README.md
    git -C "$repo" -c user.name=AFT -c user.email=aft@example.test commit -q -m base
    git -C "$repo" config core.sshCommand "sh '$AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh' '$remote'"
    git -C "$repo" remote add origin "git@github.com:$forge_repo.git"
    git -C "$repo" push -q origin main
    python3 -c 'import json,sys; print(json.dumps(dict(repo=sys.argv[1],remote=sys.argv[2],native_stacks=sys.argv[3]=="native",content_checks=True)))' "$forge_repo" "$remote" "$native" |
      curl -fsS -X POST "$AFT_FAKE_GH_BASE/__register" -H 'Content-Type: application/json' -d @- > "$work/register.json"
  else
    # The clone has no stored credential; the Loom server pushes with its own.
    # shellcheck disable=SC2016 # the helper expands the variable when git runs it
    AFT_GH_CLONE_TOKEN="$(gh auth token)" git -c credential.helper= \
      -c 'credential.helper=!f() { echo username=x-access-token; echo "password=$AFT_GH_CLONE_TOKEN"; }; f' \
      clone -q "https://github.com/$forge_repo.git" "$repo"
    git -C "$repo" config --get credential.helper > /dev/null && fail "the clone stored a credential helper"
  fi
  python3 -c 'import json,sys; print(json.dumps(dict(name=sys.argv[1],type="empty",repos=[sys.argv[2]])))' "$(tr '[:upper:]' '[:lower:]' <<< "$workspace")" "$repo" |
    curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' -d @- > "$work/workspace.json"
  curl -fsS -X POST "$api/agents" -H 'Content-Type: application/json' \
    -d '{"name":"lead","role_name":"lead","auto":false,"cross_repo":true,"repos":[],"backend":"codex"}' > "$work/lead.json"
  say "workspace $workspace on $forge forge repo $forge_repo"
  ;;

settings)
  # settings <case> <stack|trunk> <on|off> <off|when_green>: a human sets the
  # three Git settings in Settings > Git; the server and a reload agree.
  mode="$1" approve="$2" merge="$3"
  open_settings
  ui_select git-delivery-mode "$mode"
  ui_select git-lead-may-approve "$approve"
  ui_select git-lead-may-merge "$merge"
  [[ "$merge" == when_green ]] && browser wait '[data-testid="git-merge-warning"]' > /dev/null
  want_approve=false
  [[ "$approve" == on ]] && want_approve=true
  wait_until 10 "settings saved" expect_settings "$mode" "$want_approve" "$merge"
  open_settings
  test "$(ui_value git-delivery-mode)" = "$mode" || fail "reload shows another delivery mode"
  test "$(ui_value git-lead-may-approve)" = "$approve" || fail "reload shows another Lead may approve"
  test "$(ui_value git-lead-may-merge)" = "$merge" || fail "reload shows another Lead may merge"
  browser screenshot "$work/settings-$mode-$approve-$merge.png" > /dev/null
  ;;

task)
  # task <case> <slot> [after-slot|-] [FAIL]: one task in its own epic, blocked by
  # the task below it; its run writes matrix-<case>-<slot>.txt (with FAIL: red).
  slot="$1" after="${2:--}" red="${3:-}"
  file="$(file_of "$slot")"
  if [[ "$forge" == fake ]]; then
    design="STUB_CODEX_PATCH=$file"
    [[ "$red" == FAIL ]] && design="$design STUB_CODEX_TEXT=FAIL"
  else
    line="$case_name $slot ok"
    [[ "$red" == FAIL ]] && line="FAIL"
    design="Create a new file named $file at the repository root whose entire contents are exactly the single line: $line. Do not create, modify or delete any other file. Do not run git commands."
  fi
  python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1]+" epic","issue_type":"epic","priority":2}))' "matrix $case_name $slot" |
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$work/epic-$slot.json"
  epic="$(json "$work/epic-$slot.json" 'print(v["data"]["id"])')"
  python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"issue_type":"task","priority":2,"parent":sys.argv[2],"design":sys.argv[3]}))' "matrix $case_name $slot" "$epic" "$design" |
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$work/task-$slot.json"
  json "$work/task-$slot.json" 'print(v["data"]["id"])' > "$work/task-$slot.id"
  if [[ "$after" != - ]]; then
    blocker_ready "$after" || fail "task $slot would start before its blocker $after is ready"
    curl -fsS -X POST "$api/issues/$(task_id "$slot")/dependencies" -H 'Content-Type: application/json' \
      -d "{\"depends_on_id\":\"$(task_id "$after")\",\"dep_type\":\"blocks\"}" > /dev/null
  fi
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$work/workflow-$slot.json"
  say "task $slot $(task_id "$slot") started (writes $file${red:+, red})"
  ;;

wait-rev)
  # wait-rev <case> <slot> <try> <tries>: bounded under AFT's 120 s run step; the
  # last try fails. Ends with the task in review with the code-review label.
  slot="$1" try="$2" tries="$3"
  got=""
  for _ in $(seq 1 50); do # wait_until would fail the step; a non-last try must pass
    got="$(rev_field "$slot" head_sha 2> /dev/null || true)"
    [[ -n "$got" ]] && break
    sleep 2
  done
  if [[ -z "$got" ]]; then
    ((try < tries)) && { say "task $slot still running (try $try/$tries)"; exit 0; }
    fail "task $slot recorded no revision"
  fi
  json "$work/rev-$slot.json" 'print(v["change_id"])' > "$work/change-$slot.id"
  curl -fsS "$api/issues/$(task_id "$slot")" > "$work/issue-$slot.json"
  json "$work/issue-$slot.json" 'd=v["data"]; assert d["status"]=="review" and "code-review" in (d.get("labels") or []), d'
  say "task $slot in review: revision $(json "$work/rev-$slot.json" 'print(v["number"],v["head_sha"])')"
  ;;

open-task)
  open_task "$1"
  ;;

chain)
  # chain <case> <slot[:after][:FAIL][:writes-slot]>...: one epic holding all the tasks, each
  # dependent blocked by its "after" slot; the epic runner is started once, so a
  # dependent runs when its blocker's agent finishes (no review in between).
  python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1]+" chain epic","issue_type":"epic","priority":2}))' "matrix $case_name" |
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$work/epic-chain.json"
  epic="$(json "$work/epic-chain.json" 'print(v["data"]["id"])')"
  for spec in "$@"; do
    IFS=: read -r slot after red writes <<< "$spec"
    [[ -n "$writes" ]] && printf '%s\n' "$writes" > "$work/writes-$slot"
    file="$(file_of "$slot")"
    if [[ "$forge" == fake ]]; then
      design="STUB_CODEX_PATCH=$file"
      [[ "$red" == FAIL ]] && design="$design STUB_CODEX_TEXT=FAIL"
    else
      line="$case_name $slot ok"
      [[ "$red" == FAIL ]] && line="FAIL"
      if [[ -n "$writes" ]]; then
        design="Replace the entire contents of the existing file $file at the repository root with exactly the single line: $line. Do not create, modify or delete any other file. Do not run git commands."
      else
        design="Create a new file named $file at the repository root whose entire contents are exactly the single line: $line. Do not create, modify or delete any other file. Do not run git commands."
      fi
    fi
    python3 -c 'import json,sys; print(json.dumps({"title":sys.argv[1],"issue_type":"task","priority":2,"parent":sys.argv[2],"design":sys.argv[3]}))' "matrix $case_name $slot" "$epic" "$design" |
      curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$work/task-$slot.json"
    json "$work/task-$slot.json" 'print(v["data"]["id"])' > "$work/task-$slot.id"
    cp "$work/epic-chain.json" "$work/epic-$slot.json"
    if [[ -n "$after" && "$after" != - ]]; then
      curl -fsS -X POST "$api/issues/$(task_id "$slot")/dependencies" -H 'Content-Type: application/json' \
        -d "{\"depends_on_id\":\"$(task_id "$after")\",\"dep_type\":\"blocks\"}" > /dev/null
    fi
  done
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$work/workflow-chain.json"
  say "chain $* started in epic $epic"
  ;;

ran-before-approval)
  # ran-before-approval <case> <slot> <blocker>: the dependent has its own
  # revision while its blocker is still unreviewed, built on the blocker's
  # frozen revision.
  slot="$1" blocker="$2"
  revisions "$blocker"
  json "$work/rev-$blocker.json" 'assert not v.get("verdict"), "blocker already reviewed: %r" % v'
  curl -fsS "$api/issues/$(task_id "$blocker")" > "$work/issue-$blocker.json"
  json "$work/issue-$blocker.json" 'assert v["data"]["status"]=="review", v["data"]'
  revisions "$slot"
  bhead="$(rev_field "$blocker" head_sha)" shead="$(rev_field "$slot" head_sha)"
  [[ -n "$shead" ]] || fail "dependent $slot has no revision while $blocker is unreviewed"
  git -C "$repo" fetch -q origin 2> /dev/null || true
  if git -C "$repo" cat-file -e "$shead^{commit}" 2> /dev/null && git -C "$repo" cat-file -e "$bhead^{commit}" 2> /dev/null; then
    git -C "$repo" merge-base --is-ancestor "$bhead" "$shead" || fail "$slot's revision $shead is not built on $blocker's frozen revision $bhead"
    say "$slot ($shead) ran on $blocker's unreviewed revision $bhead"
  else
    say "$slot has revision $shead while $blocker ($bhead) is unreviewed (revision objects not in the workspace clone; ancestry not checked)"
  fi
  ;;

approve-waits)
  # approve-waits <case> <slot> <blocker>: (the UI step approved <slot> first)
  # the approval is recorded but waits for the blocker's approval; no PR.
  slot="$1" blocker="$2"
  waiting() { revisions "$slot"; json "$work/rev-$slot.json" 'assert v.get("verdict")=="approve", v'; grep -qi 'to be approved' "$work/rev-$slot.json"; }
  wait_until 30 "$slot's approval waits for $blocker to be approved: $(cat "$work/rev-$slot.json" 2> /dev/null | head -c 600)" waiting
  sleep $((5 * scale))
  [[ -z "$(pull_field "$slot" number)" ]] || fail "$slot got a PR before its blocker $blocker was approved"
  revisions "$blocker"
  json "$work/rev-$blocker.json" 'assert not v.get("verdict"), v'
  open_task "$slot"
  browser wait --text 'to be approved' > /dev/null || fail "task $slot does not say it waits for $blocker to be approved"
  browser screenshot "$work/approve-waits-$slot.png" > /dev/null
  say "$slot's approval waits: $(json "$work/rev-$slot.json" 'print(v.get("publish_reason") or v.get("merge_reason") or "")')"
  ;;

stale)
  # stale <case> <slot> <blocker>: after the blocker was rejected, the dependent's
  # revision is stale and the task offers a rebuild; it has no PR.
  slot="$1" blocker="$2"
  # A truthy stale flag or a status/reason saying stale; a "stale": false field does not count.
  is_stale() {
    revisions "$slot"
    json "$work/rev-$slot.json" '
def hit(x):
    if isinstance(x, dict):
        return any(("stale" in k.lower() and bool(w) and w is not False) or hit(w) for k, w in x.items())
    if isinstance(x, list):
        return any(hit(w) for w in x)
    return isinstance(x, str) and "stale" in x.lower()
assert hit(v), v'
  }
  wait_until 30 "$slot is stale after $blocker was rejected: $(cat "$work/rev-$slot.json" 2> /dev/null | head -c 600)" is_stale
  [[ -z "$(pull_field "$slot" number)" ]] || fail "stale $slot has a PR"
  open_task "$slot"
  rebuild() { browser eval "[...document.querySelectorAll('button')].some(b => /rebuild/i.test(b.textContent) && !b.disabled)" | grep -q true; }
  wait_until 20 "task $slot offers a rebuild" rebuild
  browser screenshot "$work/stale-$slot.png" > /dev/null
  say "$slot is stale and offers a rebuild"
  ;;

native-unavailable)
  # native-unavailable <case> <slot>: (the UI step approved <slot>) with native
  # stacks unavailable, publishing fails with a clear error and no PR opens;
  # there is no fallback to Loom's own publisher (D41).
  slot="$1"
  settled() { revisions "$slot"; json "$work/rev-$slot.json" 'assert v.get("publish_status") not in (None, "", "pending", "publishing"), v'; }
  wait_until 60 "publishing of $slot settled: $(cat "$work/rev-$slot.json" 2> /dev/null | head -c 500)" settled
  pulls || fail "the forge's PR list is unreachable"
  [[ -z "$(pull_field "$slot" number)" ]] || fail "PR #$(pull_field "$slot" number) opened without native stacks: Loom fell back to its own publisher ($(json "$work/rev-$slot.json" 'print(v.get("publish_status"), v.get("publish_reason"))'))"
  json "$work/rev-$slot.json" 'import re; assert v.get("publish_status") != "published" and re.search(r"native stack", v.get("publish_reason") or "", re.I), v'
  open_task "$slot"
  browser wait --text 'native stack' > /dev/null || fail "task $slot does not show the native stacks error"
  browser screenshot "$work/native-unavailable-$slot.png" > /dev/null
  say "no native stacks: $(json "$work/rev-$slot.json" 'print(v.get("publish_status"), "-", v.get("publish_reason"))')"
  ;;

lead-start)
  # lead-start <case>: the real tier launches the workspace's real codex Lead
  # from its agent page and waits for its controlled runtime.
  if [[ "$forge" == fake ]]; then say "fake tier: lead actions use the API stand-in"; exit 0; fi
  browser open "$AFT_BASE_URL/ws/$workspace/agents/lead" > /dev/null
  browser wait '[data-testid="terminal-wrapper"] .wterm' > /dev/null
  started() { lead_terminal_text; grep -qE 'Launching controlled .*lead session' "$work/lead-terminal.txt"; }
  wait_until 110 "real lead runtime started: $(tail -c 400 "$work/lead-terminal.txt" 2> /dev/null)" started
  browser screenshot "$work/lead-started.png" > /dev/null
  say "real codex lead is running"
  ;;

lead-do)
  # lead-do <case> <action> <slot|-> <expect>
  #   approve       ok|refused  Approve and create PR through the verdict API as the lead
  #   cli-approve   refused     the lead runs `loom approve` (must not bypass the policy; needs P2.25)
  #   merge         refused     Approve and merge through the API as the lead (D38)
  #   request-merge refused     the lead runs `loom merge <task>` for <slot> (D38: refused while Lead may merge is off, nothing queued)
  #   set-approve   refused     the lead turns Lead may approve off
  #   set-merge     refused     the lead turns Lead may merge to when green
  #   set-mode      ok          the lead switches delivery mode to PR per task
  action="$1" slot="$2" want="$3"
  if [[ "$slot" != - ]]; then
    revisions "$slot"
    read -r change number sha pr_head < <(json "$work/rev-$slot.json" 'print(v["change_id"],v["number"],v["head_sha"],v.get("pr_head") or "-")')
    printf '%s\n' "$change" > "$work/change-$slot.id"
  fi
  case "$action" in
    approve) url="$api/changes/$change/revisions/$number/verdict"; method=POST
      body="{\"head_sha\":\"$sha\",\"verdict\":\"approve\",\"actor\":{\"kind\":\"lead\",\"id\":\"lead\"}}" ;;
    merge) url="$api/changes/$change/merge-approval"; method=POST
      body="{\"lead\":\"lead\",\"head_sha\":\"$pr_head\",\"actor\":{\"kind\":\"lead\",\"id\":\"lead\"}}" ;;
    request-merge) ;;
    set-approve) url="$api/git/settings"; method=PUT; body='{"actor":{"kind":"lead","id":"lead"},"lead_may_approve_publish":false}' ;;
    set-merge) url="$api/git/settings"; method=PUT; body='{"actor":{"kind":"lead","id":"lead"},"lead_may_merge":"when_green"}' ;;
    set-mode) url="$api/git/settings"; method=PUT; body='{"actor":{"kind":"lead","id":"lead"},"delivery_mode":"trunk"}' ;;
    cli-approve) ;;
    *) fail "unknown lead action $action" ;;
  esac
  settings_json > "$work/settings-before-$action.json"
  # Loom's own refusal text, never a bare status or exit code: an unknown
  # command, a wrong flag or a 400 must not count as a refusal.
  case "$action" in
    # The verdict API's 409 body carries only Loom's error code, not the reason.
    approve) refusal='review_required' ;;
    cli-approve) refusal='lead approval policy is off' ;;
    # D38: with Lead may merge off the lead's merge is refused outright (P3.16).
    merge | request-merge) refusal='Lead may merge is off' ;;
    set-approve | set-merge) refusal='only a human can change workspace policy' ;;
    *) refusal='' ;;
  esac
  [[ "$forge" == github && -n "$refusal" ]] && lead_mark "$refusal"
  if [[ "$forge" == fake ]]; then
    if [[ "$action" == cli-approve || "$action" == request-merge ]]; then
      set +e
      if [[ "$action" == cli-approve ]]; then
        LOOM_AGENT_NAME=lead LOOM_AGENT_ROLE=lead LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" approve "$change" "$number" --workspace "$workspace" > "$work/lead-$action.out" 2>&1
      else
        LOOM_AGENT_NAME=lead LOOM_AGENT_ROLE=lead LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" merge "$(task_id "$slot")" --workspace "$workspace" > "$work/lead-$action.out" 2>&1
      fi
      code=$?
      set -e
    else
      code="$(curl -sS --max-time 60 -o "$work/lead-$action.out" -w '%{http_code}' -X "$method" "$url" -H 'Content-Type: application/json' -d "$body")"
    fi
    say "lead stand-in $action: $code $(head -c 300 "$work/lead-$action.out")"
  else
    if [[ "$action" == cli-approve ]]; then
      lead_say "Loom test harness: approve change $change revision $number of workspace $workspace by running exactly: loom approve $change $number --workspace $workspace . Then report the command output."
    elif [[ "$action" == request-merge ]]; then
      lead_say "Loom test harness: merge task $(task_id "$slot")'s stack by running exactly: loom merge $(task_id "$slot") --workspace $workspace . Then report the command output."
    else
      lead_say "Loom test harness: as the lead, run exactly this one command and report the HTTP status it prints: curl -sS -w '%{http_code}' -X $method '$url' -H 'Content-Type: application/json' -d '$body'"
    fi
  fi
  [[ "$forge" == github && "$want" == refused ]] && lead_refused "$refusal"
  if [[ "$forge" == fake && "$want" == refused ]]; then
    [[ "$code" != 0 && "$code" != 2?? ]] || fail "lead $action succeeded with exit/HTTP $code: $(head -c 400 "$work/lead-$action.out")"
    grep -qF "$refusal" "$work/lead-$action.out" || fail "lead $action was not refused by Loom (want \"$refusal\"; got $code: $(head -c 400 "$work/lead-$action.out"))"
  fi
  case "$action:$want" in
    approve:ok)
      if [[ "$forge" == fake ]]; then
        [[ "$code" == 200 ]] || { [[ "$code" == 409 ]] && grep -q publish_failed "$work/lead-$action.out"; } || fail "lead approval: HTTP $code"
      fi
      wait_until $((60 * scale)) "a policy verdict by the lead on $slot" verdict_by_lead "$slot" ;;
    approve:refused | cli-approve:refused)
      [[ "$forge" == fake && "$action" == approve ]] && { [[ "$code" == 409 ]] && grep -q review_required "$work/lead-$action.out" || fail "lead approval with Lead may approve off: HTTP $code"; }
      for _ in $(seq 1 $((15 * scale))); do
        revisions "$slot"
        json "$work/rev-$slot.json" 'assert not v.get("verdict"), "the lead got a verdict recorded: %r" % v' || fail "$action by the lead was not refused: $(cat "$work/rev-$slot.json")"
        sleep 2
      done
      curl -fsS "$api/issues/$(task_id "$slot")" > "$work/issue-$slot.json"
      json "$work/issue-$slot.json" 'assert v["data"]["status"]=="review", v' ;;
    merge:refused | request-merge:refused)
      for _ in $(seq 1 $((10 * scale))); do
        got="$(approval_state "$slot" || true)"
        [[ "${got%%|*}" != waiting && "${got%%|*}" != merging && "${got%%|*}" != merged ]] || fail "the lead's merge was accepted: $got"
        curl -fsS "$api/git/merge-queue" > "$work/merge-queue.json"
        json "$work/merge-queue.json" 'assert not [x for x in (v or []) if x.get("target")==sys.argv[2]], "a lead merge was queued: %r" % v' "$change" || fail "the lead's merge was queued: $(cat "$work/merge-queue.json")"
        pulls || fail "the forge's PR list is unreachable"
        [[ "$(pull_field "$slot" merged)" != True ]] || fail "PR of $slot merged after the lead's refused merge"
        sleep 1
      done ;;
    set-approve:refused | set-merge:refused)
      [[ "$forge" == fake ]] && { [[ "$code" == 403 ]] || fail "lead $action: HTTP $code, want 403"; }
      sleep $((5 * scale))
      settings_json > "$work/settings-after-$action.json"
      cmp -s "$work/settings-before-$action.json" "$work/settings-after-$action.json" || fail "the lead changed a human-only setting: $(cat "$work/settings-after-$action.json")" ;;
    set-mode:ok)
      [[ "$forge" == fake ]] && { [[ "$code" == 200 ]] || fail "lead $action: HTTP $code, want 200"; }
      mode_trunk() { settings_json | python3 -c 'import json,sys; assert json.load(sys.stdin)["delivery_mode"]=="trunk"'; }
      wait_until $((30 * scale)) "the lead switched delivery mode" mode_trunk ;;
    *) fail "unknown expectation $action:$want" ;;
  esac
  [[ "$forge" == github ]] && { lead_terminal_text; browser screenshot "$work/lead-$action-${slot//-/x}.png" > /dev/null; }
  say "lead $action on $slot: $want"
  ;;

settings-ui)
  # settings-ui <case> <stack|trunk> <on|off> <off|when_green>: the Settings UI shows these values.
  open_settings
  test "$(ui_value git-delivery-mode)" = "$1" || fail "UI delivery mode $(ui_value git-delivery-mode)"
  test "$(ui_value git-lead-may-approve)" = "$2" || fail "UI Lead may approve $(ui_value git-lead-may-approve)"
  test "$(ui_value git-lead-may-merge)" = "$3" || fail "UI Lead may merge $(ui_value git-lead-may-merge)"
  browser screenshot "$work/settings-ui-$1-$2-$3.png" > /dev/null
  ;;

lead-epic-midsession)
  # Real tier only: assign an epic to the ALREADY RUNNING lead and wait for the
  # lead process to mark it delivered (the mid-session seam ll-lead-assignment
  # leaves uncovered).
  [[ "$forge" == github ]] || fail "lead-epic-midsession needs the real tier's running lead (a skip must not read as a pass)"
  python3 -c 'import json; print(json.dumps({"title":"matrix mid-session epic","issue_type":"epic","priority":2}))' |
    curl -fsS -X POST "$api/issues" -H 'Content-Type: application/json' -d @- > "$work/midsession-epic.json"
  epic="$(json "$work/midsession-epic.json" 'print(v["data"]["id"])')"
  curl -fsS -X PATCH "$api/agents/lead" -H 'Content-Type: application/json' -d "{\"parent\":\"$epic\"}" > /dev/null
  delivered() {
    curl -fsS "$AFT_BASE_URL/api/monitor/status?workspace=$workspace" > "$work/midsession-monitor.json"
    json "$work/midsession-monitor.json" 'a=[x for x in v.get("agents",[]) if x.get("name")=="lead"][0]; assert a.get("parent")==sys.argv[2] and a.get("delivery_state")=="delivered", a' "$epic"
  }
  wait_until 90 "epic assigned to the running lead was never delivered: $(cat "$work/midsession-monitor.json" 2> /dev/null | head -c 600)" delivered
  say "mid-session epic assignment delivered"
  ;;

reject)
  # reject <case> <slot> <text>: (UI step does the click) - wait until the task reopens.
  reopened() { curl -fsS "$api/issues/$(task_id "$1")" > "$work/issue-$1.json"; json "$work/issue-$1.json" 'd=v["data"]; assert d["status"]=="open" and "code-review" not in (d.get("labels") or []), d'; }
  wait_until 30 "task $1 reopened after Reject" reopened "$1"
  revisions "$1"
  json "$work/rev-$1.json" 'assert v.get("verdict")=="reject", v'
  [[ -z "$(pull_field "$1" number)" ]] || fail "rejected task $1 has a PR"
  say "task $1 rejected and open again"
  ;;

rerun)
  # rerun <case> <slot>: the epic runner starts another attempt of the reopened task.
  epic="$(json "$work/epic-$1.json" 'print(v["data"]["id"])')"
  revisions "$1"
  json "$work/rev-$1.json" 'print(v["number"])' > "$work/rejected-$1.number"
  curl -fsS -X POST "$api/workflows/epic-runner" -H 'Content-Type: application/json' \
    -d "{\"epicId\":\"$epic\",\"runner\":\"local-task-runner\"}" > "$work/workflow-$1-rerun.json"
  say "task $1 rerun started"
  ;;

human-approve-api)
  # Only for set-up steps no case is about; every checked human action is a UI click.
  slot="$1"
  code="$(verdict "$slot" human aft-operator)"
  [[ "$code" == 200 ]] || fail "human approval: HTTP $code $(cat "$work/verdict-$slot.json")"
  ;;

pr)
  # pr <case> <slot> <main|base-slot>: the task's PR opens on the forge, based on
  # main or on the PR of base-slot, and the task shows the same PR.
  slot="$1" base="$2"
  revisions "$slot"
  json "$work/rev-$slot.json" 'print(v["change_id"])' > "$work/change-$slot.id"
  wait_until $((45 * scale)) "PR of $slot open and shown in Loom" publish_settled "$slot"
  expect_base "$slot" "$base"
  record_pr "$slot"
  file_at "$(pull_field "$slot" head)" "$(file_of "$slot")" > "$work/pr-file-$slot.txt" || fail "PR of $slot lacks $(file_of "$slot")"
  curl -fsS "$api/issues/$(task_id "$slot")" > "$work/issue-$slot.json"
  json "$work/issue-$slot.json" 'd=v["data"]; assert d["status"]=="closed" and "code-review" not in (d.get("labels") or []), d'
  say "PR #$(cat "$work/pr-$slot.number") of $slot on $(pull_field "$slot" base): $(cat "$work/pr-$slot.url")"
  ;;

no-pr)
  # no-pr <case> <slot> <seconds>: no PR opens for the task meanwhile.
  slot="$1" seconds="$2"
  revisions "$slot"
  json "$work/rev-$slot.json" 'print(v["change_id"])' > "$work/change-$slot.id"
  for _ in $(seq 1 "$seconds"); do
    pulls || fail "the forge's PR list is unreachable; cannot prove that no PR opened"
    [[ -z "$(pull_field "$slot" number)" ]] || fail "task $slot opened PR #$(pull_field "$slot" number)"
    sleep 1
  done
  say "no PR for $slot after ${seconds}s; publish: $(json "$work/rev-$slot.json" 'print(v.get("publish_status"),v.get("publish_reason"))')"
  ;;

merged)
  # merged <case> <slot>...: each PR merges on the forge and Loom shows it merged.
  for slot in "$@"; do
    wait_until $((60 * scale)) "PR of $slot merged" pr_merged "$slot"
    wait_until $((30 * scale)) "task $slot shows its PR merged" eq merged rev_field "$slot" pr_state
    say "PR of $slot merged"
  done
  ;;

hold-open)
  # hold-open <case> <seconds> <slot>...: none of these PRs merges meanwhile.
  seconds="$1"
  shift
  for _ in $(seq 1 "$seconds"); do
    for slot in "$@"; do pr_open "$slot" || fail "PR of $slot is no longer open"; done
    sleep 1
  done
  say "held open ${seconds}s: $*"
  ;;

merge-state)
  # merge-state <case> <slot> <status> [reason substring]
  slot="$1" want="$2" reason="${3:-}"
  reason="${reason//\#A/#$(cat "$work/pr-a.number" 2> /dev/null || echo A)}"
  reason="${reason//\#B/#$(cat "$work/pr-b.number" 2> /dev/null || echo B)}"
  wait_until $((40 * scale)) "Approve and merge of $slot is $want ($reason): $(approval_state "$slot" 2> /dev/null || true)" has_state "$slot" "$want" "$reason"
  say "merge approval of $slot: $(approval_state "$slot")"
  ;;

no-merge-approval)
  # The task has no pending Approve and merge (none made, or cancelled).
  got="$(approval_state "$1" || true)"
  [[ "${got%%|*}" != waiting && "${got%%|*}" != merging ]] || fail "task $1 still has a pending merge: $got"
  say "task $1 merge approval: ${got:-none}"
  ;;

rebuilt)
  # rebuilt <case> <slot> <main|base-slot>: the PR now sits on that base and,
  # against it, changes only its own file.
  slot="$1" base="$2"
  expect_base "$slot" "$base"
  wait_until $((60 * scale)) "PR of $slot rebuilt on $base" only_own_file "$slot"
  say "PR of $slot on $(pull_field "$slot" base) changes $(tr '\n' ' ' < "$work/changed-$slot.txt")"
  ;;

checks)
  # checks <case> <slot> <red|green>: the forge's required check on the PR head.
  slot="$1" want="$2"
  head="$(pull_field "$slot" sha)"
  if [[ "$forge" == fake ]]; then
    got="$(curl -fsS "$AFT_FAKE_GH_BASE/repos/$forge_repo/commits/$head/check-runs" | python3 -c 'import json,sys; r=json.load(sys.stdin)["check_runs"]; print("red" if any(x["conclusion"]=="failure" for x in r) else "green")')"
  else
    for _ in $(seq 1 60); do
      gh_api "repos/$forge_repo/commits/$head/check-runs" > "$work/checks-$slot.json"
      got="$(json "$work/checks-$slot.json" 'r=[x for x in v["check_runs"] if x["name"]=="check"]; print("pending" if not r or r[0]["status"]!="completed" else ("green" if r[0]["conclusion"]=="success" else "red"))')"
      [[ "$got" != pending ]] && break
      sleep 5
    done
  fi
  [[ "$got" == "$want" ]] || fail "check on PR of $slot is $got, want $want"
  say "check on PR of $slot: $got"
  ;;

comment)
  # comment <case> <slot> <text...>: a reviewer comments on the task's PR (on
  # GitHub for the real tier) and the comment reaches Loom as the signed
  # issue_comment webhook GitHub would send (the sandbox has no public URL).
  slot="$1"
  shift
  body="$*"
  number="$(pull_field "$slot" number)"
  if [[ ! -f "$work/binding.txt" ]]; then
    loom trigger bindings create --route-key github.issue_comment.created --workflow epic-runner --secret "aft-matrix-$case_name" > "$work/binding.txt"
  fi
  if [[ "$forge" == github ]]; then
    gh_api -X POST "repos/$forge_repo/issues/$number/comments" -f body="$body" > "$work/comment-$slot.json"
    association="$(json "$work/comment-$slot.json" 'print(v["author_association"])')"
    login="$(json "$work/comment-$slot.json" 'print(v["user"]["login"])')"
    delivery="github-comment-$(json "$work/comment-$slot.json" 'print(v["id"])')"
  else
    association=OWNER login=reviewer delivery="matrix-$case_name-$slot-$RUN_ID"
  fi
  printf '%s\n' "$delivery" > "$work/delivery-$slot.id"
  python3 - "$forge_repo" "$number" "$body" "$association" "$login" > "$work/webhook-$slot.json" <<'PY'
import json, sys
repo, number, body, association, login = sys.argv[1:]
print(json.dumps({"action": "created", "repository": {"full_name": repo}, "sender": {"login": login},
                  "issue": {"number": int(number), "pull_request": {"url": "relayed"}},
                  "comment": {"body": body, "author_association": association}}))
PY
  signature="$(openssl dgst -sha256 -hmac "aft-matrix-$case_name" -r < "$work/webhook-$slot.json" | cut -d' ' -f1)"
  code="$(curl -sS -o "$work/webhook-response-$slot.json" -w '%{http_code}' -X POST "$api/webhooks/github" \
    -H 'Content-Type: application/json' -H 'X-GitHub-Event: issue_comment' -H "X-GitHub-Delivery: $delivery" \
    -H "X-Hub-Signature-256: sha256=$signature" --data-binary @"$work/webhook-$slot.json")"
  [[ "$code" == 202 ]] || fail "relayed comment webhook: HTTP $code $(cat "$work/webhook-response-$slot.json")"
  pending() {
    curl -fsS "$api/changes/$(cat "$work/change-$slot.id")/feedback" > "$work/feedback-$slot.json"
    json "$work/feedback-$slot.json" 'f=[x for x in v["feedback"] if x.get("delivery_id")==sys.argv[2]]; assert f and f[0]["status"]=="pending", v' "$delivery"
  }
  wait_until 30 "comment recorded as pending feedback" pending
  say "comment on PR #$number recorded as feedback $delivery"
  ;;

fixup)
  # fixup <case> <slot> <rename|green>: the feedback agent addresses the comment
  # in the task copy Loom hands out (stub edit on the fake tier, real codex on the
  # real tier), and Loom imports the fix-up with `loom feedback complete`.
  slot="$1" what="$2"
  delivery="$(cat "$work/delivery-$slot.id")"
  change="$(cat "$work/change-$slot.id")"
  printf '{"attempt":"matrix-%s-%s"}' "$slot" "$RUN_ID" |
    curl -fsS -X POST "$api/changes/$change/feedback/$delivery/address" -H 'Content-Type: application/json' --data-binary @- > "$work/address-$slot.json"
  target="$(json "$work/address-$slot.json" 'print(v["target"])')"
  json "$work/address-$slot.json" 'print(v["prompt"])' > "$work/feedback-prompt-$slot.txt"
  file="$(file_of "$slot")"
  if [[ "$forge" == fake ]]; then
    case "$what" in
      rename) printf 'renamed by review\n' > "$target/matrix-$case_name-$slot-review.txt" ;;
      green) grep -v FAIL "$target/$file" > "$target/$file.tmp" || true; mv "$target/$file.tmp" "$target/$file" ;;
    esac
  else
    codex_bin="${AFT_REAL_CODEX_BIN:?}"
    (cd "$target" && env -u GITHUB_TOKEN -u GH_TOKEN -u SSH_AUTH_SOCK GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
      "$codex_bin" exec --sandbox workspace-write --skip-git-repo-check - < "$work/feedback-prompt-$slot.txt" > "$work/codex-fixup-$slot.log" 2>&1) ||
      fail "codex fix-up exited non-zero; see $work/codex-fixup-$slot.log"
  fi
  git -C "$target" add -A
  git -C "$target" diff --cached --quiet && fail "the fix-up changed nothing"
  git -C "$target" -c user.name=AFT-Agent -c user.email=aft-agent@example.test commit -q -m "Address review $delivery"
  loom feedback complete "$delivery" --capture-sha "$(git -C "$target" rev-parse HEAD)" > "$work/feedback-complete-$slot.txt"
  sed -nE 's/^Feedback addressed by revision ([0-9]+) of .*/\1/p' "$work/feedback-complete-$slot.txt" > "$work/feedback-number-$slot"
  test -s "$work/feedback-number-$slot" || fail "feedback complete: $(cat "$work/feedback-complete-$slot.txt")"
  say "fix-up of $slot is revision $(cat "$work/feedback-number-$slot")"
  ;;

fixup-pushed)
  # fixup-pushed <case> <slot> [above-slot]: Loom pushed the fix-up to the
  # task's PR with no Approve, and the PR above now contains it.
  slot="$1" above="${2:-}"
  pushed() { revisions "$slot"; json "$work/rev-$slot.json" 'assert v.get("feedback_status")=="pushed" and v.get("verdict")=="feedback", v'; }
  wait_until $((45 * scale)) "fix-up of $slot pushed: $(cat "$work/rev-$slot.json" 2> /dev/null)" pushed
  new_head="$(pull_field "$slot" sha)"
  [[ "$new_head" != "$(cat "$work/pr-$slot.sha")" ]] || fail "PR of $slot did not move"
  [[ "$(pull_field "$slot" state)" == open ]] || fail "PR of $slot is not open"
  if [[ -n "$above" ]]; then
    contains() {
      if [[ "$forge" == fake ]]; then
        git --git-dir="$remote" merge-base --is-ancestor "$new_head" "$(pull_field "$above" sha)"
      else
        [[ "$(gh_api "repos/$forge_repo/compare/$new_head...$(pull_field "$above" sha)" --jq .status)" =~ ^(ahead|identical)$ ]]
      fi
    }
    wait_until $((45 * scale)) "PR of $above replayed on the fix-up" contains
  fi
  printf '%s\n' "$new_head" > "$work/pr-$slot.sha"
  say "PR of $slot updated to $new_head without an Approve${above:+; $above replayed on it}"
  ;;

hand-push)
  # hand-push <case> <slot> [file-slot text]: someone pushes a commit to the
  # task's PR by hand: a new file, or <file-slot>'s file rewritten to <text>.
  slot="$1"
  ref="$(pull_field "$slot" head)"
  seen="$(pull_field "$slot" sha)"
  path="matrix-$case_name-hand.txt" text="pushed by hand"
  [[ -n "${2:-}" ]] && path="$(file_of "$2")" text="${3:-pushed by hand}"
  if [[ "$forge" == fake ]]; then
    blob="$(printf '%s\n' "$text" | git --git-dir="$remote" hash-object -w --stdin)"
    tree="$( (git --git-dir="$remote" ls-tree "$seen" | awk -F '\t' -v p="$path" '$2 != p'; printf '100644 blob %s\t%s\n' "$blob" "$path") | git --git-dir="$remote" mktree)"
    foreign="$(git --git-dir="$remote" -c user.name=Someone -c user.email=someone@example.test commit-tree -p "$seen" -m "pushed by hand" "$tree")"
    git --git-dir="$remote" update-ref "refs/heads/$ref" "$foreign" "$seen"
  else
    blob_sha="$(gh_api "repos/$forge_repo/contents/$path?ref=$ref" --jq .sha 2> /dev/null || true)"
    gh_api -X PUT "repos/$forge_repo/contents/$path" -f message="pushed by hand" \
      -f content="$(printf '%s\n' "$text" | base64)" -f branch="$ref" ${blob_sha:+-f sha="$blob_sha"} > "$work/hand-push.json"
    foreign="$(json "$work/hand-push.json" 'print(v["commit"]["sha"])')"
  fi
  printf '%s\n' "$foreign" > "$work/foreign-$slot.sha"
  wait_until $((20 * scale)) "forge shows the hand push" eq "$foreign" pull_field "$slot" sha
  say "hand-pushed $foreign onto PR of $slot"
  ;;

outside-merge)
  # outside-merge <case> <slot>: someone squash-merges the task's PR on the forge,
  # outside Loom (its current head, hand pushes included).
  slot="$1"
  number="$(pull_field "$slot" number)" head="$(pull_field "$slot" sha)"
  if [[ "$forge" == fake ]]; then
    main="$(git --git-dir="$remote" rev-parse refs/heads/main)"
    squash="$(git --git-dir="$remote" -c user.name=Someone -c user.email=someone@example.test commit-tree -p "$main" -m "Merged #$number outside Loom" "$head^{tree}")"
    git --git-dir="$remote" update-ref refs/heads/main "$squash" "$main"
    curl -fsS -X POST "$AFT_FAKE_GH_BASE/__merge" -H 'Content-Type: application/json' -d "{\"number\":$number,\"sha\":\"$squash\"}" > /dev/null
  else
    gh_api -X PUT "repos/$forge_repo/pulls/$number/merge" -f merge_method=squash > "$work/outside-merge.json" ||
      fail "the forge refused the outside merge of #$number: $(cat "$work/outside-merge.json")"
  fi
  wait_until $((20 * scale)) "PR of $slot merged on the forge" eq True pull_field "$slot" merged
  say "PR #$number of $slot merged outside Loom"
  ;;

not-overwritten)
  # not-overwritten <case> <slot> <seconds>: the hand-pushed head stays.
  slot="$1" seconds="$2"
  for _ in $(seq 1 "$seconds"); do
    [[ "$(pull_field "$slot" sha)" == "$(cat "$work/foreign-$slot.sha")" ]] || fail "Loom overwrote the hand push on $slot"
    [[ "$(pull_field "$slot" merged)" != True ]] || fail "the hand-pushed PR of $slot merged"
    sleep 1
  done
  say "hand push on $slot kept for ${seconds}s"
  ;;

snapshot)
  # snapshot <case> <name>: every PR of the case, for comparing later.
  pulls
  cp "$work/pulls.json" "$work/snapshot-$1.json"
  ;;

untouched)
  # untouched <case> <name> [min-open]: the PRs in the snapshot are unchanged
  # (and at least <min-open> of them were open when it was taken).
  pulls
  python3 - "$work/snapshot-$1.json" "$work/pulls.json" "${2:-0}" <<'PY'
import json, sys
before, after = json.load(open(sys.argv[1])), {p["number"]: p for p in json.load(open(sys.argv[2]))}
assert sum(p["state"] == "open" for p in before) >= int(sys.argv[3]), ("too few open PRs in the snapshot", before)
for p in before:
    q = after[p["number"]]
    assert (q["state"], q["merged"], q["base"], q["head"], q["sha"]) == (p["state"], p["merged"], p["base"], p["head"], p["sha"]), (p, q)
PY
  say "PRs of snapshot $1 untouched"
  ;;

ui)
  # ui <case> <slot> <open|merged> [merge-status text]: the task page agrees
  # with the forge, before and after a reload.
  slot="$1" want="$2" text="${3:-}"
  text="${text//\#A/#$(cat "$work/pr-a.number" 2> /dev/null || echo A)}"
  text="${text//\#B/#$(cat "$work/pr-b.number" 2> /dev/null || echo B)}"
  number="$(pull_field "$slot" number)"
  [[ -n "$number" ]] || fail "task $slot has no PR"
  case "$want" in
    open) [[ "$(pull_field "$slot" state)" == open ]] || fail "forge PR of $slot is not open" ;;
    merged) [[ "$(pull_field "$slot" merged)" == True ]] || fail "forge PR of $slot is not merged" ;;
  esac
  phrase="is open"
  [[ "$want" == merged ]] && phrase="was merged"
  open_task "$slot"
  for attempt in 1 2; do
    seen=""
    for _ in $(seq 1 20); do
      seen="$(browser eval "[...document.querySelectorAll('[data-testid=\"revision-pr\"]')].map(e => e.textContent).join(' | ')" | tr -d '"')"
      status="$(browser eval "document.querySelector('[data-testid=\"merge-status\"]')?.textContent || ''" | tr -d '"')"
      if [[ "$seen" == *"#$number"* && "$seen" == *"$phrase"* && "$status" == *"$text"* ]]; then break; fi
      sleep 1
    done
    [[ "$seen" == *"#$number"* && "$seen" == *"$phrase"* ]] || fail "task $slot shows '$seen', want PR #$number $phrase"
    [[ "$status" == *"$text"* ]] || fail "task $slot merge status '$status', want '$text'"
    browser screenshot "$work/ui-$slot-$want-$attempt.png" > /dev/null
    [[ "$attempt" == 1 ]] && { browser eval "location.reload()" > /dev/null; browser wait '[data-testid="revisions-section"]' > /dev/null; }
  done
  say "task $slot page: '$seen' / '$status' (kept after reload)"
  ;;

teardown)
  # Close this case's open PRs (real tier: the repo is kept for inspection),
  # then remove the Loom workspace.
  [[ -d "$work" ]] || exit 0
  pulls || true
  if [[ "$forge" == github && -f "$work/pulls.json" ]]; then
    for number in $(json "$work/pulls.json" 'print(" ".join(str(p["number"]) for p in v if p["state"]=="open"))'); do
      gh_api -X PATCH "repos/$forge_repo/pulls/$number" -f state=closed > /dev/null && say "closed PR #$number"
    done
  fi
  AFT_WS="$workspace" "$AFT_TESTS_DIR/scripts/close-open-issues.sh" || true
  if [[ "$forge" == github ]]; then
    "$AFT_TESTS_DIR/scripts/live-close-agent-tab.sh" lead "$workspace" || true
    curl -s -X POST "$api/agents/lead/stop" > /dev/null || true
  fi
  curl -s -X DELETE "$api/agents/lead" > /dev/null || true
  curl -s -X DELETE "$api" > /dev/null || true
  ;;

*)
  echo "unknown phase $phase" >&2
  exit 2
  ;;
esac

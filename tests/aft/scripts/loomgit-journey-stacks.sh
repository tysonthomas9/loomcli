#!/usr/bin/env bash
# Deterministic product journeys at #943. Human mutations live in the YAML UI
# steps. This helper provisions API fixtures, runs the named stub-agent actor,
# and reads actual product/API/Git/provider outcomes. Never seed Loom's journal.
set -Eeuo pipefail

phase="${1:?phase}" case_name="${2:?case}" slot="${3:-}"
case "$case_name" in
  loom-stack|loom-lower|native-stack|native-lower|loom-feedback|native-feedback|trunk-feedback) ;;
  *) echo "Unknown stack journey: $case_name" >&2; exit 2 ;;
esac
: "${AFT_WORK_DIR:?}" "${AFT_TESTS_DIR:?}" "${AFT_BASE_URL:?}"
# Shared helper arrives from the reviewed runner contribution at integration.
# shellcheck source=/dev/null
source "$AFT_TESTS_DIR/scripts/loomgit-journey-common.sh"
if [[ "$phase" == setup ]]; then
  mode=stack backend=loom
  [[ "$case_name" == trunk-feedback ]] && mode=trunk
  [[ "$case_name" == native-* ]] && backend=github
  journey_setup "$case_name" "$mode" "$backend"
else
  [[ "$phase" != teardown || -f "$AFT_WORK_DIR/journey-$case_name/workspace.id" ]] || exit 0
  journey_load "$case_name"
fi
work="$JOURNEY_STATE" workspace="$JOURNEY_WS" api="$JOURNEY_API"
repo="$JOURNEY_REPO" remote="$JOURNEY_REMOTE" forge_repo="$JOURNEY_FORGE_REPO"
export LOOM_CONNECTOR_GITHUB_BASE_URL="${AFT_FAKE_GH_BASE:?Use the reviewed --suite loomgit-* launcher}"
export GITHUB_TOKEN=aft-fixture-token

loom() { LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" "$@" --workspace "$workspace"; }
browser() { agent-browser --session "$AFT_SESSION" "$@"; }
json() { python3 -c "import json,sys; v=json.load(open(sys.argv[1])); $2" "$1" "${@:3}"; }
get() { curl -fsS "$api/$1" > "$2"; }
post() { curl -fsS -X POST "$api/$1" -H 'Content-Type: application/json' --data-binary @- > "$2"; }
id_of() { json "$1" 'print(v["data"]["id"])'; }
task_id() { cat "$work/task-$1.id"; }
newest() {
  get "issues/$(task_id "$1")/revisions?lead=lead" "$work/revisions-$1.json"
  json "$work/revisions-$1.json" 'r=max(v["data"],key=lambda r:r["number"]); print(r["change_id"],r["number"],r["head_sha"])'
}
pulls() { curl -fsS "$AFT_FAKE_GH_BASE/repos/$forge_repo/pulls?state=all" > "$work/pulls.json"; }
pull() {
  pulls
  json "$work/pulls.json" 'p=[p for p in v if p["head"]["ref"].endswith("/change/"+sys.argv[2])]; assert len(p)==1,p; print(json.dumps(p[0]))' "$(cat "$work/change-$1.id")" > "$work/pull-$1.json"
}
receipt() {
  pulls
  curl -fsS "$AFT_FAKE_GH_BASE/__requests" > "$work/provider-all-$1.json"
  json "$work/provider-all-$1.json" 'print(json.dumps([r for r in v if r["path"].startswith("/repos/"+sys.argv[2]+"/")]))' "$forge_repo" > "$work/provider-$1.json"
  cp "$work/pulls.json" "$work/pulls-$1.json"
  git --git-dir="$remote" show-ref > "$work/refs-$1.txt"
  loom stack list --json > "$work/stacks-$1.json"
}
diagnose() {
  [[ -d "$work" ]] || return 0
  for f in "$work"/task-*.id; do
    [[ -f "$f" ]] || continue
    curl -sS "$api/issues/$(cat "$f")/revisions?lead=lead" > "$f.failure-revisions.json" || true
  done
  curl -sS "$AFT_FAKE_GH_BASE/__requests" > "$work/failure-provider.json" || true
  curl -sS "$AFT_FAKE_GH_BASE/__pulls?repo=$forge_repo" > "$work/failure-pulls.json" || true
  echo "Stack journey $case_name failed in $phase $slot; retain $work and the AFT failed-step screenshot." >&2
}
trap diagnose ERR

case "$phase" in
setup)
  if [[ "$case_name" == *feedback ]]; then
    loom trigger bindings create --route-key github.pull_request_review.submitted --workflow epic-runner --secret aft-journey-signing-secret > "$work/binding.txt"
  fi
  ;;
teardown)
  [[ ! -f "$work/cleanup.txt" ]] || exit 0
  journey_teardown
  ;;
attempt)
  # Each new epic has one task. Dependencies remain intact; no forced closure.
  # This arranges an already-approved predecessor, not a run-ahead policy.
  previous="${4:-}"
  if [[ -n "$previous" ]]; then
    get "issues/$(task_id "$previous")" "$work/predecessor-$slot.json"
    json "$work/predecessor-$slot.json" 'assert v["data"]["status"]=="closed" and "code-review" not in (v["data"].get("labels") or []),v'
  fi
  journey_create_task "$slot" "journey-$slot.txt" "$previous"
  journey_start_task "$slot"
  journey_wait_revision "$slot"
  read -r change _ head < <(newest "$slot")
  printf '%s\n' "$change" > "$work/change-$slot.id"
  git -C "$repo" show "$head:journey-$slot.txt" > "$work/source-$slot.txt"
  grep -qF "task=$(task_id "$slot") run=" "$work/source-$slot.txt"
  get "issues/$(task_id "$slot")" "$work/review-$slot.json"
  json "$work/review-$slot.json" 'assert v["data"]["status"]=="review" and "code-review" in v["data"]["labels"],v'
  ;;
open-task)
  journey_open_task "$slot"
  ;;
open-settings)
  browser open "$AFT_BASE_URL/ws/$workspace/settings" >/dev/null
  ;;
published)
  settled=false
  for _ in $(seq 1 45); do
    newest "$slot" > "$work/newest-$slot.txt"
    if json "$work/revisions-$slot.json" 'r=max(v["data"],key=lambda r:r["number"]); assert r.get("pr_head") and r.get("applied"),r' 2>/dev/null; then settled=true; break; fi
    sleep 2
  done
  [[ "$settled" == true ]]
  get "issues/$(task_id "$slot")" "$work/approved-$slot.json"
  json "$work/approved-$slot.json" 'assert v["data"]["status"]=="closed" and "code-review" not in (v["data"].get("labels") or []),v'
  pull "$slot"
  branch="$(json "$work/pull-$slot.json" 'print(v["head"]["ref"])')"
  git --git-dir="$remote" show "refs/heads/$branch:journey-$slot.txt" > "$work/published-$slot.txt"
  cmp "$work/source-$slot.txt" "$work/published-$slot.txt"
  get "issues/$(task_id "$slot")/diff?lead=lead" "$work/diff-$slot.json"
  ;;
chain)
  # Assert order, bases, isolated PR patches, bytes and native membership.
  count="${3:?layer count}"
  for i in $(seq 1 "$count"); do get "issues/$(task_id "$i")/diff?lead=lead" "$work/diff-$i.json"; done
  receipt chain
  if [[ "$case_name" == native-* ]]; then
    pull 1
    curl -fsS "$AFT_FAKE_GH_BASE/repos/$forge_repo/stacks?pull_request=$(json "$work/pull-1.json" 'print(v["number"])')" > "$work/native-chain.json"
  fi
  python3 - "$work" "$remote" "$count" "$case_name" <<'PY'
import json, pathlib, subprocess, sys
w, remote, count, case = pathlib.Path(sys.argv[1]), sys.argv[2], int(sys.argv[3]), sys.argv[4]
pulls = json.load(open(w / 'pulls-chain.json'))
assert len(pulls) == count, pulls
ordered = []
for i in range(1, count + 1):
    c = (w / f'change-{i}.id').read_text().strip()
    p, = [p for p in pulls if p['head']['ref'].endswith('/change/' + c)]
    ordered.append(p)
    base = 'main' if i == 1 else ordered[-2]['head']['ref']
    assert p['state'] == 'open' and p['base']['ref'] == base, p
    git = lambda *a: subprocess.check_output(['git', '--git-dir=' + remote, *a])
    assert git('diff', '--name-only', base, p['head']['ref']).decode().splitlines() == [f'journey-{i}.txt'], p
    for j in range(1, i + 1):
        assert git('show', p['head']['ref'] + f':journey-{j}.txt') == (w / f'source-{j}.txt').read_bytes()
    diff = json.load(open(w / f'diff-{i}.json'))['data']
    assert len(diff) == 1 and [f['path'] for f in diff[0]['files']] == [f'journey-{i}.txt'], diff
    assert diff[0]['compare'] == ('trunk' if i == 1 else 'layer'), diff
    patch=git('diff','--no-ext-diff','--no-textconv','--no-renames','--no-color','--patch',base,p['head']['ref']).decode()
    assert ''.join(f['patch'] for f in diff[0]['files']).rstrip()==patch.rstrip(),diff
requests = json.load(open(w / 'provider-chain.json'))
creates = [r for r in requests if r['method'] == 'POST' and r['path'].endswith('/pulls')]
assert len(creates) == count, creates
if case.startswith('native-'):
    assert any(r['method'] == 'POST' and (r['path'].endswith('/stacks') or r['path'].endswith('/add')) for r in requests), requests
    stacks=json.load(open(w/'native-chain.json'))
    assert len(stacks)==1 and [p['number'] for p in stacks[0]['pull_requests']]==[p['number'] for p in ordered],stacks
lead=subprocess.check_output(['git','-C',str(w/'repo'),'rev-parse','refs/heads/loom/ws/'+(w/'workspace.id').read_text().strip()+'/interactive/lead']).strip()
assert lead.decode()==ordered[-1]['head']['sha'],(lead,ordered[-1])
PY
  ;;
diff)
  get "issues/$(task_id "$slot")/diff?lead=lead" "$work/diff-$slot.json"
  ;;
merge-fields)
  loom stack list --json > "$work/stack-list.json"
  stack="$(json "$work/stack-list.json" 'c=open(sys.argv[2]).read().strip(); s=[s for s in v if s.get("source")=="published" and any(l["change"]==c for l in s["layers"])]; assert len(s)==1,s; print(s[0]["id"])' "$work/change-$slot.id")"
  printf '%s\n' "$stack" > "$work/stack.id"
  printf '%s\n' "$slot" > "$work/target-slot"
  browser open "$AFT_BASE_URL/ws/$workspace/agents/lead" >/dev/null
  browser find role button click --name Git --exact >/dev/null
  browser find role button click --name 'Merge stack' --exact >/dev/null
  browser find label 'Stack ID' fill "$stack" >/dev/null
  browser find label 'Up to layer' fill "$(cat "$work/change-$slot.id")" >/dev/null
  ;;
confirm-merge)
  # One human UI action, including its native confirmation dialog. No fetch,
  # handler call or replacement window.confirm is used to submit the request.
  target="$(cat "$work/change-$(cat "$work/target-slot").id")"
  get "agents/lead/git/merge-up-to?stack_id=$(cat "$work/stack.id")&target=$target" "$work/pinned-preview.json"
  browser find role button click --name 'Confirm merge request' --exact >/dev/null
  browser dialog status > "$work/confirmation-dialog.txt"
  grep -qF "$target" "$work/confirmation-dialog.txt"
  json "$work/pinned-preview.json" '[print(l["head"]) for l in v["layers"]]' > "$work/pinned-heads.txt"
  while read -r head; do grep -qF "$head" "$work/confirmation-dialog.txt"; done < "$work/pinned-heads.txt"
  browser dialog accept >/dev/null
  ;;
merge-done)
  stack="$(cat "$work/stack.id")" target="$(cat "$work/change-$slot.id")"
  done_merge=false
  for _ in $(seq 1 45); do
    get "agents/lead/git/merge-up-to?stack_id=$stack&target=$target" "$work/merge-$slot.json"
    if json "$work/merge-$slot.json" 'assert v["phase"]=="done",v' 2>/dev/null; then done_merge=true; break; fi
    sleep 2
  done
  [[ "$done_merge" == true ]]
  receipt "merge-$slot"
  python3 - "$work" "$remote" "$slot" "$case_name" <<'PY'
import json, pathlib, subprocess, sys
w, remote, k, case = pathlib.Path(sys.argv[1]), sys.argv[2], int(sys.argv[3]), sys.argv[4]
v = json.load(open(w / f'merge-{k}.json'))
assert v['backend'] == ('native' if case.startswith('native-') else 'loom'), v
active=list(range(3,5)) if case=='loom-stack' and k==3 else list(range(1,5))
assert [l['change'] for l in v['layers']]==[(w/f'change-{i}.id').read_text().strip() for i in active],v
assert all(l['state']=='done' for i,l in zip(active,v['layers']) if i<=k),v
assert all(l['state']!='done' for i,l in zip(active,v['layers']) if i>k),v
pulls = json.load(open(w / f'pulls-merge-{k}.json'))
for i in range(1, 5):
    c = (w / f'change-{i}.id').read_text().strip()
    p, = [p for p in pulls if p['head']['ref'].endswith('/change/' + c)]
    if i <= k:
        assert p['merged_at'] and p['state'] == 'closed', p
        got = subprocess.check_output(['git', '--git-dir=' + remote, 'show', f'main:journey-{i}.txt'])
        assert got == (w / f'source-{i}.txt').read_bytes()
    else:
        assert p['state'] == 'open' and not p['merged_at'], p
        if i == k + 1: assert p['base']['ref'] == 'main', p
        assert subprocess.run(['git', '--git-dir=' + remote, 'cat-file', '-e', f'main:journey-{i}.txt'], capture_output=True).returncode != 0
        got = subprocess.check_output(['git', '--git-dir=' + remote, 'show', p['head']['ref'] + f':journey-{i}.txt'])
        assert got == (w / f'source-{i}.txt').read_bytes()
requests = json.load(open(w / f'provider-merge-{k}.json'))
puts = [r for r in requests if r['method']=='PUT' and r['path'].endswith('/merge-async')]
expected = 1 if case.startswith('native-') else k
assert len(puts) == expected, puts
assert all(r['body']['bypass_rules'] is False and r['body']['merge_action']=='default' for r in puts), puts
leaf=next(p for p in pulls if p['head']['ref'].endswith('/change/'+(w/'change-4.id').read_text().strip()))
lead=subprocess.check_output(['git','-C',str(w/'repo'),'rev-parse','refs/heads/loom/ws/'+(w/'workspace.id').read_text().strip()+'/interactive/lead']).decode().strip()
assert lead==leaf['head']['sha'],(lead,leaf)
subprocess.check_call(['git','--git-dir='+remote,'merge-base','--is-ancestor','main',leaf['head']['ref']])
PY
  ;;
mode-before)
  receipt mode-before
  ;;
mode-after)
  get git/settings "$work/settings-after.json"
  json "$work/settings-after.json" 'assert v["delivery_mode"]=="trunk",v'
  receipt mode-after
  python3 - "$work" <<'PY'
import json, pathlib, sys
w=pathlib.Path(sys.argv[1])
key=lambda p:(p['number'],p['head']['ref'],p['head']['sha'],p['base']['ref'],p['state'],p['merged_at'])
before=json.load(open(w/'pulls-mode-before.json')); after=json.load(open(w/'pulls-mode-after.json'))
assert sorted(map(key,before))==sorted(map(key,after)),(before,after)
PY
  ;;
future-pr)
  receipt future
  pull future
  json "$work/pull-future.json" 'assert v["base"]["ref"]=="main" and v["state"]=="open",v'
  python3 - "$work" "$remote" <<'PY'
import json, pathlib, subprocess, sys
w=pathlib.Path(sys.argv[1]); before=json.load(open(w/'pulls-mode-before.json')); after=json.load(open(w/'pulls-future.json'))
key=lambda p:(p['head'],p['base']['ref'],p['state'],p['merged_at'])
assert len(after)==len(before)+1
for p in before: assert key(p)==key(next(q for q in after if q['number']==p['number']))
p=json.load(open(w/'pull-future.json'))
assert subprocess.check_output(['git','--git-dir='+sys.argv[2],'diff','--name-only','main',p['head']['ref']]).decode().splitlines()==['journey-future.txt']
PY
  ;;
feedback-send)
  pull "$slot"
  delivery="journey-$case_name-$slot-$RUN_ID"
  printf '%s\n' "$delivery" > "$work/delivery-$slot.id"
  receipt "before-feedback-$slot"
  python3 - "$forge_repo" "$work/pull-$slot.json" "$slot" <<'PY' > "$work/webhook-$slot.json"
import json,sys
p=json.load(open(sys.argv[2]))
print(json.dumps({'action':'submitted','repository':{'full_name':sys.argv[1]},'sender':{'login':'reviewer'},'pull_request':{'number':p['number'],'head':{'sha':p['head']['sha']}},'review':{'state':'changes_requested','author_association':'MEMBER','body':'Please address this review. STUB_CODEX_PATCH=review-'+sys.argv[3]+'.txt'}}))
PY
  signature="$(openssl dgst -sha256 -hmac aft-journey-signing-secret -r < "$work/webhook-$slot.json" | cut -d' ' -f1)"
  code="$(curl -sS -o "$work/webhook-response-$slot.json" -w '%{http_code}' -X POST "$api/webhooks/github" -H 'Content-Type: application/json' -H 'X-GitHub-Event: pull_request_review' -H "X-GitHub-Delivery: $delivery" -H "X-Hub-Signature-256: sha256=$signature" --data-binary @"$work/webhook-$slot.json")"
  [[ "$code" == 202 ]]
  get "changes/$(cat "$work/change-$slot.id")/feedback" "$work/feedback-intake-$slot.json"
  json "$work/feedback-intake-$slot.json" 'f=[f for f in v["feedback"] if f["delivery_id"]==sys.argv[2]]; assert len(f)==1 and f[0]["status"]=="pending",v' "$delivery"
  ;;
feedback-agent)
  # Named deterministic agent: use only the repo stub, never operator codex.
  # Complete freezes/imports this agent commit via the product feedback API/CLI.
  # There is no feedback runner-exit CaptureTaskCopy seam here: retain that gap.
  delivery="$(cat "$work/delivery-$slot.id")"
  change="$(cat "$work/change-$slot.id")"
  printf '{"attempt":"journey-%s-%s"}' "$slot" "$RUN_ID" | post "changes/$change/feedback/$delivery/address" "$work/address-$slot.json"
  target="$(json "$work/address-$slot.json" 'print(v["target"])')"
  test "$(json "$work/address-$slot.json" 'print(v["base_sha"])')" = "$(json "$work/pull-$slot.json" 'print(v["head"]["sha"])')"
  json "$work/address-$slot.json" 'print(v["prompt"])' > "$work/feedback-prompt-$slot.txt"
  stub="$AFT_TESTS_DIR/../../e2e/stubs/codex"
  [[ -f "$stub" ]]
  (cd "$target"; STUB_CODEX_EPIC_RUNNER=0 LOOM_TASK_ID="$(task_id "$slot")" LOOM_TASK_RUN_ID="$delivery" bash "$stub" exec --json - < "$work/feedback-prompt-$slot.txt" > "$work/stub-feedback-$slot.json")
  grep -qF "task=$(task_id "$slot") run=$delivery" "$target/review-$slot.txt"
  git -C "$target" add -- "review-$slot.txt"
  git -C "$target" -c user.name=AFT-Agent -c user.email=aft-agent@example.test commit -q -m "Address verified review $delivery"
  loom feedback complete "$delivery" --capture-sha "$(git -C "$target" rev-parse HEAD)" > "$work/feedback-complete-$slot.txt"
  sed -nE 's/^Feedback addressed by revision ([0-9]+) of .*/\1/p' "$work/feedback-complete-$slot.txt" > "$work/feedback-number-$slot"
  test -s "$work/feedback-number-$slot"
  ;;
feedback-pushed)
  ready=false
  for _ in $(seq 1 45); do
    newest "$slot" > "$work/newest-feedback-$slot.txt"
    if json "$work/revisions-$slot.json" 'r=max(v["data"],key=lambda r:r["number"]); f=[i for i in v["data"] if i["number"]==int(sys.argv[2])]; assert r.get("feedback_status")=="pushed" and len(f)==1 and f[0].get("verdict")=="feedback" and not f[0]["incomplete"],v' "$(cat "$work/feedback-number-$slot")" 2>/dev/null; then ready=true; break; fi
    sleep 2
  done
  [[ "$ready" == true ]]
  receipt "feedback-$slot"
  python3 - "$work" "$remote" "$slot" <<'PY'
import json, pathlib, subprocess, sys
w,remote,slot=pathlib.Path(sys.argv[1]),sys.argv[2],sys.argv[3]
before=json.load(open(w/f'pulls-before-feedback-{slot}.json')); after=json.load(open(w/f'pulls-feedback-{slot}.json'))
assert {p['number'] for p in before}=={p['number'] for p in after},(before,after)
change=(w/f'change-{slot}.id').read_text().strip()
target=next(p for p in after if p['head']['ref'].endswith('/change/'+change))
old=next(p for p in before if p['number']==target['number'])
assert target['head']['sha']!=old['head']['sha'] and target['state']=='open', target
assert target['base']['ref']==old['base']['ref'],target
got=subprocess.check_output(['git','--git-dir='+remote,'show',target['head']['ref']+f':review-{slot}.txt']).decode()
assert 'run='+(w/f'delivery-{slot}.id').read_text().strip() in got,got
for p in after:
    assert p['state']=='open' and not p['merged_at'],p
    candidates=[i for i in ('1','2') if (w/f'change-{i}.id').exists() and p['head']['ref'].endswith('/change/'+(w/f'change-{i}.id').read_text().strip())]
    assert len(candidates)==1,(p,candidates)
    i=candidates[0]
    assert subprocess.check_output(['git','--git-dir='+remote,'show',p['head']['ref']+f':journey-{i}.txt'])==(w/f'source-{i}.txt').read_bytes()
if slot=='1' and len(after)==2:
    above=next(p for p in after if p['number']!=target['number'])
    assert above['base']['ref']==target['head']['ref']
    assert subprocess.check_output(['git','--git-dir='+remote,'show',above['head']['ref']+':review-1.txt']).decode()==got
requests=json.load(open(w/f'provider-feedback-{slot}.json'))
assert not any(r['method']=='PUT' and r['path'].endswith('/merge-async') for r in requests),requests
PY
  ;;
merge-waiting|merge-cancelled)
  want=waiting
  [[ "$phase" == merge-cancelled ]] && want=cancelled
  ready=false
  for _ in $(seq 1 45); do
    get "changes/$(cat "$work/change-$slot.id")/merge-approval" "$work/approval-$slot.json"
    if json "$work/approval-$slot.json" 'd=v.get("data",v); assert d["status"]==sys.argv[2],d' "$want" 2>/dev/null; then ready=true; break; fi
    sleep 2
  done
  [[ "$ready" == true ]]
  if [[ "$phase" == merge-cancelled ]]; then
    json "$work/approval-$slot.json" 'd=v.get("data",v); assert "review fix-ups" in d["reason"],d'
    json "$work/revisions-$slot.json" 'r=max(v["data"],key=lambda r:r["number"]); assert r["feedback_merge_cancelled"] is True,r'
  fi
  ;;
*) echo "Unknown phase: $phase" >&2; exit 2 ;;
esac

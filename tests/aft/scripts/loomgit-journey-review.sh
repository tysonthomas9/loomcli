#!/usr/bin/env bash
# Narrow mechanics/readbacks for loomgit-journey-review. Human verdicts and
# Create PR stay in YAML UI steps. Only fixture setup and named API-client
# task starts/design edits mutate through this script.
set -euo pipefail

# AFT caps every run step/hook at 120 seconds. Keep the entire process tree
# below that cap, including helper Git/CLI calls, and emit local diagnostics.
if [[ "${1:-}" != __guarded ]]; then
    exec python3 - "$0" "$@" <<'PY'
import os,pathlib,signal,subprocess,sys
script,*args=sys.argv[1:]
child=subprocess.Popen(['bash',script,'__guarded',*args],start_new_session=True)
try:
    code=child.wait(timeout=105)
except subprocess.TimeoutExpired:
    print('Review phase exceeded 105 seconds: '+repr(args),file=sys.stderr)
    try: os.killpg(child.pid,signal.SIGTERM)
    except ProcessLookupError: pass
    try: child.wait(timeout=2)
    except subprocess.TimeoutExpired:
        try: os.killpg(child.pid,signal.SIGKILL)
        except ProcessLookupError: pass
        try: child.wait(timeout=2)
        except subprocess.TimeoutExpired: pass
    code=124
if code:
    key=args[1] if len(args)>1 else ''
    state=pathlib.Path(os.environ.get('AFT_WORK_DIR','.'),'journey-'+key)
    print(f'Review phase failed ({code}): {args}; retain {state} and server logs',file=sys.stderr)
    for name in ('workspace.json','issue.json','revisions.json','workflow-readback.json'):
        path=state/name
        if path.is_file():
            with path.open(errors='replace') as stream:
                print(name+': '+stream.read(2048),file=sys.stderr)
    if state.is_dir(): (state/'phase-failure.txt').write_text(f'{args}: exit {code}\n')
sys.exit(code if code>=0 else 1)
PY
fi
shift

# Bound requests made here AND by sourced fixture helpers. Last options win.
curl() { command curl "$@" --connect-timeout 2 --max-time 5; }
source "${AFT_TESTS_DIR:?}/scripts/loomgit-journey-common.sh"

phase="${1:?phase required}"

lead_path() {
    git -C "$JOURNEY_REPO" worktree list --porcelain | python3 -c '
import os,sys
blocks=sys.stdin.read().strip().split("\n\n")
wanted="branch refs/heads/loom/ws/"+os.environ["JOURNEY_WS"]+"/interactive/lead"
paths=[b.splitlines()[0].removeprefix("worktree ") for b in blocks if wanted in b.splitlines()]
assert len(paths)==1, (wanted,blocks)
print(paths[0])'
}

if [[ "$phase" == prepare ]]; then
    # Provisioning is a separate bounded step in EACH case, not ten serial
    # fixtures inside the single 120-second suite setup hook.
    test -r "$AFT_TESTS_DIR/scripts/loomgit-journey-common.sh"
    exit 0
fi

if [[ "$phase" == setup ]]; then
    key="${2:?fixture key required}"
    case "$key" in review-stack-*) mode=stack ;; review-trunk-*) mode=trunk ;; *) exit 2 ;; esac
    flow="${key#review-$mode-}"
    case "$flow" in explicit|automatic|reject-retry|empty|held) ;; *) exit 2 ;; esac
    journey_setup "$key" "$mode" loom
    file="$key.txt"
    [[ "$flow" == empty ]] && file=empty
    journey_create_task task "$file"
    lead_path > "$JOURNEY_STATE/lead.path"
    if [[ "$flow" == held ]]; then
        # Fixture setup only: an unsaved user file overlaps the task's
        # new file. Approval must preserve it, not overwrite it.
        area="$(lead_path)"
        printf 'unsaved user edit for %s\n' "$key" > "$area/$file"
        cp "$area/$file" "$JOURNEY_STATE/unsaved-before.txt"
        git -C "$area" status --porcelain > "$JOURNEY_STATE/status-before.txt"
    fi
    git -C "$JOURNEY_REMOTE" for-each-ref --format='%(refname) %(objectname)' refs/heads > "$JOURNEY_STATE/remote-before.txt"
    git -C "$JOURNEY_REPO" rev-parse "refs/heads/loom/ws/$JOURNEY_WS/interactive/lead" > "$JOURNEY_STATE/lead-before.sha"
    touch "$JOURNEY_STATE/setup.complete"
    exit 0
fi

if [[ "$phase" == teardown ]]; then
    # Retain unresolved code review, refs and provider outcomes for inspection.
    # The outer harness owns server/process teardown. Never force-close a task
    # or bypass guarded workspace deletion just to make suite cleanup green.
    cleanup_failed=0
    for mode in stack trunk; do
        for flow in explicit automatic reject-retry empty held; do
            state="${AFT_WORK_DIR:?}/journey-review-$mode-$flow"
            # --filter may provision only one case. Absent fixture directories
            # are not failures; created but incomplete fixtures remain failures.
            [[ -d "$state" ]] || continue
            printf 'Fixture retained for review; outer harness owns process cleanup.\n' > "$state/cleanup.txt"
            incomplete=false
            [[ -f "$state/setup.complete" ]] || incomplete=true
            for receipt in workspace.id forge-repo task-task.id file-task lead.path lead-before.sha remote-before.txt; do
                [[ -s "$state/$receipt" ]] || incomplete=true
            done
            if "$incomplete"; then
                printf 'Fixture setup incomplete; all existing evidence retained.\n' > "$state/cleanup.failed"
                echo "Incomplete review fixture retained: $state" >&2
                cleanup_failed=1
            fi
        done
    done
    exit "$cleanup_failed"
fi

key="${2:?fixture key required}"
journey_load "$key"
task="$(cat "$JOURNEY_STATE/task-task.id")"
file="$(cat "$JOURNEY_STATE/file-task")"
lead_ref="refs/heads/loom/ws/$JOURNEY_WS/interactive/lead"
# All successive waits share one wall-clock budget, rather than accumulating
# three independent 90/180-second polls. Reserve time for the five readbacks.
poll_deadline=$((SECONDS + 70))

readback() {
    curl -fsS "$JOURNEY_API/issues/$task" > "$JOURNEY_STATE/issue.json"
    curl -fsS "$JOURNEY_API/issues/$task/revisions?lead=lead" > "$JOURNEY_STATE/revisions.json"
    curl -fsS "$JOURNEY_API/issues/$task/diff?lead=lead" > "$JOURNEY_STATE/task-diff.json"
    curl -fsS "$AFT_FAKE_GH_BASE/__pulls?repo=$JOURNEY_FORGE_REPO" > "$JOURNEY_STATE/pulls.json"
    curl -fsS "$AFT_FAKE_GH_BASE/__requests" > "$JOURNEY_STATE/forge-requests.json"
}

wait_state() {
    local wanted="$1"
    while (( SECONDS < poll_deadline )); do
        curl -fsS "$JOURNEY_API/issues/$task" > "$JOURNEY_STATE/issue.json"
        if python3 - "$JOURNEY_STATE/issue.json" "$wanted" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))['data']
sys.exit(0 if d['status']==sys.argv[2] else 1)
PY
        then return 0; fi
        sleep 1
    done
    echo "Task $task did not reach $wanted; retain $JOURNEY_STATE and server logs" >&2
    cat "$JOURNEY_STATE/issue.json" >&2
    return 1
}

wait_workflow() {
    # Ensure the previous epic-runner cannot race the human Reject/retry.
    # completed and needs_review are normal outcomes for a captured task;
    # failed/cancelled are failures, never a successful fixture shortcut.
    local run
    run="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["run_id"])' "$JOURNEY_STATE/workflow-task.json")"
    while (( SECONDS < poll_deadline )); do
        curl -fsS "$JOURNEY_API/runs/$run" > "$JOURNEY_STATE/workflow-readback.json"
        if python3 - "$JOURNEY_STATE/workflow-readback.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1])); status=d['status']
if status in ('failed','cancelled'): raise RuntimeError(d)
sys.exit(0 if status in ('completed','needs_review') else 1)
PY
        then return 0; fi
        sleep 1
    done
    echo "Workflow $run did not finish; retain $JOURNEY_STATE/workflow-readback.json" >&2
    return 1
}

case "$phase" in
start)
    journey_start_task task
    ;;
finished)
    # Own wait: the public TaskRevision has head_sha, not base_sha. Resolve
    # the recorded base ref through Git; never invent an API field.
    captured=false
    while (( SECONDS < poll_deadline )); do
        curl -fsS "$JOURNEY_API/issues/$task/revisions" > "$JOURNEY_STATE/revisions.json"
        if python3 - "$JOURNEY_STATE/revisions.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))['data']
sys.exit(0 if len(d)==1 and d[0].get('head_sha') else 1)
PY
        then captured=true; break; fi
        sleep 2
    done
    "$captured" || { echo "Revision capture timed out for $task" >&2; exit 1; }
    if [[ "$file" == empty ]]; then wait_state closed; else wait_state review; fi
    wait_workflow
    readback
    cp "$JOURNEY_STATE/revisions.json" "$JOURNEY_STATE/source-first.json"
    ;;
open)
    journey_open_task task
    ;;
ui-pin)
    readback
    expression="$(python3 - "$JOURNEY_STATE/revisions.json" <<'PY'
import json,sys
r=max(json.load(open(sys.argv[1]))['data'],key=lambda x:x['number'])
print('(() => { const section=document.querySelector("[data-testid=revisions-section]"); const label=document.querySelector("[data-testid=task-diff-label]"); if (!section?.textContent.includes('+json.dumps(r['head_sha'][:12])+') || !label?.textContent.startsWith('+json.dumps('Revision '+str(r['number'])+' ')+')) throw new Error("UI revision/head differs from API"); return true; })()')
PY
)"
    agent-browser --session "$AFT_SESSION" eval "$expression"
    ;;
retry-publish)
    # A named API-client retries delivery AFTER the human UI published it.
    # This exercises idempotency, not a bypass for the initial human action.
    change="$(python3 -c 'import json,sys; print(max(json.load(open(sys.argv[1]))["data"],key=lambda x:x["number"])["change_id"])' "$JOURNEY_STATE/revisions.json")"
    code="$(curl -sS -o "$JOURNEY_STATE/retry-publish.json" -w '%{http_code}' -X POST "$JOURNEY_API/agents/lead/git/pr" \
        -H 'Content-Type: application/json' -d "{\"change_id\":\"$change\"}")"
    # The production publisher adopts the existing PR and returns success.
    # Any unexpected refusal is a failure, never silently treated as PASS.
    test "$code" = 200 || { cat "$JOURNEY_STATE/retry-publish.json" >&2; exit 1; }
    ;;
retry)
    # API-client actor asks the same rejected task to produce a new attempt.
    # It edits design only: no status/label/closure/revision bypass.
    retry_file="$key-retry.txt"
    python3 - "$retry_file" <<'PY' |
import json,sys
print(json.dumps(dict(design='STUB_CODEX_PATCH='+sys.argv[1])))
PY
        curl -fsS -X PATCH "$JOURNEY_API/issues/$task" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/retry-design.json"
    printf '%s\n' "$retry_file" > "$JOURNEY_STATE/file-task"
    journey_start_task task
    ;;
retried)
    captured=false
    while (( SECONDS < poll_deadline )); do
        curl -fsS "$JOURNEY_API/issues/$task/revisions" > "$JOURNEY_STATE/revisions.json"
        if python3 - "$JOURNEY_STATE/revisions.json" "$JOURNEY_STATE/source-first.json" <<'PY'
import json,sys
d=json.load(open(sys.argv[1]))['data']; old=json.load(open(sys.argv[2]))['data'][0]
sys.exit(0 if any(r['number']>old['number'] and r['head_sha']!=old['head_sha'] for r in d) else 1)
PY
        then captured=true; break; fi
        sleep 2
    done
    "$captured" || { echo "Retry revision capture timed out for $task" >&2; exit 1; }
    wait_state review
    wait_workflow
    readback
    cp "$JOURNEY_STATE/revisions.json" "$JOURNEY_STATE/source-retry.json"
    ;;
check)
    check="${3:?readback check required}"
    case "$check" in
        applied|published|stable) wait_state closed ;;
        rejected) wait_state open ;;
        empty) wait_state closed ;;
    esac
    readback
    python3 - "$check" "$file" <<'PY'
import json,os,pathlib,sqlite3,subprocess,sys
check,file=sys.argv[1:]; state=pathlib.Path(os.environ['JOURNEY_STATE'])
repo=os.environ['JOURNEY_REPO']; remote=os.environ['JOURNEY_REMOTE']; ws=os.environ['JOURNEY_WS']
def load(name): return json.loads((state/name).read_text())
def git(*args,at=repo): return subprocess.check_output(['git','-C',at,*args],text=True).strip()
def branch_refs(at): return git('for-each-ref','--format=%(refname) %(objectname)','refs/heads',at=at)+'\n'
issue=load('issue.json')['data']; revisions=load('revisions.json')['data']
r=max(revisions,key=lambda x:x['number']); pulls=load('pulls.json')
first=load('source-first.json')['data'][0]
source=max(load('source-retry.json')['data'],key=lambda x:x['number']) if (state/'source-retry.json').exists() else first
lead='refs/heads/loom/ws/'+ws+'/interactive/lead'
for old in [first,source]:
    head=f"refs/loom/ws/{ws}/change/{old['change_id']}/{old['number']}/head"
    assert git('rev-parse',head)==old['head_sha'], ('source head changed',old)
base_ref=f"refs/loom/ws/{ws}/change/{source['change_id']}/{source['number']}/base"
base=git('rev-parse',base_ref)
diff=load('task-diff.json')['data']; assert len(diff)==1, diff
assert diff[0]['change']==r['change_id'] and diff[0]['revision']==r['number'], (diff,r)
paths=[f['path'] for f in diff[0]['files']]
patch=''.join(f.get('patch','') for f in diff[0]['files'])
def exact_patch(from_sha,to_sha):
    expected=subprocess.check_output(['git','-C',repo,'diff','--no-ext-diff','--no-textconv','--no-renames','--no-color','--patch',from_sha,to_sha],text=True)
    assert patch==expected, ('API/Git patch mismatch',patch,expected)
def unpublished():
    assert not pulls, pulls
    # P1.19 permits private capture-backup refs before review. Publication
    # cannot move a branch or open a PR; this is not a zero-network claim.
    assert branch_refs(remote)==(state/'remote-before.txt').read_text(), 'remote branch moved before publish'
def source_verdict(kind):
    s=next(x for x in revisions if x['number']==source['number'] and x['change_id']==source['change_id'])
    assert s['head_sha']==source['head_sha'] and s.get('verdict')==kind, s
    # Read product-created state only. A normal connection observes WAL;
    # query_only forbids writes and the existence check forbids creation.
    journal=pathlib.Path(os.environ['AFT_LOOM_CONFIG_DIR'],'loomgit','store.db')
    assert journal.is_file(), journal
    with sqlite3.connect(journal,timeout=10) as db:
        db.execute('PRAGMA query_only=ON')
        verdict=db.execute('SELECT head_sha,kind,actor_kind,actor_id FROM review_verdicts WHERE workspace=? AND change_id=? AND number=? ORDER BY id DESC LIMIT 1', (ws,source['change_id'],source['number'])).fetchone()
    assert verdict==(source['head_sha'],kind,'human','local-user'), verdict
if check in ('review','retry'):
    assert issue['status']=='review' and 'code-review' in issue.get('labels',[]), issue
    assert not r.get('verdict') and not r['incomplete'] and not r['no_changes'] and not r['applied'], r
    assert paths==[file], paths
    assert git('rev-parse',lead)==(state/'lead-before.sha').read_text().strip(), 'unapproved Apply'
    exact_patch(base,source['head_sha']); unpublished()
    # Validate the bytes the stub agent wrote were captured by the product.
    content=git('show',source['head_sha']+':'+file)
    assert content.startswith('task='+issue['id']+' run=') and content.split(' run=',1)[1], content
    if check=='retry':
        old=next(x for x in revisions if x['number']==first['number'])
        assert old.get('verdict')=='reject' and old['superseded'], old
        assert source['change_id']==first['change_id'] and source['number']>first['number'], (source,first)
elif check=='rejected':
    source_verdict('reject')
    assert issue['status']=='open' and 'code-review' not in issue.get('labels',[]), issue
    assert not r['applied']; unpublished()
    assert git('rev-parse',lead)==(state/'lead-before.sha').read_text().strip()
elif check=='empty':
    assert len(revisions)==1 and r['no_changes'] and not r['incomplete'] and not r.get('verdict') and not r['applied'], r
    assert issue['status']=='closed' and issue['close_reason']=='No changes', issue
    assert not paths and git('rev-parse',source['head_sha']+'^{tree}')==git('rev-parse',base+'^{tree}')
    unpublished(); assert git('rev-parse',lead)==(state/'lead-before.sha').read_text().strip()
elif check=='held':
    source_verdict('approve')
    assert not r['applied'] and issue['status']=='review' and 'code-review' in issue.get('labels',[]), (r,issue)
    assert git('rev-parse',lead)==(state/'lead-before.sha').read_text().strip()
    area=(state/'lead.path').read_text().strip()
    assert pathlib.Path(area,file).read_bytes()==(state/'unsaved-before.txt').read_bytes(), 'user bytes overwritten'
    assert git('status','--porcelain',at=area)+'\n'==(state/'status-before.txt').read_text(), 'index/worktree changed'
    unpublished()
else:
    assert check in ('applied','published','stable'), check
    source_verdict('approve')
    assert r['applied'] and r.get('verdict') in ('approve','carried'), r
    assert r['number']==source['number'] and r['head_sha']==source['head_sha'], ('fresh area must fast-forward to reviewed source',r,source)
    assert issue['status']=='closed' and 'code-review' not in issue.get('labels',[]), issue
    assert paths==[file] and diff[0]['compare']=='trunk', diff
    assert git('rev-parse',lead)==r['head_sha'], (lead,r)
    assert git('show',lead+':'+file)==git('show',source['head_sha']+':'+file), 'applied bytes differ'
    message=git('show','-s','--format=%B',r['head_sha'])
    for trailer in ('Loom-Change-Id: '+source['change_id'],'Loom-Task: '+issue['id'],'Loom-Revision: '+str(r['number'])):
        assert trailer in message.splitlines(), ('missing driver revision trailer',trailer,message)
    assert any(line.startswith('Loom-Attempt: ') and len(line)>len('Loom-Attempt: ') for line in message.splitlines()), message
    assert git('rev-parse','main')==(state/'base.sha').read_text().strip(), 'workspace trunk moved'
    assert git('rev-list','--count',base+'..'+r['head_sha'])=='1', 'driver layer must be one commit'
    exact_patch(base,r['head_sha'])
    if check=='applied': unpublished()
    else:
        assert len(pulls)==1, pulls
        p=pulls[0]; assert p['repo']==os.environ['JOURNEY_FORGE_REPO'] and p['state']=='open' and p['base']['ref']=='main', p
        assert f"Loom-Change-Id: {source['change_id']}" in p['body'], p
        remote_head=git('rev-parse','refs/heads/'+p['head']['ref'],at=remote)
        assert remote_head==r['head_sha']==p['head']['sha']==r['pr_head'], (p,r,remote_head)
        assert p['number']==r['pr_number'], (p,r)
        assert git('rev-parse',f"refs/loom/ws/{ws}/pub/{source['change_id']}")==remote_head
        remote_paths=git('diff','--name-only','refs/heads/main','refs/heads/'+p['head']['ref'],at=remote).splitlines()
        assert remote_paths==[file], remote_paths
        requests=load('forge-requests.json')
        creates=[q for q in requests if q['method']=='POST' and q['path']=='/repos/'+os.environ['JOURNEY_FORGE_REPO']+'/pulls']
        assert len(creates)==1, ('duplicate PR create',creates)
        snapshot=dict(number=p['number'],head=remote_head,body=p['body'],remote_branches=branch_refs(remote),revision=r['number'])
        if check=='published': (state/'published.json').write_text(json.dumps(snapshot))
        else: assert snapshot==load('published.json'), ('reload/retry changed publication',snapshot)
print(f"{check}: task={issue['id']} change={r['change_id']} revision={r['number']} head={r['head_sha']}")
(state/('evidence-'+check+'.json')).write_text(json.dumps(dict(issue=issue,revisions=revisions,diff=diff,pulls=pulls),indent=2))
PY
    ;;
*) echo "unknown review journey phase: $phase" >&2; exit 2 ;;
esac

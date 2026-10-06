#!/usr/bin/env bash
# Source this file from a journey's narrow phase script, or use its phase CLI.
# Fixtures create repositories/issues via public APIs; only product TaskRuns
# capture/freeze revisions. Human verdicts belong in mounted YAML UI controls.
set -euo pipefail

journey_key() {
    [[ "$1" =~ ^[a-zA-Z0-9_-]+$ ]] || { echo 'journey fixture key must contain only letters, numbers, _ or -' >&2; return 1; }
}

journey_json_id() {
    python3 -c 'import json,sys; d=json.load(sys.stdin)["data"]; assert d.get("id"),d; print(d["id"])'
}

journey_load() {
    journey_key "$1"
    export JOURNEY_STATE="${AFT_WORK_DIR:?}/journey-$1"
    export JOURNEY_REPO="$JOURNEY_STATE/repo" JOURNEY_REMOTE="$JOURNEY_STATE/origin.git"
    JOURNEY_WS="$(cat "$JOURNEY_STATE/workspace.id")"
    export JOURNEY_WS
    JOURNEY_FORGE_REPO="$(cat "$JOURNEY_STATE/forge-repo")"
    export JOURNEY_FORGE_REPO
    export JOURNEY_API="${AFT_BASE_URL:?}/api/workspaces/$JOURNEY_WS"
}

journey_setup() {
    local key="$1" mode="${2:-stack}" backend="${3:-loom}" token name
    journey_key "$key"
    case "$mode" in stack|trunk) ;; *) echo 'delivery mode must be stack or trunk' >&2; return 1 ;; esac
    case "$backend" in loom|github) ;; *) echo 'forge backend must be loom or github' >&2; return 1 ;; esac
    : "${AFT_FAKE_GH_BASE:?Use run-aft.sh --suite loomgit-* to start the forge}"
    export JOURNEY_STATE="${AFT_WORK_DIR:?}/journey-$key"
    # Refuse to reuse a case fixture, even after an interrupted setup.
    mkdir "$JOURNEY_STATE"
    token="$(python3 -c 'import uuid; print(uuid.uuid4().hex)')"
    # Loom workspace names are capped at 64 characters. Keep full UUID identity.
    name="e2e-journey-${key:0:18}-$token"
    export JOURNEY_REPO="$JOURNEY_STATE/repo" JOURNEY_REMOTE="$JOURNEY_STATE/origin.git"
    export JOURNEY_FORGE_REPO="owner/journey-$key-$token"
    printf '%s\n' "$JOURNEY_FORGE_REPO" > "$JOURNEY_STATE/forge-repo"
    git init -q --bare "$JOURNEY_REMOTE"
    git -C "$JOURNEY_REMOTE" symbolic-ref HEAD refs/heads/main
    git init -q -b main "$JOURNEY_REPO"
    printf 'journey=%s fixture=%s\n' "$key" "$token" > "$JOURNEY_REPO/README.md"
    git -C "$JOURNEY_REPO" add README.md
    git -C "$JOURNEY_REPO" -c user.name=AFT -c user.email=aft@example.test commit -q -m 'journey fixture base'
    # Only this throwaway fixture gets Git configuration, through Git's CLI.
    git -C "$JOURNEY_REPO" config core.sshCommand "sh '$AFT_TESTS_DIR/fixtures/fake-github/git-ssh-bridge.sh' '$JOURNEY_REMOTE'"
    git -C "$JOURNEY_REPO" remote add origin "git@github.com:$JOURNEY_FORGE_REPO.git"
    git -C "$JOURNEY_REPO" push -q origin main
    python3 - "$JOURNEY_FORGE_REPO" "$JOURNEY_REMOTE" "$backend" <<'PY' |
import json,sys
print(json.dumps(dict(repo=sys.argv[1],remote=sys.argv[2],native_stacks=sys.argv[3]=='github')))
PY
        curl -fsS -X POST "$AFT_FAKE_GH_BASE/__register" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/forge-register.json"
    python3 - "$name" "$JOURNEY_REPO" <<'PY' |
import json,sys
print(json.dumps(dict(name=sys.argv[1],type='empty',repos=[sys.argv[2]])))
PY
        curl -fsS -X POST "$AFT_BASE_URL/api/workspaces" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/workspace.json"
    journey_json_id < "$JOURNEY_STATE/workspace.json" > "$JOURNEY_STATE/workspace.id"
    journey_load "$key"
    # Public product configuration; no journal/session/ref records are seeded.
    LOOM_WORKSPACE="$JOURNEY_WS" LOOM_CONFIG_DIR="$AFT_LOOM_CONFIG_DIR" "$AFT_LOOM_BIN" delivery-mode "$mode" --workspace "$JOURNEY_WS" > "$JOURNEY_STATE/delivery-mode.txt"
    curl -fsS -X POST "$JOURNEY_API/agents" -H 'Content-Type: application/json' \
        -d '{"name":"lead","role_name":"lead","auto":false,"cross_repo":true,"repos":[],"backend":"codex"}' > "$JOURNEY_STATE/lead.json"
    git -C "$JOURNEY_REPO" rev-parse HEAD > "$JOURNEY_STATE/base.sha"
    printf '%s\n' "$mode" > "$JOURNEY_STATE/delivery-mode"
    printf '%s\n' "$backend" > "$JOURNEY_STATE/forge-backend"
    echo "fixture provisioned: workspace $JOURNEY_WS, forge $JOURNEY_FORGE_REPO"
}

journey_create_task() {
    local slot="$1" file="$2" predecessor="${3:-}" epic task
    journey_key "$slot"
    [[ ! -e "$JOURNEY_STATE/task-$slot.id" ]] || { echo "task slot $slot already exists" >&2; return 1; }
    if [[ -n "$predecessor" ]]; then journey_key "$predecessor"; test -s "$JOURNEY_STATE/task-$predecessor.id"; fi
    python3 - "$JOURNEY_WS" "$slot" <<'PY' |
import json,sys
print(json.dumps(dict(title=f'Journey {sys.argv[1]} {sys.argv[2]} epic',issue_type='epic',priority=2)))
PY
        curl -fsS -X POST "$JOURNEY_API/issues" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/epic-$slot.json"
    epic="$(journey_json_id < "$JOURNEY_STATE/epic-$slot.json")"
    printf '%s\n' "$epic" > "$JOURNEY_STATE/epic-$slot.id"
    python3 - "$JOURNEY_WS" "$slot" "$epic" "$file" <<'PY' |
import json,re,sys
ws,slot,epic,path=sys.argv[1:]
if path!='empty':
    assert re.fullmatch(r'[a-zA-Z0-9._/-]+',path) and not path.startswith('/') and '..' not in path.split('/'),path
print(json.dumps(dict(title=f'Journey {ws} {slot}',issue_type='task',priority=2,parent=epic,design='Check README; no changes needed.' if path=='empty' else f'STUB_CODEX_PATCH={path}')))
PY
        curl -fsS -X POST "$JOURNEY_API/issues" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/task-$slot.json"
    task="$(journey_json_id < "$JOURNEY_STATE/task-$slot.json")"
    printf '%s\n' "$task" > "$JOURNEY_STATE/task-$slot.id"
    if [[ -n "$predecessor" ]]; then
        python3 - "$(cat "$JOURNEY_STATE/task-$predecessor.id")" <<'PY' |
import json,sys
print(json.dumps(dict(depends_on_id=sys.argv[1],dep_type='blocks')))
PY
            curl -fsS -X POST "$JOURNEY_API/issues/$task/dependencies" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/dependency-$slot.json"
    fi
    printf '%s\n' "$file" > "$JOURNEY_STATE/file-$slot"
    echo "fixture task $slot: $task"
}

journey_start_task() {
    local slot="$1"
    journey_key "$slot"
    # The YAML intent must name the API-client actor for this mutation.
    # Each task has its own epic, so a predecessor can be reviewed through UI
    # before the next run starts, without editing/closing dependencies as a bypass.
    python3 - "$(cat "$JOURNEY_STATE/epic-$slot.id")" <<'PY' |
import json,sys
print(json.dumps(dict(epicId=sys.argv[1],runner='local-task-runner')))
PY
        curl -fsS -X POST "$JOURNEY_API/workflows/epic-runner" -H 'Content-Type: application/json' -d @- > "$JOURNEY_STATE/workflow-$slot.json"
}

journey_wait_revision() {
    local slot="$1" task count deadline=$((SECONDS + 90))
    journey_key "$slot"
    task="$(cat "$JOURNEY_STATE/task-$slot.id")"
    while (( SECONDS < deadline )); do
        curl -fsS --max-time 2 "$JOURNEY_API/issues/$task/revisions" > "$JOURNEY_STATE/revisions-$slot.json"
        count="$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1]))["data"]; assert isinstance(d,list),d; print(len(d))' "$JOURNEY_STATE/revisions-$slot.json")"
        if [[ "$count" -gt 0 ]]; then
            python3 - "$JOURNEY_STATE/revisions-$slot.json" <<'PY'
import json,sys
r=max(json.load(open(sys.argv[1]))['data'],key=lambda r:r['number'])
assert r.get('change_id') and r.get('repo') and r.get('head_sha'),r
print('product revision:',r['change_id'],r['number'],r['head_sha'])
PY
            return 0
        fi
        sleep 2
    done
    echo "task $task produced no revision; retain server log and fixture $JOURNEY_STATE" >&2
    return 1
}

journey_readback() {
    local slot="$1" task change number sha repo_name revision_fields
    journey_key "$slot" || return 1
    task="$(cat "$JOURNEY_STATE/task-$slot.id")" || return 1
    curl -fsS --max-time 10 "$JOURNEY_API/issues/$task" > "$JOURNEY_STATE/issue-$slot-readback.json" || return 1
    curl -fsS --max-time 10 "$JOURNEY_API/issues/$task/revisions" > "$JOURNEY_STATE/revisions-$slot-readback.json" || return 1
    revision_fields="$(python3 -c 'import json,sys; r=max(json.load(open(sys.argv[1]))["data"],key=lambda r:r["number"]); print(r["change_id"],r["number"],r["head_sha"],r["repo"])' "$JOURNEY_STATE/revisions-$slot-readback.json")" || return 1
    read -r change number sha repo_name <<< "$revision_fields"
    curl -fsS --max-time 10 --get "$JOURNEY_API/changes/$change/revisions/$number/diff" --data-urlencode "repo=$repo_name" > "$JOURNEY_STATE/diff-$slot-readback.json" || return 1
    curl -fsS --max-time 10 "$AFT_FAKE_GH_BASE/__pulls?repo=$JOURNEY_FORGE_REPO" > "$JOURNEY_STATE/pulls-$slot-readback.json" || return 1
    git -C "$JOURNEY_REPO" for-each-ref --format='%(refname) %(objectname)' > "$JOURNEY_STATE/refs-$slot-readback.txt" || return 1
    git -C "$JOURNEY_REMOTE" for-each-ref --format='%(refname) %(objectname)' > "$JOURNEY_STATE/remote-refs-$slot-readback.txt" || return 1
    echo "readback retained for task $task at revision $change/$number ($sha); case-specific assertions are required"
}

journey_open_task() {
    local slot="$1"
    journey_key "$slot"
    # Navigation only. All human mutations remain visible steps in the suite.
    agent-browser --session "${AFT_SESSION:?}" open "$AFT_BASE_URL/ws/$JOURNEY_WS/kanban" >/dev/null
    agent-browser --session "$AFT_SESSION" wait '[data-testid="board-toolbar"]' >/dev/null
    agent-browser --session "$AFT_SESSION" open "$AFT_BASE_URL/ws/$JOURNEY_WS/issues/$(cat "$JOURNEY_STATE/task-$slot.id")" >/dev/null
}

journey_teardown() {
    # Public guarded deletion only; never close code-review tasks to bypass it.
    # On denial, report failure and keep all local evidence. The outer harness
    # still owns/stops its server and forge. No other fixture or ref is removed.
    local task_file slot area index=0
    # Write a pessimistic marker before any cleanup work. AFT treats teardown
    # errors as report-only, so run-aft.sh also checks this marker after AFT exits.
    printf 'cleanup/readback unfinished; evidence retained\n' > "$JOURNEY_STATE/cleanup.failed"
    for task_file in "$JOURNEY_STATE"/task-*.id; do
        [[ -f "$task_file" ]] || continue
        slot="${task_file##*/task-}"; slot="${slot%.id}"
        journey_readback "$slot" || return 1
    done
    git -C "$JOURNEY_REPO" worktree list --porcelain > "$JOURNEY_STATE/worktrees-before-cleanup.txt" || return 1
    while IFS= read -r area; do
        [[ "$area" == 'worktree '* ]] || continue
        area="${area#worktree }"
        index=$((index + 1))
        git -C "$area" status --porcelain > "$JOURNEY_STATE/tree-$index-status.txt" || return 1
        git -C "$area" diff --binary HEAD > "$JOURNEY_STATE/tree-$index-dirty.patch" || return 1
        git -C "$area" archive HEAD > "$JOURNEY_STATE/tree-$index-head.tar" || return 1
        python3 - "$area" "$JOURNEY_STATE/tree-$index-files.tar" <<'PY' || return 1
import os,pathlib,subprocess,sys,tarfile
root,out=sys.argv[1:]
paths=subprocess.check_output(['git','-C',root,'ls-files','-z','--cached','--others','--exclude-standard']).split(b'\0')
with tarfile.open(out,'w') as archive:
    for raw in sorted(set(filter(None,paths))):
        name=os.fsdecode(raw)
        assert not pathlib.PurePosixPath(name).is_absolute() and '..' not in pathlib.PurePosixPath(name).parts,name
        path=os.path.join(root,name)
        if os.path.lexists(path): archive.add(path,arcname=name,recursive=False)
PY
    done < "$JOURNEY_STATE/worktrees-before-cleanup.txt"
    # Fixture API-client cleanup uses the product's exact-list confirmation.
    # Archive actual product working areas/copies, not just the source fixture.
    curl -fsS --max-time 10 "$JOURNEY_API/delete/preview" > "$JOURNEY_STATE/delete-preview-before.json" || return 1
    python3 - "$JOURNEY_STATE" "$AFT_WORK_DIR" "$AFT_LOOM_CONFIG_DIR" <<'PY' || return 1
import hashlib,json,os,pathlib,subprocess,sys,tarfile
state,run,config=map(pathlib.Path,sys.argv[1:])
ws=json.load(open(state/'workspace.json'))['data']
product=pathlib.Path(ws['path']).resolve()
allowed=[state.resolve(),(config/'workspaces'/ws['name']).resolve()]
assert product.is_relative_to(allowed[0]) or product==allowed[1],(product,allowed)
assert state.resolve().is_relative_to(run.resolve()),state
preview=json.load(open(state/'delete-preview-before.json'))
for item in preview['items']:
    path=pathlib.Path(item['path']).resolve()
    assert any(path.is_relative_to(root) for root in allowed),(path,allowed)
manifest={'workspace':ws['id'],'product_root':str(product),'preview':preview,'trees':[],'files':[]}
roots=[(product,'product-workspace'),(state/'repo','source-fixture')]
# Archive raw index/stat and all bytes before diagnostic Git status can refresh it.
with tarfile.open(state/'product-before-delete.tar.gz','w:gz') as archive:
    for root,prefix in roots:
        assert root.is_dir(),root
        archive.add(root,arcname=prefix)
with tarfile.open(state/'product-before-delete.tar.gz','r:gz') as archive:
    for member in archive:
        parts=pathlib.PurePosixPath(member.name).parts
        root=next(root for root,prefix in roots if prefix==parts[0])
        entry={'path':str(root.joinpath(*parts[1:])),'archive_member':member.name,'size':member.size,'mtime':member.mtime,'mode':member.mode}
        if member.isfile():
            h=hashlib.sha256();stream=archive.extractfile(member)
            while data:=stream.read(1024*1024):h.update(data)
            entry['sha256']=h.hexdigest()
        elif member.issym():entry['symlink']=member.linkname
        else:continue
        manifest['files'].append(entry)
for root,_ in roots:
    # Do not mistake an enclosing fixture repository for a managed working copy.
    gitroots=[p.parent for p in root.rglob('.git')]
    for tree in sorted(set(gitroots)):
        read=lambda *args:subprocess.check_output(['git','--no-optional-locks','-C',str(tree),*args],text=True)
        manifest['trees'].append({'path':str(tree),'managed':tree.is_relative_to(product),'head':read('rev-parse','HEAD').strip(),'refs':read('for-each-ref','--format=%(refname) %(objectname)'),'index':read('ls-files','--stage'),'status':read('status','--porcelain=v1','--untracked-files=all'),'dirty_patch':read('diff','--binary','HEAD')})
assert any(tree['managed'] for tree in manifest['trees']),manifest['trees']
(state/'product-before-delete-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
(state/'product-root-before-delete.txt').write_text(str(product)+'\n')
PY
    curl -fsS --max-time 10 "$JOURNEY_API/delete/preview" > "$JOURNEY_STATE/delete-preview-confirmed.json" || return 1
    local fingerprint
    fingerprint="$(python3 - "$JOURNEY_STATE" <<'PY'
import json,pathlib,sys
state=pathlib.Path(sys.argv[1]);before=json.load(open(state/'delete-preview-before.json'));after=json.load(open(state/'delete-preview-confirmed.json'))
assert before['fingerprint']==after['fingerprint'] and before['items']==after['items'],'Deletion preview changed during archive; retain all evidence'
assert len(after['fingerprint'])==64,after
print(after['fingerprint'])
PY
)" || return 1
    curl --fail-with-body -sS --max-time 30 -X DELETE "$JOURNEY_API" \
        -H "X-Loom-Delete-Fingerprint: $fingerprint" > "$JOURNEY_STATE/delete-workspace.json" || {
        printf 'guarded workspace deletion refused; evidence retained\n' > "$JOURNEY_STATE/cleanup.failed"
        return 1
    }
    curl -fsS --max-time 10 "$AFT_BASE_URL/api/workspaces" > "$JOURNEY_STATE/workspaces-after-delete.json" || return 1
    python3 - "$JOURNEY_STATE" <<'PY' || return 1
import json,pathlib,sys
state=pathlib.Path(sys.argv[1]);ws=json.load(open(state/'workspace.json'))['data']['id'];rows=json.load(open(state/'workspaces-after-delete.json'))
if isinstance(rows,dict):rows=rows.get('data',rows.get('workspaces',[]))
assert isinstance(rows,list),rows
assert not any(row.get('id')==ws for row in rows),(ws,rows)
product=pathlib.Path((state/'product-root-before-delete.txt').read_text().strip())
manifest=json.load(open(state/'product-before-delete-manifest.json'))
managed=[pathlib.Path(t['path']) for t in manifest['trees'] if t['managed']]
assert all(not path.exists() for path in managed),managed
assert all(not pathlib.Path(item['path']).exists() for item in manifest['preview']['items']),manifest['preview']
# Registrations in every retained source Git repository must omit removed copies.
import subprocess
registrations={}
for tree in manifest['trees']:
    if tree['managed']:continue
    path=pathlib.Path(tree['path'])
    listing=subprocess.check_output(['git','--no-optional-locks','-C',str(path),'worktree','list','--porcelain'],text=True)
    assert all('worktree '+str(p)+'\n' not in listing for p in managed),listing
    registrations[str(path)]=listing
# Only proven empty owned parent directories may be removed. Retain metadata.
if product.exists():
    for path in sorted((p for p in product.rglob('*') if p.is_dir() and not p.is_symlink()),key=lambda p:len(p.parts),reverse=True):
        if not any(path.iterdir()):path.rmdir()
    if not any(product.iterdir()):product.rmdir()
retained=[str(p) for p in product.rglob('*')] if product.exists() else []
assert not any(p.name=='.git' for p in product.rglob('*')) if product.exists() else True
(state/'cleanup-post-state.json').write_text(json.dumps(dict(workspace=ws,registered=False,managed_paths_removed=[str(p) for p in managed],registrations=registrations,product_root_removed=not product.exists(),retained_metadata=retained))+'\n')
PY
    printf 'workspace API deletion and removal readbacks succeeded; local archives/provider evidence retained\n' > "$JOURNEY_STATE/cleanup.txt"
    rm "$JOURNEY_STATE/cleanup.failed"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    phase="${1:?phase required}" key="${2:?fixture key required}"
    shift 2
    if [[ "$phase" == setup ]]; then
        journey_setup "$key" "$@"
    else
        journey_load "$key"
        case "$phase" in
            create-task) journey_create_task "$@" ;;
            start-task) journey_start_task "$@" ;;
            wait-revision) journey_wait_revision "$@" ;;
            readback) journey_readback "$@" ;;
            open-task) journey_open_task "$@" ;;
            teardown) journey_teardown ;;
            *) echo "unknown journey phase: $phase" >&2; exit 2 ;;
        esac
    fi
fi

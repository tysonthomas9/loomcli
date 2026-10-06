#!/usr/bin/env bash
# Deterministic product journeys only. No journal/lease/session fabrication.
# Human mutations are YAML UI steps; helper mutations identify an API or local
# editor/provider actor. Each helper phase is one action or one readback.
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
    print('Recovery phase exceeded 105 seconds: '+repr(args),file=sys.stderr)
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
    state=pathlib.Path(os.environ.get('AFT_WORK_DIR','.'),'journey-recovery',key)
    print(f'Recovery phase failed ({code}): {args}; retain {state} and server logs',file=sys.stderr)
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

python3 - "$@" <<'PY'
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

phase = sys.argv[1]
slot = sys.argv[2] if len(sys.argv) > 2 else ''
slots = ('hold', 'incomplete', 'delete', 'foreign')
assert not os.environ.get('AFT_REAL_BACKEND') and os.environ.get('AFT_REAL_CODEX', '0') != '1', 'Recovery suite requires stub AI'
root = Path(os.environ['AFT_WORK_DIR']).resolve() / 'journey-recovery'
base = os.environ['AFT_BASE_URL'].rstrip('/')
forge = os.environ['AFT_FAKE_GH_BASE'].rstrip('/')
for url in (base, forge):
    assert urllib.parse.urlparse(url).hostname in ('localhost', '127.0.0.1', '::1'), 'Only the owned local AFT stack is allowed'
if phase != 'setup':
    assert slot in slots, slot
case = root / slot
workspace = '' if phase == 'setup' else json.loads((case / 'workspace.json').read_text())['data']['id']
api = base + '/api/workspaces/' + workspace
repo = case / 'source'
remote = case / 'origin.git'


def save(name, value):
    (case / name).write_text(json.dumps(value, indent=2) + '\n')


def read(name):
    return json.loads((case / name).read_text())


def request(url, method='GET', body=None, expected=200, receipt=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, method=method, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=5) as response:
            status, raw = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, raw = error.code, error.read()
    value = json.loads(raw) if raw else {}
    if receipt:
        save(receipt, {'status': status, 'body': value})
    allowed = (expected,) if isinstance(expected, int) else expected
    assert status in allowed, (method, url, status, value)
    return value


def git(path, *args):
    try:
        return subprocess.check_output(['git', '-C', str(path), *args], stderr=subprocess.PIPE).decode().rstrip('\n')
    except subprocess.CalledProcessError as error:
        raise AssertionError(error.stderr.decode()) from error


def revisions():
    return request(api + '/issues/' + read('task.json')['data']['id'] + '/revisions?lead=lead')['data']


def newest():
    rows = revisions()
    assert rows, 'No product-created revision'
    return max(rows, key=lambda row: row['number'])


def wait_for(fn, description, attempts=90):
    deadline = time.monotonic() + 70
    for _ in range(attempts):
        if time.monotonic() >= deadline:
            break
        value = fn()
        if value:
            return value
        time.sleep(2)
    raise AssertionError(description)


def lead():
    branch = 'refs/heads/loom/ws/' + workspace + '/interactive/lead'
    blocks = git(repo, 'worktree', 'list', '--porcelain').split('\n\n')
    matches = [Path(block.splitlines()[0][9:]).resolve() for block in blocks if ('branch ' + branch) in block.splitlines()]
    assert len(matches) == 1, (branch, blocks)
    path = matches[0]
    assert path.is_relative_to(root), ('Working area is not owned by this suite', path)
    assert git(path, 'rev-parse', '--path-format=absolute', '--git-common-dir') == git(repo, 'rev-parse', '--path-format=absolute', '--git-common-dir')
    return path


def hashes(path, names):
    return {name: hashlib.sha256((path / name).read_bytes()).hexdigest() if (path / name).is_file() else None for name in names}


def snapshot(label):
    path = lead()
    rows = revisions()
    data = {
        'workspace': workspace, 'task': read('task.json')['data']['id'],
        'lead_path': str(path), 'lead_head': git(path, 'rev-parse', 'HEAD'),
        'index_entries': git(path, 'ls-files', '--stage'),
        'status': git(path, 'status', '--porcelain=v1', '--untracked-files=all'),
        'files': hashes(path, ('review-output.txt', 'user-index.txt', 'user-kept.txt', 'sentinel.txt', 'server.pem')),
        'trunk': git(repo, 'rev-parse', 'refs/heads/main'),
        'remote_refs': git(remote, 'for-each-ref', '--format=%(refname) %(objectname)'),
        'revisions': rows,
        'issue': request(api + '/issues/' + read('task.json')['data']['id'])['data'],
        'pulls': request(forge + '/__pulls?workspace=' + workspace),
    }
    save(label + '.json', data)
    print(json.dumps({'checkpoint': label, 'workspace': workspace, 'task': data['task'], 'lead_head': data['lead_head'], 'revisions': [(r['number'], r['head_sha'], r['incomplete']) for r in rows]}))
    return data


def unchanged(before, after, fields):
    for field in fields:
        assert before[field] == after[field], (field, before[field], after[field])


def verdict_url(row):
    return api + '/changes/' + row['change_id'] + '/revisions/' + str(row['number']) + '/verdict'


if phase == 'setup':
    assert not root.exists(), 'Fixture root already exists; never reuse another run'
    root.mkdir()
    for name in slots:
        case = root / name
        case.mkdir()
        repo, remote = case / 'source', case / 'origin.git'
        workspace_name = 'e2e-ws-recovery-' + name
        git(case, 'init', '-q', '--bare', str(remote))
        git(remote, 'symbolic-ref', 'HEAD', 'refs/heads/main')
        git(case, 'init', '-q', '-b', 'main', str(repo))
        for file in ('review-output.txt', 'user-index.txt', 'user-kept.txt'):
            (repo / file).write_text('base\n')
        git(repo, 'add', '--', 'review-output.txt', 'user-index.txt', 'user-kept.txt')
        git(repo, '-c', 'user.name=AFT', '-c', 'user.email=aft@example.test', 'commit', '-q', '-m', 'Recovery fixture baseline')
        bridge = Path(os.environ['AFT_TESTS_DIR']) / 'fixtures/fake-github/git-ssh-bridge.sh'
        assert bridge.is_file(), 'Reviewed fake-forge harness overlay is required'
        import shlex
        git(repo, 'config', 'core.sshCommand', 'sh ' + shlex.quote(str(bridge)) + ' ' + shlex.quote(str(remote)))
        git(repo, 'remote', 'add', 'origin', 'git@github.com:owner/recovery-' + name + '.git')
        git(repo, 'push', '-q', 'origin', 'main')
        # The API provisions all workspace/runtime state; the directory override
        # keeps every working area and attempt copy under this run's own root.
        created = request(base + '/api/workspaces', 'POST', {'name': workspace_name, 'type': 'empty', 'path': str(case / 'workspace'), 'repos': [str(repo)]}, expected=201)
        save('workspace.json', created)
        workspace = created['data']['id']
        assert workspace, created
        api = base + '/api/workspaces/' + workspace
        request(forge + '/__register', 'POST', {'repo': 'owner/recovery-' + name, 'remote': str(remote), 'native_stacks': False}, expected=201)
        request(api + '/agents', 'POST', {'name': 'lead', 'role_name': 'lead', 'auto': False, 'cross_repo': True, 'repos': [], 'backend': 'codex'}, expected=(200, 201))
        if name == 'foreign':
            subprocess.run([os.environ['AFT_LOOM_BIN'], 'delivery-mode', 'trunk', '--workspace', workspace], env={**os.environ, 'LOOM_CONFIG_DIR': os.environ['AFT_LOOM_CONFIG_DIR'], 'LOOM_WORKSPACE': workspace}, check=True)
        epic = request(api + '/issues', 'POST', {'title': 'Recovery ' + name + ' epic', 'issue_type': 'epic', 'priority': 2}, expected=(200, 201))
        save('epic.json', epic)
        task = request(api + '/issues', 'POST', {'title': 'Recovery ' + name + ' task', 'issue_type': 'task', 'priority': 2, 'parent': epic['data']['id'], 'design': 'STUB_CODEX_PATCH=' + ('server.pem' if name == 'incomplete' else 'review-output.txt')}, expected=(200, 201))
        save('task.json', task)

elif phase == 'start':
    request(api + '/workflows/epic-runner', 'POST', {'epicId': read('epic.json')['data']['id'], 'runner': 'local-task-runner'}, expected=(200, 201, 202), receipt='start.json')

elif phase == 'wait-revision':
    # Observer readiness/readback only: strict incomplete capture assertions
    # remain in captured, after the mounted UI checkpoint so failures are visible.
    wait_for(revisions, 'TaskRun did not freeze a revision')
    save('observed-revisions.json', revisions())
    snapshot('observed-capture')

elif phase == 'captured':
    wait_for(revisions, 'TaskRun did not freeze a revision')
    rows = revisions()
    assert len(rows) == 1, rows
    row = rows[0]
    assert row['incomplete'] == (slot == 'incomplete') and row['no_changes'] is False and not row.get('verdict'), row
    git(repo, 'cat-file', '-e', row['head_sha'] + '^{commit}')
    save('source-revision.json', row)
    if slot == 'incomplete':
        # Inspect only product-created copies under the API-provisioned root.
        candidates = list((case / 'workspace' / '.loom' / 'task-copies').glob('*/*/server.pem'))
        task = read('task.json')['data']['id']
        candidates = [p for p in candidates if ('task=' + task) in p.read_text()]
        assert len(candidates) == 1, candidates
        path = candidates[0]
        save('retained-copy.json', {'path': str(path.parent.resolve()), 'head': git(path.parent, 'rev-parse', 'HEAD'), 'index': git(path.parent, 'ls-files', '--stage'), 'hash': hashes(path.parent, ('server.pem',))['server.pem']})
        assert 'server.pem' not in git(repo, 'ls-tree', '-r', '--name-only', row['head_sha']).splitlines()
        issue = request(api + '/issues/' + task)['data']
        assert issue['status'] != 'closed', issue
    else:
        task = read('task.json')['data']['id']
        wait_for(lambda: request(api + '/issues/' + task)['data']['status'] == 'review', 'Nonempty task did not enter review')
    snapshot('captured')

elif phase == 'open-board':
    subprocess.run(['agent-browser', '--session', os.environ['AFT_SESSION'], 'open', base + '/ws/' + workspace + '/kanban'], check=True)

elif phase == 'open-task':
    subprocess.run(['agent-browser', '--session', os.environ['AFT_SESSION'], 'open', base + '/ws/' + workspace + '/kanban'], check=True)
    subprocess.run(['agent-browser', '--session', os.environ['AFT_SESSION'], 'wait', '[data-testid=board-toolbar]'], check=True)
    subprocess.run(['agent-browser', '--session', os.environ['AFT_SESSION'], 'open', base + '/ws/' + workspace + '/issues/' + read('task.json')['data']['id']], check=True)

elif phase == 'dirty':
    path = lead()
    # Local editor agent: change the overlapping file and unrelated work, with
    # a staged edit and an untracked sentinel. This is an actor action, not a
    # hand-seeded capture, revision, journal, or lease.
    (path / 'review-output.txt').write_text('unsaved local editor work\n')
    (path / 'user-index.txt').write_text('staged local editor work\n')
    (path / 'user-kept.txt').write_text('unstaged local editor work\n')
    (path / 'sentinel.txt').write_text('untracked local editor work\n')
    git(path, 'add', '--', 'user-index.txt')
    snapshot('hold-before')

elif phase == 'held':
    row = newest()
    assert row['verdict'] == 'approve' and row['follow_status'] == 'apply_pending' and not row['applied'], row
    before, after = read('hold-before.json'), snapshot('hold-held')
    unchanged(before, after, ('lead_path', 'lead_head', 'index_entries', 'status', 'files', 'trunk', 'remote_refs', 'pulls'))
    assert after['issue']['status'] == 'review' and 'code-review' in after['issue']['labels'], after['issue']
    assert len(after['revisions']) == 1 and after['revisions'][0]['head_sha'] == read('source-revision.json')['head_sha']

elif phase == 'save-overlap':
    path = lead()
    # Local editor agent saves the held file outside the checkout before
    # restoring its original bytes. Other staged/unstaged/untracked work stays.
    data = (path / 'review-output.txt').read_bytes()
    assert hashlib.sha256(data).hexdigest() == read('hold-before.json')['files']['review-output.txt']
    saved = case / 'saved-unsaved-edit.txt'
    assert not saved.exists()
    saved.write_bytes(data)
    (path / 'review-output.txt').write_bytes(subprocess.check_output(['git', '-C', str(path), 'show', 'HEAD:review-output.txt']))
    save('overlap-saved.json', {'path': str(saved), 'hash': hashlib.sha256(data).hexdigest()})

elif phase == 'retry-apply':
    row = read('source-revision.json')
    result = request(api + '/git/apply', 'POST', {'change': row['change_id'], 'revision': row['number'], 'lead': 'lead'}, receipt='retry-apply.json')
    assert result.get('success') is True, result

elif phase == 'recovered':
    task = read('task.json')['data']['id']
    wait_for(lambda: newest().get('applied') and request(api + '/issues/' + task)['data']['status'] == 'closed', 'Apply retry did not settle the task')
    before, after = read('hold-before.json'), snapshot('hold-recovered')
    assert after['lead_head'] != before['lead_head']
    unchanged(before, after, ('lead_path', 'trunk', 'remote_refs', 'pulls'))
    for name in ('user-index.txt', 'user-kept.txt', 'sentinel.txt'):
        assert before['files'][name] == after['files'][name], name
    before_entry = [l for l in before['index_entries'].splitlines() if l.endswith('\tuser-index.txt')]
    assert before_entry == [l for l in after['index_entries'].splitlines() if l.endswith('\tuser-index.txt')]
    assert 'M  user-index.txt' in after['status'] and ' M user-kept.txt' in after['status'] and '?? sentinel.txt' in after['status']
    saved = read('overlap-saved.json')
    assert hashlib.sha256(Path(saved['path']).read_bytes()).hexdigest() == before['files']['review-output.txt']
    assert len(after['revisions']) == 1, after['revisions']
    row = read('source-revision.json')
    assert (lead() / 'review-output.txt').read_bytes() == subprocess.check_output(['git', '-C', str(repo), 'show', row['head_sha'] + ':review-output.txt'])

elif phase == 'incomplete-denied':
    row = newest()
    request(verdict_url(row), 'POST', {'head_sha': row['head_sha'], 'verdict': 'approve', 'lead': 'lead', 'approve_only': True, 'actor': {'kind': 'human', 'id': 'aft-api-client'}}, expected=409, receipt='incomplete-verdict-denied.json')
    assert 'capture_incomplete' in json.dumps(read('incomplete-verdict-denied.json'))
    request(api + '/git/apply', 'POST', {'change': row['change_id'], 'revision': row['number'], 'lead': 'lead'}, expected=409, receipt='incomplete-apply-denied.json')
    assert 'capture_incomplete' in json.dumps(read('incomplete-apply-denied.json'))
    retained = read('retained-copy.json')
    path = Path(retained['path'])
    assert path.is_relative_to(case) and path.is_dir()
    assert hashes(path, ('server.pem',))['server.pem'] == retained['hash']
    assert git(path, 'rev-parse', 'HEAD') == retained['head'] and git(path, 'ls-files', '--stage') == retained['index']
    after = snapshot('incomplete-denied')
    before = read('captured.json')
    unchanged(before, after, ('lead_head', 'index_entries', 'files', 'trunk', 'remote_refs', 'pulls', 'revisions'))
    assert after['issue']['status'] != 'closed'

elif phase == 'delete-dirty':
    path = lead()
    (path / 'server.pem').write_text('non-secret test marker refused by secret-path policy\n')
    (path / 'user-kept.txt').write_text('dirty deletion sentinel\n')
    snapshot('delete-before')
    preview = request(api + '/delete/preview')
    assert len(preview['fingerprint']) == 64 and any('server.pem' in json.dumps(item) for item in preview['items']), preview
    save('delete-preview.json', preview)

elif phase == 'delete-stale':
    # Local editor agent writes AFTER the human has opened the real preview.
    path = lead()
    (path / 'user-kept.txt').write_text('dirty deletion sentinel changed after preview\n')
    snapshot('delete-stale-before')

elif phase == 'delete-stale-readback':
    after = snapshot('delete-stale-denied')
    unchanged(read('delete-stale-before.json'), after, ('lead_head', 'index_entries', 'files', 'trunk', 'remote_refs', 'revisions'))
    current = request(api + '/delete/preview')
    assert current['fingerprint'] != read('delete-preview.json')['fingerprint']

elif phase == 'delete-incomplete-readback':
    after = snapshot('delete-incomplete-denied')
    unchanged(read('delete-stale-before.json'), after, ('lead_head', 'index_entries', 'files', 'trunk', 'remote_refs', 'revisions'))
    assert (lead() / 'server.pem').is_file()
    # No successful deletion/expiry expectation: retain the refused folder.

elif phase == 'stale-verdict':
    row = newest()
    # Real earlier SHA: the task's baseline, not fabricated revision state.
    stale = git(repo, 'rev-parse', 'refs/heads/main')
    assert stale != row['head_sha']
    request(verdict_url(row), 'POST', {'head_sha': stale, 'verdict': 'approve', 'lead': 'lead', 'approve_only': True, 'actor': {'kind': 'human', 'id': 'aft-api-client'}}, expected=409, receipt='stale-verdict-denied.json')
    assert 'stale_subject' in json.dumps(read('stale-verdict-denied.json'))
    before, after = read('captured.json'), snapshot('stale-verdict-denied')
    unchanged(before, after, ('lead_head', 'index_entries', 'files', 'trunk', 'remote_refs', 'revisions', 'pulls'))

elif phase == 'published':
    wait_for(lambda: len(request(forge + '/__pulls?workspace=' + workspace)) == 1, 'UI approval did not publish exactly one PR', attempts=60)
    after = snapshot('foreign-published')
    assert len(after['pulls']) == 1
    pull = after['pulls'][0]
    row = newest()
    assert row['applied'] and row['pr_number'] == pull['number'] and pull['base']['ref'] == 'main'
    assert git(remote, 'rev-parse', 'refs/heads/' + pull['head']['ref']) == row['head_sha']
    save('publication.json', pull)

elif phase == 'foreign-write':
    # Foreign provider actor makes an actual fast-forward in the owned bare
    # remote. No force, guessed token, synthetic publication or lease state.
    pull = read('publication.json')
    other = case / 'foreign-clone'
    assert not other.exists()
    git(case, 'clone', '-q', str(remote), str(other))
    git(other, 'checkout', '-q', pull['head']['ref'])
    (other / 'foreign.txt').write_text('foreign provider work must survive\n')
    git(other, 'add', '--', 'foreign.txt')
    git(other, '-c', 'user.name=Foreign', '-c', 'user.email=foreign@example.test', 'commit', '-q', '-m', 'Foreign PR branch edit')
    git(other, 'push', '-q', 'origin', 'HEAD:refs/heads/' + pull['head']['ref'])
    save('foreign-tip.json', {'sha': git(other, 'rev-parse', 'HEAD')})
    snapshot('foreign-before-denial')

elif phase == 'foreign-publish-denied':
    request(api + '/agents/lead/git/pr', 'POST', {'change_id': read('source-revision.json')['change_id']}, expected=409, receipt='foreign-publish-denied.json')
    assert 'diverged' in json.dumps(read('foreign-publish-denied.json'))
    before, after = read('foreign-before-denial.json'), snapshot('foreign-denied')
    unchanged(before, after, ('lead_head', 'index_entries', 'files', 'trunk', 'remote_refs'))
    assert len(after['pulls']) == 1 and after['pulls'][0]['number'] == read('publication.json')['number']
    pull = read('publication.json')
    assert git(remote, 'rev-parse', 'refs/heads/' + pull['head']['ref']) == read('foreign-tip.json')['sha']
    assert git(remote, 'show', read('foreign-tip.json')['sha'] + ':foreign.txt') == 'foreign provider work must survive'

elif phase == 'teardown':
    # Preserve refused/incomplete work and readback artifacts for review. The
    # integrator's runner owns stack/process cleanup. No fingerprint bypass or
    # manual issue closure is permitted here.
    print('Retaining owned recovery fixture for review: ' + str(case))
else:
    raise AssertionError('Unknown recovery phase ' + phase)
PY

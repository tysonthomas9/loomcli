// Runs only in the runner-owned Loom container. Never writes native/store state.
const fs = require('node:fs');
const cp = require('node:child_process');
const path = require('node:path');
const {DatabaseSync} = require('node:sqlite');
const fail = () => { throw Error('owned native lifecycle probe failed'); };
function checkSession(result, mode, ref, id, worktree) {
  if (mode === 'deleted') {
    // Pinned OpenCode b30c4d0: protocol/errors.ts and server/handlers/session-error.ts.
    // A route/auth 404 is not proof of native purge.
    if (result.status !== 404 || result.body?._tag !== 'SessionNotFoundError' ||
        result.body.sessionID !== ref.native_id ||
        result.body.message !== `Session not found: ${ref.native_id}` ||
        result.body.data !== undefined) fail();
  } else if (result.status !== 200 || result.body?.data?.id !== ref.native_id ||
             result.body.data.metadata?.agent_id !== id ||
             result.body.data.location?.directory !== worktree) fail();
}
function selfTest() {
  const ref = {harness:'opencode',native_root:'',native_id:'ses_owned'};
  const good = {status:200,body:{data:{id:'ses_owned',metadata:{agent_id:'agt_owned'},location:{directory:'/owned'}}}};
  checkSession(good,'present',ref,'agt_owned','/owned');
  const missing = {status:404,body:{_tag:'SessionNotFoundError',sessionID:'ses_owned',message:'Session not found: ses_owned'}};
  checkSession(missing,'deleted',ref,'agt_owned','/owned');
  for (const [result,mode] of [
    [{status:401,body:{}},'deleted'],
    [{status:403,body:{}},'deleted'],
    [{status:404,body:{}},'deleted'],
    [{status:404,body:{...missing.body,_tag:'NotFoundError'}},'deleted'],
    [{status:404,body:{...missing.body,sessionID:'ses_foreign'}},'deleted'],
    [{status:404,body:{...missing.body,message:'wrong session'}},'deleted'],
    [{status:404,body:{...missing.body,data:{id:'ses_owned'}}},'deleted'],
    [good,'deleted'],
    [{status:200,body:{data:{...good.body.data,id:'ses_foreign'}}},'present'],
    [{status:200,body:{data:{...good.body.data,metadata:{agent_id:'agt_foreign'}}}},'present'],
    [{status:404,body:{}},'present'],
  ]) {
    let rejected = false;
    try { checkSession(result,mode,ref,'agt_owned','/owned'); } catch { rejected = true; }
    if (!rejected) fail();
  }
  process.stdout.write('native lifecycle negative checks passed\n');
}
if (process.argv[1] === '--self-test' || process.argv[2] === '--self-test') {
  selfTest();
  process.exit(0);
}
const [mode, id, encoded, run, repo] = process.argv.slice(1);
const prior = JSON.parse(Buffer.from(encoded, 'base64').toString());
if (!['capture','present','deleted'].includes(mode) || !/^agt_[A-Za-z0-9_-]+$/.test(id) ||
    !/^af[a-z0-9]{8}$/.test(run) || prior.run !== run || prior.agent_id !== id ||
    prior.repo !== repo || process.env.LOOM_CONFIG_DIR !== '/root/.loom') fail();
const db = new DatabaseSync('/root/.loom/agents.db', {readOnly:true});
const row = db.prepare('SELECT agent_id,workspace_id,name,preset,created_by_kind,created_by_id,parent_agent_id,root_agent_id,repo,harness,worktree_path,branch,harness_session_id,harness_session_root,deleted_at,history_purged_at FROM agents WHERE workspace_id=? AND agent_id=?').get('LOCALMODE', id);
if (!row || row.name !== prior.name || row.repo !== repo || row.harness !== 'opencode' ||
    row.worktree_path !== `/root/.loom/worktrees/source-repo/${id}` ||
    row.branch !== prior.branch) fail();
const expected = {
  target: 'cov-delete-target-', control: 'cov-delete-control-',
  parent: 'cov-delete-parent-', child: 'cov-delete-child-'
}[prior.label] + run;
if (row.name !== expected || !row.branch || !row.branch.startsWith('loom/agent/')) fail();
if (prior.label === 'child') {
  const parent = db.prepare('SELECT agent_id,name,preset,repo,harness FROM agents WHERE workspace_id=? AND agent_id=?').get('LOCALMODE', row.parent_agent_id);
  if (row.preset !== 'task' || row.created_by_kind !== 'agent' ||
      row.root_agent_id !== row.parent_agent_id || row.created_by_id !== row.parent_agent_id ||
      !parent || parent.name !== 'cov-delete-parent-' + run || parent.preset !== 'lead' ||
      parent.repo !== repo || parent.harness !== 'opencode' ||
      row.parent_agent_id !== prior.parent_agent_id) fail();
} else if (row.preset !== 'lead' || row.created_by_kind !== 'user' ||
           row.parent_agent_id !== null || row.root_agent_id !== null) fail();
const refs = db.prepare('SELECT harness,native_root,native_id FROM agent_native_sessions WHERE agent_id=? ORDER BY native_id').all(id);
if (mode === 'capture') {
  if (row.deleted_at || row.history_purged_at || !refs.length ||
      !row.harness_session_id || typeof row.harness_session_root !== 'string' ||
      row.harness_session_root !== '' ||
      !refs.some(r => r.harness === 'opencode' && r.native_root === '' && r.native_id === row.harness_session_id) ||
      refs.some(r => r.harness !== 'opencode' || r.native_root !== '' || !r.native_id)) fail();
} else if (!Array.isArray(prior.refs) || !prior.refs.length ||
           JSON.stringify(prior.refs) !== JSON.stringify(prior.refs.slice().sort((a,b)=>a.native_id.localeCompare(b.native_id))) ||
           !prior.refs.some(r=>r.native_id===prior.current_native_id && r.native_root===prior.current_native_root) ||
           prior.refs.some(r=>r.harness!=='opencode' || r.native_root!=='' || !r.native_id)) fail();
const source = fs.realpathSync(repo);
const worktree = row.worktree_path;
if (source !== repo || source !== '/root/.loom/workspaces/LOCALMODE/source-repo' ||
    (mode !== 'deleted' && fs.realpathSync(worktree) !== worktree) ||
    (mode === 'deleted' && fs.existsSync(worktree))) fail();
const branchRef = cp.execFileSync('git', ['-C',repo,'show-ref','--verify','refs/heads/'+row.branch], {encoding:'utf8'}).trim();
if (!branchRef || (prior.branch_ref && branchRef !== prior.branch_ref)) fail();
const registration = JSON.parse(fs.readFileSync('/root/.loom/agents-opencode/state/opencode/service.json', 'utf8'));
const base = new URL(registration.url);
if (base.protocol !== 'http:' || !['127.0.0.1','localhost'].includes(base.hostname) ||
    base.username || base.password || base.search || base.hash || base.pathname !== '/' ||
    typeof registration.password !== 'string' || !registration.password ||
    !Number.isInteger(registration.pid) || registration.pid < 1 ||
    (prior.service_pid && registration.pid !== prior.service_pid) ||
    (prior.service_url && base.href !== prior.service_url)) fail();
const authorization = 'Basic '+Buffer.from('opencode:'+registration.password).toString('base64');
async function get(endpoint) {
  const res = await fetch(new URL(endpoint,base), {headers:{Authorization:authorization},signal:AbortSignal.timeout(15000)});
  if (res.status === 401 || res.status === 403) fail();
  let body = null;
  try { body = await res.json(); } catch { /* no secret output */ }
  return {status:res.status, body};
}
(async()=>{
  const info = await get('/api/info');
  if (info.status !== 200 || info.body?.pid !== registration.pid) fail();
  const expectedRefs = mode === 'capture' ? refs : prior.refs;
  for (const ref of expectedRefs) {
    const result = await get('/api/session/'+encodeURIComponent(ref.native_id));
    checkSession(result,mode,ref,id,worktree);
  }
  if (mode === 'deleted') {
    if (!row.deleted_at || !row.history_purged_at) fail();
    if (db.prepare('SELECT COUNT(*) AS n FROM agent_events WHERE agent_id=?').get(id).n !== 0) fail();
  } else if (row.deleted_at || row.history_purged_at) fail();
  process.stdout.write(JSON.stringify({
    label:prior.label,run,agent_id:id,name:row.name,repo,worktree,
    parent_agent_id:row.parent_agent_id,branch:row.branch,branch_ref:branchRef,
    service_pid:registration.pid,service_url:base.href,
    current_native_id:mode==='capture'?row.harness_session_id:prior.current_native_id,
    current_native_root:mode==='capture'?row.harness_session_root:prior.current_native_root,
    refs:expectedRefs,stage:mode,history_purged:!!row.history_purged_at
  })+'\n');
})().catch(()=>{console.error('owned native lifecycle probe failed');process.exitCode=1});

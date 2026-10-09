import assert from 'node:assert/strict';
import { test } from 'node:test';
import { NativeRegistrationIdentity, nativeRegistrationIdentity, type AgentRow, type NativeRef } from './protocol.js';
import { createNativeSessionQueryMapper, NativeSessionQuery, NativeSessionQueryReply, type NativeSessionQueryBinding } from './native-session-query.js';

const source = { sourceKey: 'source-beta', repository: { repoName: 'beta', sourceRepoId: 'src_beta',
  repo: '/owned/beta', commonDir: '/owned/beta/.git', groups: [] }, root: { path: '/owned/beta', device: 1, inode: 2 } };
const actor: AgentRow = { agent_id: 'agt_owned', workspace_id: 'workspace', repo: '/owned/beta', worktree_path: '/owned/tree',
  branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '',
  parent_agent_id: null, root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead',
  revision: 1, state: 'idle', running_turn_id: null, deleted_at: null, history_purged_at: null };
const registration = { url: 'http://127.0.0.1:4123/', password: 'private-password', pid: 42, generation: 'native-kernel', endpointId: 'endpoint' };
const proc = { pid: 42, generation: 'native-kernel', executable: '/owned/opencode', argv: ['/owned/opencode','serve','--service'] };
const req = (operation: string, extra: Record<string,unknown> = {}) => ({ workspaceBindingId: 'binding', operation, ...extra });
function setup() {
  const calls: string[] = [];
  let row = structuredClone(actor), refs: NativeRef[] = [{ agent_id: 'agt_owned', harness: 'opencode', native_id: 'ses_owned', native_root: '' }];
  const binding: NativeSessionQueryBinding = { workspaceBindingId: 'binding', workspaceId: 'workspace', sources: [structuredClone(source)],
    secrets: ['private-password'], async verify(signal) { signal.throwIfAborted(); calls.push('verify'); },
    access: { pinnedExecutable: '/owned/opencode', async rawAgent(id,signal) { signal.throwIfAborted(); calls.push(`row:${id}`); return row; },
      async agent() { throw new Error('mapper must use fixed rawAgent'); }, async sessions(id) { calls.push(`sessions:${id}`); return refs; },
      async registration() { calls.push('registration'); return registration; }, async process() { calls.push('process'); return proc; },
      async read(route,signal) { signal.throwIfAborted(); calls.push(route); return { status: 200, body: { route } }; } },
    physical: { async source(key) { calls.push(`source:${key}`); return { repository: structuredClone(source.repository), root: { ...source.root } }; },
      async agent(id) { calls.push(`physical:${id}`); return { agentId: id, root: { path: actor.worktree_path, device: 1, inode: 3 }, commonDir: source.repository.commonDir }; } } };
  return { binding,calls,setRow(value: AgentRow) { row=value; },setRefs(value: NativeRef[]) { refs=value; },
    invoke: (raw: unknown,signal=new AbortController().signal) => createNativeSessionQueryMapper(binding)(raw,signal) };
}
test('private mapper delegates fixed queries and omits the real credential without a placeholder', async () => {
  const h=setup(), run=createNativeSessionQueryMapper(h.binding);
  assert.deepEqual((await run(req('raw-agent',{agentId:'agt_owned'}),new AbortController().signal)).data,actor);
  const identity=await run(req('registration-identity'),new AbortController().signal);
  assert.deepEqual(identity.data,{url:registration.url,pid:42,generation:'native-kernel',endpointId:'endpoint'});
  assert.ok(!JSON.stringify(identity).includes('password'));
  assert.deepEqual((await run(req('native-process'),new AbortController().signal)).data,proc);
  assert.deepEqual((await run(req('sessions',{agentId:'agt_owned',maxRegistrations:1}),new AbortController().signal)).data,
    [{agent_id:'agt_owned',harness:'opencode',native_id:'ses_owned',native_root:''}]);
  for (const [operation,extra,path] of [
    ['native-info',{},'/api/info'],
    ['native-session',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:''},'/api/session/ses_owned'],
    ['native-messages',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:'',limit:200},'/api/session/ses_owned/message?order=asc&limit=200'],
  ] as const) {
    const value=await run(req(operation,extra),new AbortController().signal);
    assert.deepEqual(value.data,{status:200,body:{route:path}});
    assert.ok(h.calls.includes(path));
  }
});
test('credential-free identity rejects raw secrets and foreign/credential-bearing endpoint URLs', () => {
  assert.throws(()=>NativeRegistrationIdentity.parse(registration));
  for(const url of ['https://example.test/','http://user:pass@127.0.0.1:4123/','http://127.0.0.1:4123/path','http://127.0.0.1:4123/?token=x'])
    assert.throws(()=>nativeRegistrationIdentity({...registration,url}));
  assert.deepEqual(nativeRegistrationIdentity(registration),{url:registration.url,pid:42,generation:'native-kernel',endpointId:'endpoint'});
});
test('closed binding/query grammar rejects unknown operations, paths and foreign sources before verification', async () => {
  const h=setup();
  for(const raw of [req('exec',{command:'id'}),req('native-info',{path:'/api/arbitrary'}),{...req('native-info'),workspaceBindingId:'foreign'},
    req('source-physical',{sourceKey:'foreign'}),req('raw-agent',{agentId:'foreign'}),req('native-session',{agentId:'agt_owned',nativeSessionId:'../foreign',nativeRoot:''}),
    req('native-messages',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:'',limit:201})]) await assert.rejects(h.invoke(raw));
  assert.deepEqual(h.calls,[]);
  assert.throws(()=>NativeSessionQuery.parse(req('native-info',{sql:'SELECT * FROM agents'})));
  assert.throws(()=>NativeSessionQueryReply.parse({workspaceBindingId:'binding',operation:'registration-identity',data:registration}));
});
test('finite source snapshots preserve beta association and reject later topology/root substitution', async () => {
  const h=setup(),run=createNativeSessionQueryMapper(h.binding);
  assert.deepEqual((await run(req('source-physical',{sourceKey:'source-beta'}),new AbortController().signal)).data,
    {repository:source.repository,root:source.root});
  assert.deepEqual((await run(req('agent-physical',{agentId:'agt_owned'}),new AbortController().signal)).data,
    {agentId:'agt_owned',root:{path:'/owned/tree',device:1,inode:3},commonDir:'/owned/beta/.git'});
  h.binding.sources[0]!.repository.repo='/foreign';
  assert.deepEqual((await run(req('raw-agent',{agentId:'agt_owned'}),new AbortController().signal)).data,actor);
  h.binding.physical.source=async()=>({repository:source.repository,root:{...source.root,inode:99}});
  await assert.rejects(createNativeSessionQueryMapper({...h.binding,sources:[source]})(req('source-physical',{sourceKey:'source-beta'}),new AbortController().signal));
  assert.throws(()=>createNativeSessionQueryMapper({...h.binding,sources:[source,source]}));
});
test('foreign rows, registrations and physical common directories never become native reads', async () => {
  const h=setup();
  h.setRow({...actor,workspace_id:'foreign'});
  await assert.rejects(h.invoke(req('native-session',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:''})));
  assert.ok(!h.calls.some(v=>v.startsWith('/api/')));
  h.setRow(actor);h.setRefs([{agent_id:'agt_foreign',harness:'opencode',native_id:'ses_owned',native_root:''}]);
  await assert.rejects(h.invoke(req('native-session',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:''})));
  h.setRefs([{agent_id:'agt_owned',harness:'opencode',native_id:'ses_owned',native_root:''},{agent_id:'agt_owned',harness:'opencode',native_id:'ses_owned',native_root:''}]);
  await assert.rejects(h.invoke(req('sessions',{agentId:'agt_owned',maxRegistrations:10})));
  h.binding.physical.agent=async id=>({agentId:id,root:{path:'/owned/tree',device:1,inode:3},commonDir:'/foreign/.git'});
  await assert.rejects(h.invoke(req('agent-physical',{agentId:'agt_owned'})));
});
test('actual response bounds and private material reject without dropping or sanitizing fields', async () => {
  const h=setup();
  h.setRefs([{agent_id:'agt_owned',harness:'opencode',native_id:'ses_owned',native_root:''},{agent_id:'agt_owned',harness:'opencode',native_id:'ses_other',native_root:''}]);
  await assert.rejects(h.invoke(req('sessions',{agentId:'agt_owned',maxRegistrations:1})));
  h.binding.access.read=async()=>({status:200,body:{text:'private-password'}});
  const withoutFixtureSecret={...h.binding,secrets:[]};
  await assert.rejects(createNativeSessionQueryMapper(withoutFixtureSecret)(req('native-info'),new AbortController().signal),/private material/);
  await assert.rejects(h.invoke(req('native-info')),/private material/);
  h.binding.access.read=async()=>({status:200,body:{text:'x'.repeat(4_000_001)}});
  await assert.rejects(h.invoke(req('native-info')));
  await assert.rejects(h.invoke(req('native-messages',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:'x'.repeat(1024*1024),limit:1})));
});
test('concurrent requests are rejected synchronously and post-await ownership failure denies the reply', async () => {
  const h=setup();let release!:()=>void,started!:()=>void;
  const ready=new Promise<void>(resolve=>{started=resolve;}),barrier=new Promise<void>(resolve=>{release=resolve;});
  h.binding.access.read=async()=>{started();await barrier;return {status:200,body:{pid:42}};};
  let valid=true;
  h.binding.verify=async()=>{h.calls.push('verify');assert.ok(valid,'store replaced');};
  const run=createNativeSessionQueryMapper(h.binding),first=run(req('native-info'),new AbortController().signal);
  await ready;
  const checksBeforeReply=h.calls.filter(value=>value==='verify').length;
  await assert.rejects(run(req('native-info'),new AbortController().signal),/overlaps/);
  valid=false;release();await assert.rejects(first,/store replaced/);
  assert.equal(h.calls.filter(value=>value==='verify').length,checksBeforeReply+1);
});
test('replacement during a registry callback prevents the following native read', async () => {
  const h=setup();let valid=true;
  h.binding.access.rawAgent=async()=>{valid=false;return actor;};
  h.binding.verify=async()=>{assert.ok(valid,'owner replaced');};
  await assert.rejects(h.invoke(req('native-session',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:''})),/owner replaced/);
  assert.ok(!h.calls.some(value=>value.startsWith('/api/')||value.startsWith('sessions:')));
});
test('abort after a native read denies publication and no automatic read retry occurs', async () => {
  const h=setup(),controller=new AbortController();let reads=0;
  h.binding.access.read=async(_route,signal)=>{assert.equal(signal,controller.signal);reads++;controller.abort();return {status:200,body:{pid:42}};};
  await assert.rejects(h.invoke(req('native-info'),controller.signal));assert.equal(reads,1);
  const denied=setup();await assert.rejects(denied.invoke(req('native-info'),AbortSignal.abort()));assert.deepEqual(denied.calls,[]);
});

test('source prefix query fixes desc200 and rejects caller query options before verification',async()=>{
  const h=setup();
  const request=req('native-assistant-prefix',{agentId:'agt_owned',nativeSessionId:'ses_owned',nativeRoot:''});
  for(const extra of [{limit:1},{order:'asc'},{type:'user'},{path:'/api/arbitrary'}])await assert.rejects(h.invoke({...request,...extra}));
  assert.deepEqual(h.calls,[]);
  const value=await h.invoke(request);
  assert.deepEqual(value.data,{status:200,body:{route:'/api/session/ses_owned/message?type=assistant&order=desc&limit=200'}});
  h.setRefs([]);await assert.rejects(h.invoke(request));
});
test('actual identity mapper denies absent capability and creation/source changes without alias/default',async()=>{
  const h=setup(),request=req('agent-identity',{agentId:'agt_owned'});
  await assert.rejects(h.invoke(request),/unavailable/);
  h.binding.access.agentIdentity=async()=>({...actor,name:'actual-name',created_at:'actual-created'});
  const value=await h.invoke(request);
  assert.equal(value.operation,'agent-identity');if(value.operation!=='agent-identity')throw Error('wrong operation');
  assert.equal(value.data.name,'actual-name');assert.equal(value.data.created_at,'actual-created');
  for(const corrupt of [{repo:'/foreign'}, {name:''}, {created_at:''}]){
    h.binding.access.agentIdentity=async()=>({...actor,name:'actual-name',created_at:'actual-created',...corrupt});
    await assert.rejects(h.invoke(request));
  }
  let reads=0;h.binding.access.agentIdentity=async()=>({...actor,name:'actual-name',created_at:++reads===1?'original':'replacement'});
  await assert.rejects(h.invoke(request),/changed/);
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { z } from 'zod';
import { fileURLToPath } from 'node:url';
import { readdir, mkdtemp, realpath, rm, readFile, writeFile, symlink } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin, getRegisteredResource, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createCoreProviders } from './index.js';
import { putFixture, getFixture, disposeFixtures, type OwnedFixture } from './ownership.js';
import { SavedEventsOutput } from './events.js';
import { NativeOutput } from './native.js';
import { AgentRow, type Json, type HttpResponse } from './protocol.js';

async function setup(t: { after(fn: () => Promise<void>): void }) {
  const root = fileURLToPath(new URL('.', import.meta.url));
  const files = (await readdir(root)).filter(name => name.endsWith('.ts') && !name.endsWith('.test.ts'));
  const pin = calculateImplementationPin(root, files, 'index.ts', 'createCoreProviders');
  const registry = new CapabilityRegistry();
  for (const provider of createCoreProviders(pin)) registry.register(provider);
  const context = createCapabilityContext({ file: 'deterministic.test.yaml', line: 1 }, registry, '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002');
  const evidenceRoot = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-adapter-evidence-')));
  t.after(() => rm(evidenceRoot, { recursive: true }));
  const evidenceStore = await createEvidenceStore(evidenceRoot);
  putEvidenceStore(context, evidenceStore);
  const row = AgentRow.parse({ agent_id: 'agt_owned', workspace_id: 'workspace', repo: '/owned/source', worktree_path: '/owned/tree',
    branch: 'loom/agent/owned', harness: 'opencode', harness_session_id: 'ses_owned', harness_session_root: '', parent_agent_id: null,
    root_agent_id: null, created_by_kind: 'user', created_by_id: null, preset: 'lead', revision: 1, state: 'idle', running_turn_id: null,
    deleted_at: null, history_purged_at: null });
  let disposed = 0;
  let reads = 0;
  let payload: Json = { text: 'independently observed value', password: 'private-password' };
  const fixture: OwnedFixture = { leaseId: 'lease', runId: context.runId, caseId: context.caseId, suiteId: context.suiteId, scope: context.scope, workspaceId: 'workspace', repo: '/owned/source',
    profile: 'deterministic', expiresAtUtcMs: Date.now() + 100000, evidenceClass: 'deterministic', roots: new Map(),
    agents: new Map([['agt_owned', { row, commonDir: '/owned/source/.git' }]]), secrets: ['private-password'],
    readApi: async () => { reads++; return { status: 200, body: { events: [{ agent_id: row.agent_id, seq: 1, event_id: 'event_1',
      kind: 'item.completed', turn_id: 'turn_1', payload, created_at: '2026-10-09T00:00:00Z' }], snapshot_seq: 1, next: 1, more: false } }; },
    readFiles: async () => ({ status: 404, body: {} }), resolveAgent: async () => ({ row, commonDir: '/owned/source/.git' }),
    verify: async () => {}, dispose: async () => { disposed++; } };
  putFixture(context, fixture);
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  return { registry, context, fixture, invoke, evidenceStore, get reads() { return reads; }, get disposed() { return disposed; }, setPayload(value: Json) { payload = value; } };
}
const input = { agent: { fixtureLeaseId: 'lease', workspaceId: 'workspace', agentId: 'agt_owned' }, after: 0, pageSize: 2, maxPages: 2, maxRecords: 10, kinds: [] };
test('agent observations distinguish missing requested model/outcome from actual nullable and completed values', async t => {
  const harness = await setup(t);
  const missing = await harness.invoke('loom.agent.observe',{agent:input.agent});
  assert.equal(missing.availability,'observed');
  assert.ok(missing.data && typeof missing.data==='object' && !Array.isArray(missing.data));
  assert.deepEqual(missing.data.requestedModel,{present:false,value:null});
  assert.deepEqual(missing.data.outcome,{present:false,value:null});
  const row = harness.fixture.agents.get('agt_owned')!.row;
  row.model='requested/provider';row.outcome='completed';row.state='finished';
  const observed = await harness.invoke('loom.agent.observe',{agent:input.agent});
  assert.ok(observed.data && typeof observed.data==='object' && !Array.isArray(observed.data));
  assert.equal(observed.data.state,'finished');
  assert.deepEqual(observed.data.requestedModel,{present:true,value:'requested/provider'});
  assert.deepEqual(observed.data.outcome,{present:true,value:'completed'});
  row.model=null;row.outcome=null;
  const nullable = await harness.invoke('loom.agent.observe',{agent:input.agent});
  assert.ok(nullable.data && typeof nullable.data==='object' && !Array.isArray(nullable.data));
  assert.deepEqual(nullable.data.requestedModel,{present:true,value:null});
  row.outcome={malformed:true};
  assert.equal((await harness.invoke('loom.agent.observe',{agent:input.agent})).availability,'error');
});
test('canonical registry pins captured saved-event snapshot before reread and rejects later tail substitution', async t => {
  const harness = await setup(t);
  const capture = SavedEventsOutput.parse((await harness.invoke('loom.api.savedEvents',input)).data);
  const routes:string[] = [];
  harness.fixture.readApi = async route => { routes.push(route); return {status:200,body:{
    events:[{agent_id:'agt_owned',seq:1,event_id:'event_1',kind:'item.completed',turn_id:'turn_1',
      payload:{text:'independently observed value'},created_at:'2026-10-09T00:00:00Z'}],snapshot_seq:1,next:1,more:false}}; };
  const reread = await harness.invoke('loom.api.savedEvents',{...input,snapshotSeq:capture.snapshotSeq});
  assert.equal(reread.availability,'observed');
  assert.match(routes[0]!,/snapshot=1/);
  harness.fixture.readApi = async () => ({status:200,body:{events:[],snapshot_seq:2,next:0,more:false}});
  const substituted = await harness.invoke('loom.api.savedEvents',{...input,snapshotSeq:capture.snapshotSeq});
  assert.equal(substituted.availability,'error'); assert.equal(substituted.data,undefined);
  harness.fixture.readApi = async () => ({status:200,body:{events:[],snapshot_seq:1,next:0,more:false}});
  const missing = await harness.invoke('loom.api.savedEvents',{...input,snapshotSeq:capture.snapshotSeq});
  assert.equal(missing.availability,'incomplete'); assert.equal(missing.data,undefined);
});
test('public registry validates before transport and returns redacted typed evidence', async t => {
  const harness = await setup(t);
  await assert.rejects(harness.invoke('loom.api.savedEvents', { ...input, command: 'unsafe' }));
  assert.equal(harness.reads, 0);
  const observed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(observed.availability, 'observed'); assert.equal(observed.provenance.evidenceClass, 'deterministic');
  assert.equal(observed.provenance.identity.agentId, 'agt_owned');
  assert.ok(!JSON.stringify(observed).includes('private-password'));
  assert.equal(observed.provenance.artifacts[0]!.redaction, 'sanitized');
  const retained = await harness.evidenceStore.resolve(observed.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), observed.data);
  const schema = SavedEventsOutput;
  const parsed = schema.parse(observed.data);
  assert.equal(z.object({text:z.string()}).parse(parsed.events[0]!.payload).text, 'independently observed value');
  assert.deepEqual(parsed.events[0]!.redaction.omittedPaths,['/password']);
  assert.equal(Object.hasOwn(parsed.events[0]!.payload as object,'password'),false);
  assert.notEqual(z.object({text:z.string()}).parse(parsed.events[0]!.payload).text, 'caller expected value');
  harness.setPayload({ text: 'changed actual value' });
  const changed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(z.object({text:z.string()}).parse(schema.parse(changed.data).events[0]!.payload).text, 'changed actual value');
});
test('foreign, missing and expired leases never read observations; cleanup survives abort', async t => {
  const harness = await setup(t);
  const foreign = await harness.invoke('loom.api.savedEvents', { ...input, agent: { ...input.agent, fixtureLeaseId: 'foreign' } });
  assert.equal(foreign.availability, 'error'); assert.equal(foreign.data, undefined); assert.equal(harness.reads, 0);
  const other = { ...harness.context, caseId: 'case_2' };
  await assert.rejects(getFixture(other, 'lease'));
  harness.fixture.expiresAtUtcMs = 0;
  assert.equal((await harness.invoke('loom.api.savedEvents', input)).availability, 'error');
  const abort = new AbortController(); abort.abort(); harness.context.signal = abort.signal;
  await disposeFixtures(harness.context); assert.equal(harness.disposed, 1);
  await disposeFixtures(harness.context); assert.equal(harness.disposed, 1);
});
test('incomplete and unreadable events cannot become empty successful evidence', async t => {
  const harness = await setup(t);
  harness.fixture.readApi = async () => ({ status: 200, body: { events: [], snapshot_seq: 1, next: 0, more: false } });
  const incomplete = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(incomplete.availability, 'incomplete'); assert.equal(incomplete.data, undefined);
  harness.fixture.readApi = async () => ({ status: 403, body: { password: 'private-password' } });
  const failed = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(failed.availability, 'error'); assert.equal(failed.data, undefined);
  assert.ok(!JSON.stringify(failed).includes('private-password'));
});

test('two child cases use only exported suite fixture handles; case cleanup cannot release them', async t => {
  const harness = await setup(t);
  const suiteContext = createCapabilityContext(harness.context.source, harness.registry);
  suiteContext.runId = harness.context.runId; suiteContext.caseId = 'suite-setup';
  suiteContext.scope = 'suite'; suiteContext.suiteId = harness.context.suiteId;
  putEvidenceStore(suiteContext, harness.evidenceStore);
  const suiteFixture = { ...harness.fixture, scope: 'suite' as const, leaseId: 'suite-lease' };
  putFixture(suiteContext, suiteFixture);
  const child = (caseId: string, handles: string[]) => ({ ...harness.context, caseId, resources: new Map<string, unknown>(),
    suite: { id: suiteContext.suiteId, handles, getResource(key: string, handle: string) {
      assert.equal(handle, 'suite-lease'); assert.ok(handles.includes(handle)); return getRegisteredResource(suiteContext, key, handle);
    } } });
  const one = child('one', ['suite-lease']); const two = child('two', ['suite-lease']);
  assert.equal(await getFixture(one, 'suite-lease'), suiteFixture);
  assert.equal(await getFixture(two, 'suite-lease'), suiteFixture);
  await assert.rejects(getFixture(child('undeclared', []), 'suite-lease'));
  await assert.rejects(getFixture({ ...one, suiteId: 'foreign-suite' }, 'suite-lease'));
  const suiteInput = { ...input, agent: { ...input.agent, fixtureLeaseId: 'suite-lease' } };
  const observed = await harness.registry.invoke({ id: 'loom.api.savedEvents', version: 1, input: {} }, suiteInput, one);
  assert.equal(observed.availability, 'observed');
  assert.ok(await harness.evidenceStore.resolve(observed.provenance.artifacts[0]!.id));
  await disposeFixtures(one); await disposeFixtures(two); assert.equal(harness.disposed, 0);
  suiteFixture.expiresAtUtcMs = 0;
  await disposeFixtures(suiteContext); assert.equal(harness.disposed, 1);
  assert.equal(suiteContext.resources.has('@loom/aft-adapter/fixtures/v1:suite-lease'), false);
});

test('exact event selector rejects foreign turn/request/item and missing identifiers through registry', async t => {
  const harness = await setup(t);
  harness.setPayload({ itemId: 'item_1', requestId: 'request_1', text: 'actual answer' });
  const correlated = { ...input, eventIds: ['event_1'], turnId: 'turn_1', itemId: 'item_1', requestId: 'request_1' };
  assert.equal((await harness.invoke('loom.api.correlate', correlated)).availability, 'observed');
  for (const corrupt of [{ turnId: 'foreign' }, { itemId: 'foreign' }, { requestId: 'foreign' }, { eventIds: ['missing'] }, { eventIds: ['event_1', 'event_1'] }]) {
    const result = await harness.invoke('loom.api.correlate', { ...correlated, ...corrupt });
    assert.equal(result.availability, 'error'); assert.equal(result.data, undefined);
  }
});

test('native model and deletion evidence rejects foreign/stale/duplicate proofs through the public registry', async t => {
  const harness = await setup(t); const agent = harness.fixture.agents.get('agt_owned')!;
  const row = agent.row;
  let status = 200;
  let body: Json = { data: { id: 'ses_owned', metadata: { agent_id: row.agent_id }, location: { directory: row.worktree_path } } };
  let messages: Json = { data: [{ id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: { completed: 1 }, finish: 'stop',
    model: { providerID: 'provider', id: 'actual-model' } }] };
  agent.native = { pinnedExecutable: '/owned/opencode',
    registration: async () => ({ url: 'http://127.0.0.1:4123/', password: 'private-password', pid: 42, generation: 'gen_1', endpointId: 'endpoint_1' }),
    process: async () => ({ pid: 42, generation: 'gen_1', executable: '/owned/opencode', argv: ['/owned/opencode', 'serve', '--service'] }),
    sessions: async () => [{ agent_id: row.agent_id, harness: 'opencode', native_root: '', native_id: 'ses_owned' }], agent: async () => row,
    read: async route => route === '/api/info' ? { status: 200, body: { pid: 42 } } : route.includes('/message?') ? { status: 200, body: messages } : { status, body } };
  const nativeInput = { agent: input.agent, view: 'completed-models', nativeSessionId: 'ses_owned', nativeRoot: '', expectedGeneration: 'gen_1', maxMessages: 200 };
  const schema = NativeOutput.options[3];
  const models = await harness.invoke('loom.native.observe', nativeInput);
  assert.equal(models.availability, 'observed');
  assert.equal(schema.parse(models.data).records[0]!.model, 'actual-model');
  assert.notEqual(schema.parse(models.data).records[0]!.model, 'expected-model');
  assert.equal(schema.parse(models.data).records[0]!.finish,'stop');
  assert.equal(schema.parse(models.data).records[0]!.errorFieldPresent,false);
  assert.equal(schema.parse(models.data).records[0]!.errorTruthy,false);
  const completed = {id:'msg_1',sessionID:'ses_owned',type:'assistant',time:{completed:1},finish:'stop',model:{providerID:'provider',id:'actual-model'}};
  messages={data:[completed,
    {...completed,id:'msg_unfinished',finish:''},
    {...completed,id:'msg_null',finish:null},
    {...completed,id:'msg_failed',error:{name:'failed',message:'private-password'}},
    {...completed,id:'msg_other',error:null,model:{providerID:'provider',id:'different-model'}},
  ]};
  const allCompleted = await harness.invoke('loom.native.observe',nativeInput);
  assert.equal(allCompleted.availability,'observed');
  const completedFacts = schema.parse(allCompleted.data);
  assert.deepEqual(completedFacts.records.map(record=>[record.id,record.finish,record.errorFieldPresent,record.errorTruthy,record.model]),
    [['msg_1','stop',false,false,'actual-model'],['msg_other','stop',true,false,'different-model']]);
  assert.ok(!JSON.stringify(allCompleted).includes('private-password'));
  messages={data:[{...completed,id:'msg_failed',error:{name:'failed'}},{...completed,id:'msg_unfinished',finish:''}]};
  assert.deepEqual(schema.parse((await harness.invoke('loom.native.observe',nativeInput)).data).records,[]);
  const selected = await harness.invoke('loom.native.observe',{...nativeInput,view:'session'});
  assert.equal(NativeOutput.options[0].parse(selected.data).selectedModel,null);
  body={data:{id:'ses_owned',metadata:{agent_id:row.agent_id},location:{directory:row.worktree_path},model:{providerID:'provider',id:'selected-model'}}};
  const selectedFact = await harness.invoke('loom.native.observe',{...nativeInput,view:'session'});
  assert.deepEqual(NativeOutput.options[0].parse(selectedFact.data).selectedModel,{provider:'provider',model:'selected-model'});
  agent.native.sessions = async () => [
    {agent_id:row.agent_id,harness:'opencode',native_root:'',native_id:'ses_owned'},
    {agent_id:row.agent_id,harness:'opencode',native_root:'',native_id:'ses_old'},
  ];
  const all = await harness.invoke('loom.native.observe',{...nativeInput,view:'registrations'});
  assert.equal(all.availability,'observed');
  assert.deepEqual(NativeOutput.options[6].parse(all.data).records.map(record=>record.nativeSessionId),['ses_old','ses_owned']);

  const stale = await harness.invoke('loom.native.observe', { ...nativeInput, expectedGeneration: 'stale' });
  assert.equal(stale.availability, 'error'); assert.equal(stale.data, undefined);
  messages = { data: [{ id: 'msg_1', sessionID: 'foreign', type: 'assistant', time: {} }] };
  assert.equal((await harness.invoke('loom.native.observe', nativeInput)).availability, 'error');
  messages = { data: [{ id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: {} }, { id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: {} }] };
  assert.equal((await harness.invoke('loom.native.observe', nativeInput)).availability, 'error');
  status = 404; body = { _tag: 'SessionNotFoundError', sessionID: 'ses_owned', message: 'Session not found: ses_owned' };
  const absent = await harness.invoke('loom.native.observe', { ...nativeInput, view: 'presence' });
  assert.equal(absent.availability, 'observed'); assert.equal(NativeOutput.options[1].parse(absent.data).present, false);
  body = {_tag:'SessionNotFoundError',sessionID:'ses_old',message:'Session not found: ses_old'};
  const oldAbsent = await harness.invoke('loom.native.observe',{...nativeInput,view:'presence',nativeSessionId:'ses_old'});
  assert.equal(oldAbsent.availability,'observed'); assert.equal(NativeOutput.options[1].parse(oldAbsent.data).present,false);
  assert.equal((await harness.invoke('loom.native.observe',{...nativeInput,view:'presence',nativeSessionId:'ses_foreign'})).availability,'error');

  body = { _tag: 'NotFoundError' };
  const unknown = await harness.invoke('loom.native.observe', { ...nativeInput, view: 'presence' });
  assert.equal(unknown.availability, 'error'); assert.equal(unknown.data, undefined);
});

test('failed cleanup retains the exact owned fixture for a final retry after expiry and abort', async t => {
  const harness = await setup(t); let attempts = 0;
  harness.fixture.dispose = async () => { if (++attempts === 1) throw new Error('deterministic cleanup failure'); };
  harness.fixture.expiresAtUtcMs = 0; const abort = new AbortController(); abort.abort(); harness.context.signal = abort.signal;
  await assert.rejects(disposeFixtures(harness.context));
  assert.equal(harness.context.resources.get('@loom/aft-adapter/fixtures/v1:lease'), harness.fixture);
  await disposeFixtures(harness.context); await disposeFixtures(harness.context);
  assert.equal(attempts, 2);
});

test('native registration captures only validated owned facts and rejects changed or foreign bootstrap evidence', async t => {
  const h = await setup(t); const agent = h.fixture.agents.get('agt_owned')!; const row = agent.row;
  let registrations = 0, changed = false;
  const current = {agent_id:row.agent_id,harness:'opencode' as const,native_root:'',native_id:'ses_owned'};
  let refs = [current,{...current,native_id:'ses_prior'}];
  agent.native = {pinnedExecutable:'/owned/opencode',
    registration:async()=>{registrations++; return {url:'http://127.0.0.1:4123/',password:'private-password',pid:42,
      generation:changed && registrations > 1 ? 'replacement' : 'generation',endpointId:'endpoint'};},
    process:async()=>({pid:42,generation:'generation',executable:'/owned/opencode',argv:['/owned/opencode','serve','--service']}),
    agent:async()=>row,sessions:async()=>refs,read:async()=>({status:200,body:{pid:42}})};
  const request = {agent:input.agent,maxRegistrations:10};
  assert.deepEqual(h.registry.get('loom.native.registration',1).effects,['read-native']);
  await assert.rejects(h.invoke('loom.native.registration',{...request,expectedGeneration:'guessed'}));
  assert.equal((await h.invoke('loom.native.registration',{...request,agent:{...input.agent,fixtureLeaseId:'foreign'}})).availability,'error');
  assert.equal(registrations,0);
  const captured = await h.invoke('loom.native.registration',request);
  assert.equal(captured.availability,'observed');
  const facts = NativeOutput.options[6].parse(captured.data);
  assert.equal(facts.serviceGeneration,'generation'); assert.equal(facts.servicePid,42); assert.equal(facts.registeredEndpointId,'endpoint');
  assert.deepEqual(facts.records.map(ref=>ref.nativeSessionId),['ses_owned','ses_prior']);
  assert.ok(!JSON.stringify(captured).includes('private-password')); assert.ok(!JSON.stringify(captured).includes('127.0.0.1'));
  for (const invalid of [[current,current],[{...current,native_root:'foreign'}],[{...current,agent_id:'foreign'}]]) {
    refs=invalid; const rejected=await h.invoke('loom.native.registration',request);
    assert.equal(rejected.availability,'error'); assert.equal(rejected.data,undefined);
  }
  refs=[current]; changed=true; registrations=0;
  assert.equal((await h.invoke('loom.native.registration',request)).availability,'error');
  changed=false; agent.native.agent=async()=>({...row,repo:'/foreign/store'});
  assert.equal((await h.invoke('loom.native.registration',request)).availability,'error');
  agent.native.agent=async()=>row;
  agent.native.process=async()=>({pid:42,generation:'replacement',executable:'/owned/opencode',argv:['/owned/opencode','serve','--service']});
  assert.equal((await h.invoke('loom.native.observe',{agent:input.agent,view:'registrations',nativeSessionId:facts.currentNativeSessionId,
    nativeRoot:facts.currentNativeRoot,expectedGeneration:facts.serviceGeneration,expectedServicePid:facts.servicePid,
    expectedEndpointId:facts.registeredEndpointId,maxMessages:1,maxRegistrations:10})).availability,'error');
});

test('revoked canonical authority denies observations while exact owned cleanup remains available', async t => {
  const harness = await setup(t); revokeCapabilityContext(harness.context);
  const unavailable = await harness.invoke('loom.api.savedEvents', input);
  assert.equal(unavailable.availability, 'error'); assert.equal(unavailable.data, undefined); assert.equal(harness.reads, 0);
  await disposeFixtures(harness.context); assert.equal(harness.disposed, 1);
});

test('public native and saved-event probe facts expose actual leaks before sanitized bindings', async t => {
  const { createSyntheticProbe } = await import('./synthetic-probe.js');
  const harness = await setup(t); const fixture = harness.fixture; const agent = fixture.agents.get('agt_owned')!; const row = agent.row;
  const probe = createSyntheticProbe(harness.context.runId, fixture.leaseId); fixture.syntheticProbe = probe;
  let messageContent: Json[] = [{ type: 'tool', id: 'call_1', name: 'bash', state: { status: 'completed', input: { command: probe.value }, content: { text: probe.value } } }];
  agent.native = { pinnedExecutable: '/owned/opencode',
    registration: async () => ({ url: 'http://127.0.0.1:4123/', password: 'private-password', pid: 42, generation: 'gen_1', endpointId: 'endpoint_1' }),
    process: async () => ({ pid: 42, generation: 'gen_1', executable: '/owned/opencode', argv: ['/owned/opencode', 'serve', '--service'] }),
    sessions: async () => [{ agent_id: row.agent_id, harness: 'opencode', native_root: '', native_id: 'ses_owned' }], agent: async () => row,
    read: async (route): Promise<HttpResponse> => ({ status: 200, body: route === '/api/info' ? { pid: 42 } : route.includes('/message?') ?
      { data: [{ id: 'msg_1', sessionID: 'ses_owned', type: 'assistant', time: { completed: 1 }, content: messageContent }] } :
      { data: { id: 'ses_owned', metadata: { agent_id: row.agent_id }, location: { directory: row.worktree_path } } } }) };
  const nativeInput = { agent: input.agent, view: 'tools', nativeSessionId: 'ses_owned', nativeRoot: '', expectedGeneration: 'gen_1', maxMessages: 200, probeHandle: probe.handle };
  const nativeSchema = NativeOutput.options[4];
  const leaking = await harness.invoke('loom.native.observe', nativeInput);
  assert.equal(leaking.availability, 'observed');
  const facts = nativeSchema.parse(leaking.data).records[0]!;
  assert.equal(facts.probe!.inputOccurrences, 1); assert.equal(facts.probe!.outputOccurrences, 1);
  assert.equal(facts.outputPresent,true);
  assert.deepEqual(facts.inputRedaction.replacedTextPaths,['/command']);
  assert.deepEqual(facts.outputRedaction.replacedTextPaths,['/text']);
  assert.ok(!JSON.stringify(leaking).includes(probe.value));
  const artifact = await harness.evidenceStore.resolve(leaking.provenance.artifacts[0]!.id);
  assert.ok(!(await readFile(artifact, 'utf8')).includes(probe.value));
  const partial = await harness.invoke('loom.native.observe', { ...nativeInput, maxMessages: 1 });
  assert.equal(partial.availability, 'incomplete'); assert.equal(partial.data, undefined);
  messageContent = [{ type: 'tool', id: 'call_1', name: 'bash', state: { status: 'completed', input: { command: '[REDACTED]' }, content: {} } }];
  const alreadyRedacted = await harness.invoke('loom.native.observe', nativeInput);
  assert.equal(nativeSchema.parse(alreadyRedacted.data).records[0]!.probe!.inputOccurrences, 0);
  harness.setPayload({ itemId: 'item_1', output: probe.value });
  const saved = await harness.invoke('loom.api.savedEvents', { ...input, probeHandle: probe.handle });
  assert.equal(saved.availability, 'observed');
  assert.equal(SavedEventsOutput.parse(saved.data).events[0]!.probe!.payloadOccurrences, 1);
  assert.ok(!JSON.stringify(saved).includes(probe.value));
  const foreign = await harness.invoke('loom.api.savedEvents', { ...input, probeHandle: 'foreign-probe' });
  assert.equal(foreign.availability, 'error'); assert.equal(foreign.data, undefined);
  fixture.syntheticProbe = createSyntheticProbe('foreign-run', fixture.leaseId);
  assert.equal((await harness.invoke('loom.api.savedEvents', { ...input, probeHandle: fixture.syntheticProbe.handle })).availability, 'error');
});

test('container filesystem and Git adapters reject wrong physical and agent identity through registry', async t => {
  const { containerFilesystemObserver, containerGitObserver } = await import('./container-observations.js');
  const harness = await setup(t); let foreign = false;
  const stamp = { path: '/container/owned/source', device: 7, inode: 42 };
  harness.fixture.roots.set('container-source', { ...stamp, remoteObserve: containerFilesystemObserver(async request => {
    assert.equal(request.operation, 'filesystem-observe');
    return { root: { ...stamp, inode: foreign ? 43 : 42 }, data: { entries: [
      { relativePath: 'marker', exists: false, kind: 'missing', bytes: null, sha256: null, contentBase64: null }] } };
  }, { kind: 'managed-repo' }, stamp) });
  const fsInput = { leaseId: 'lease', rootId: 'container-source', relativePaths: ['marker'], view: 'presence', maxBytes: 100, maxEntries: 10 };
  assert.equal((await harness.invoke('loom.filesystem.observe', fsInput)).availability, 'observed');
  foreign = true; const fsForeign = await harness.invoke('loom.filesystem.observe', fsInput);
  assert.equal(fsForeign.availability, 'error'); assert.equal(fsForeign.data, undefined);
  const agent = harness.fixture.agents.get('agt_owned')!;
  agent.gitObserve = containerGitObserver(async request => {
    assert.equal(request.operation, 'git-observe');
    return { head: 'a'.repeat(40), branch: agent.row.branch, worktree: foreign ? '/container/foreign' : agent.row.worktree_path,
      commonDir: agent.commonDir, status: [], refs: [], diff: null, origin: null };
  }, input.agent, { worktree: agent.row.worktree_path, commonDir: agent.commonDir, branch: agent.row.branch });
  const gitInput = { agent: input.agent, view: 'status', paths: [], maxBytes: 1000 };
  foreign = false; assert.equal((await harness.invoke('loom.git.observe', gitInput)).availability, 'observed');
  foreign = true; const gitForeign = await harness.invoke('loom.git.observe', gitInput);
  assert.equal(gitForeign.availability, 'error'); assert.equal(gitForeign.data, undefined);
});

test('registered fixture-created workspaces bind exact agents before discovery and preserve row provenance',async t=>{
  const {createOwnedWorkspaceRoster}=await import('./workspaces.js');
  const h=await setup(t);const f=h.fixture;
  const record=async(workspaceId:string,agentIds:string[])=>{
    const source=workspaceId==='workspace'?f.repo:'/owned/second';
    const fields={identityKind:'native-agent-id' as const,workspaceId,repo:source,commonDir:source+'/.git',storeId:'owned-store',storeGeneration:'store-generation',agentIds};
    const creationReceipt=await h.evidenceStore.retain(JSON.stringify({kind:'workspace-created',leaseId:f.leaseId,runId:f.runId,suiteId:f.suiteId,
      scope:f.scope,caseId:f.caseId,profile:f.profile,...fields}));return {...fields,creationReceipt};
  };
  f.ownedWorkspaces=await createOwnedWorkspaceRoster(f,[await record('workspace',['agt_owned']),await record('E2E-WS-AGENT',['nova'])],h.evidenceStore);
  let discovered=0;
  f.resolveAgent=async(agentId,_signal,workspaceId)=>{discovered++;assert.equal(agentId,'nova');assert.equal(workspaceId,'E2E-WS-AGENT');
    return {row:{...f.agents.get('agt_owned')!.row,agent_id:'nova',workspace_id:'E2E-WS-AGENT',repo:'/owned/second'},commonDir:'/owned/second/.git'};};
  for(const input of [{leaseId:'lease',workspaceId:'foreign',agentId:'nova'},{leaseId:'lease',workspaceId:'E2E-WS-AGENT',agentId:'unregistered'}])
    assert.notEqual((await h.invoke('loom.agent.bind',input)).availability,'observed');
  assert.equal(discovered,0);
  const result=await h.invoke('loom.agent.bind',{leaseId:'lease',workspaceId:'E2E-WS-AGENT',agentId:'nova'});
  assert.equal(result.availability,'observed');assert.equal(result.provenance.identity.workspaceId,'E2E-WS-AGENT');
  assert.equal(discovered,1);
  f.readFiles=async route=>{assert.ok(route.includes('/E2E-WS-AGENT/files/stat?'));assert.ok(route.includes('repo=second'));
    return {status:200,body:{path:'file',size:1,version:'v1',is_dir:false,mod_time:'2026-10-09T00:00:00Z'}};};
  assert.equal((await h.invoke('loom.files.observe',{agent:{fixtureLeaseId:'lease',workspaceId:'E2E-WS-AGENT',agentId:'nova'},path:'file',view:'stat',maxBytes:100})).availability,'observed');
  assert.equal((await h.invoke('loom.agent.observe',{agent:{fixtureLeaseId:'lease',workspaceId:'E2E-WS-AGENT',agentId:'nova'}})).availability,'observed');
});

test('saved-history and lifecycle observations expose actual facts and never treat missing/error as zero or absence',async t=>{
  const h=await setup(t);const agent=h.fixture.agents.get('agt_owned')!;
  const request={agent:input.agent};
  assert.equal((await h.invoke('loom.agent.history',request)).availability,'unsupported');
  const unused=async():Promise<never>=>{throw new Error('unused owned native port');};
  let count=3;let wrong=false;let failed=false;
  agent.native={pinnedExecutable:'/owned/opencode',registration:unused,process:unused,sessions:unused,read:unused,agent:unused,
    history:async()=>{if(failed)throw new Error('unreadable store');return {agentId:'agt_owned',workspaceId:wrong?'foreign':'workspace',repo:h.fixture.repo,
      revision:2,deletedAt:'2026-10-09T01:00:00Z',historyPurgedAt:null,savedEventCount:count};}};
  const result=await h.invoke('loom.agent.history',request);
  assert.equal(result.availability,'observed');assert.equal(z.object({savedEventCount:z.number()}).parse(result.data).savedEventCount,3);
  count=0;assert.equal(z.object({savedEventCount:z.number()}).parse((await h.invoke('loom.agent.history',request)).data).savedEventCount,0);
  wrong=true;assert.equal((await h.invoke('loom.agent.history',request)).availability,'error');wrong=false;
  failed=true;const unavailable=await h.invoke('loom.agent.history',request);assert.equal(unavailable.availability,'error');assert.equal(unavailable.data,undefined);
  agent.gitLifecycle=async()=>({agentId:'agt_owned',sourceRoot:h.fixture.repo,commonDir:agent.commonDir,branch:agent.row.branch,
    branchRef:{ref:'refs/heads/loom/agent/owned',oid:'a'.repeat(40)},worktree:agent.row.worktree_path,worktreePresent:false});
  const git=await h.invoke('loom.git.lifecycle',{...request,maxBytes:10000});assert.equal(git.availability,'observed');
  assert.equal(z.object({worktreePresent:z.boolean()}).parse(git.data).worktreePresent,false);
  agent.gitLifecycle=async()=>{throw new Error('unreadable source');};assert.equal((await h.invoke('loom.git.lifecycle',{...request,maxBytes:10000})).availability,'error');
});

test('later UI native child binds through actual scoped identity facts without legacy-name authority',async t=>{
  const {createOwnedWorkspaceRoster}=await import('./workspaces.js');const h=await setup(t);const f=h.fixture;
  const fields={identityKind:'native-agent-id' as const,workspaceId:'workspace',repo:f.repo,commonDir:'/owned/source/.git',storeId:'native-store',storeGeneration:'generation',agentIds:['agt_owned']};
  const owner={leaseId:f.leaseId,runId:f.runId,suiteId:f.suiteId,scope:f.scope,caseId:f.caseId,profile:f.profile};
  const creationReceipt=await h.evidenceStore.retain(JSON.stringify({kind:'workspace-created',...owner,...fields}));
  f.ownedWorkspaces=await createOwnedWorkspaceRoster(f,[{...fields,creationReceipt}],h.evidenceStore);
  let foreign=false;let missingKind=false;let resolves=0;
  f.readWorkspaceAgent=async(workspaceId,agentId)=>{const fact={kind:'agent-enrolled' as const,identityKind:'native-agent-id' as const,...owner,workspaceId,agentId,repo:f.repo,
    commonDir:fields.commonDir,storeId:foreign?'foreign-store':fields.storeId,storeGeneration:fields.storeGeneration,
    parentAgentId:agentId==='agt_owned'?null:'agt_owned',rootAgentId:agentId==='agt_owned'?null:'agt_owned',
    createdByKind:agentId==='agt_owned'?'user' as const:'agent' as const,createdById:agentId==='agt_owned'?'actual-user':'agt_owned',revision:1};
    if(missingKind)Reflect.deleteProperty(fact,'identityKind');return fact;};
  f.resolveAgent=async(agentId,_signal,workspaceId)=>{resolves++;assert.equal(workspaceId,'workspace');return {
    row:{...f.agents.get('agt_owned')!.row,agent_id:agentId,worktree_path:'/owned/child',parent_agent_id:'agt_owned',root_agent_id:'agt_owned',
      created_by_kind:'agent',created_by_id:'agt_owned'},commonDir:fields.commonDir};};
  foreign=true;assert.equal((await h.invoke('loom.agent.bind',{leaseId:'lease',workspaceId:'workspace',agentId:'agt_child'})).availability,'error');
  assert.equal(resolves,0);foreign=false;
  missingKind=true;assert.equal((await h.invoke('loom.agent.bind',{leaseId:'lease',workspaceId:'workspace',agentId:'agt_child'})).availability,'error');
  assert.equal(resolves,0);missingKind=false;
  const bound=await h.invoke('loom.agent.bind',{leaseId:'lease',workspaceId:'workspace',agentId:'agt_child'});
  assert.equal(bound.availability,'observed');assert.equal(bound.provenance.identity.rootAgentId,'agt_owned');assert.equal(resolves,1);
  assert.equal((await h.invoke('loom.agent.bind',{leaseId:'lease',workspaceId:'workspace',agentId:'agt_child'})).availability,'error');
  assert.equal(resolves,1);
  const observed=await h.invoke('loom.agent.observe',{agent:{fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_child'}});
  assert.equal(observed.availability,'observed');assert.equal(observed.provenance.identity.parentAgentId,'agt_owned');
});


test('fixed temporary namespace reads preserve exact marker paths and cannot turn unavailable stat into absence', async t => {
  const {ContainerObservationRequest,containerFilesystemObserver,readContainerObservation}=await import('./container-observations.js');
  const {observeFilesystem}=await import('./filesystem.js');
  const h=await setup(t); const directory=await realpath(await mkdtemp(path.join(os.tmpdir(),'loom-temp-reader-')));
  t.after(()=>rm(directory,{recursive:true}));
  // The fake namespace is backed by exact local test-owned files. Host /tmp is never read.
  const originalRun='original-provisioned-run'; const marker='cov-controls-stop-effect-'+originalRun;
  assert.notEqual(originalRun,h.context.runId);
  const stamp={path:'/tmp',device:12,inode:34}; let changed=false, unavailable=false, calls=0;
  h.fixture.roots.set('fixture-temporary',{...stamp,remoteObserve:containerFilesystemObserver(async request=>{
    calls++; assert.equal(request.operation,'filesystem-observe');
    if(request.operation!=='filesystem-observe')throw new Error('Wrong closed request');
    assert.deepEqual(request.root,{kind:'fixture-temporary'}); assert.deepEqual(request.relativePaths,[marker]);
    if(unavailable)throw new Error('Injected unavailable stat');
    const data=await observeFilesystem({leaseId:'lease',rootId:'fake-owned-namespace',relativePaths:request.relativePaths,
      view:request.view,maxBytes:request.maxBytes,maxEntries:request.maxEntries},directory);
    return {root:{...stamp,inode:changed?35:34},data};
  },{kind:'fixture-temporary'},stamp)});
  const request={leaseId:'lease',rootId:'fixture-temporary',relativePaths:[marker],view:'presence',maxBytes:100,maxEntries:1};
  const missing=await h.invoke('loom.filesystem.observe',request);
  assert.equal(missing.availability,'observed'); assert.equal(missing.provenance.identity.fixtureLeaseId,'lease');
  assert.equal(z.object({entries:z.array(z.object({exists:z.boolean()}))}).parse(missing.data).entries[0]!.exists,false);
  await writeFile(path.join(directory,marker),'actual effect');
  assert.equal(z.object({entries:z.array(z.object({exists:z.boolean()}))}).parse((await h.invoke('loom.filesystem.observe',request)).data).entries[0]!.exists,true);
  for(const invalid of ['unavailable','changed'] as const) {
    unavailable=invalid==='unavailable'; changed=invalid==='changed';
    const rejected=await h.invoke('loom.filesystem.observe',request); assert.equal(rejected.availability,'error'); assert.equal(rejected.data,undefined);
  }
  unavailable=false;changed=false; await rm(path.join(directory,marker)); await symlink('missing-target',path.join(directory,marker));
  assert.equal((await h.invoke('loom.filesystem.observe',request)).availability,'error');
  const before=calls;
  await assert.rejects(h.invoke('loom.filesystem.observe',{...request,relativePaths:['../foreign']})); assert.equal(calls,before);
  assert.throws(()=>ContainerObservationRequest.parse({operation:'filesystem-root',root:{kind:'fixture-temporary',path:'/host/tmp'}}));
  const never=async():Promise<never>=>{throw new Error('Native access must not run');};
  await assert.rejects(readContainerObservation({operation:'filesystem-root',root:{kind:'fixture-temporary'}},
    {pinnedExecutable:'/owned/opencode',agent:never,sessions:never,registration:never,process:never,read:never},
    {workspaceId:'workspace',repo:'/owned/repo',worktreeParent:'/owned/worktrees',commonDir:'/owned/repo/.git'}));
});

// Public admission is exercised on the maintained canonical engine, before the
// normalized request reaches the fixed container reader.
test('filesystem scalar/array union rejects ambiguity and invalid paths before remote transport', async t => {
  const {containerFilesystemObserver}=await import('./container-observations.js');
  const {FilesystemOutput}=await import('./filesystem.js');
  const h=await setup(t); let calls=0;
  const stamp={path:'/tmp',device:12,inode:34};
  h.fixture.roots.set('fixture-temporary',{...stamp,remoteObserve:containerFilesystemObserver(async request=>{
    calls++; assert.equal(request.operation,'filesystem-observe');
    if(request.operation!=='filesystem-observe')throw new Error('Wrong fixed request');
    assert.deepEqual(request.relativePaths,['safe-run']);
    return {root:stamp,data:{entries:[{relativePath:'safe-run',exists:false,kind:'missing',bytes:null,sha256:null,contentBase64:null}]}};
  },{kind:'fixture-temporary'},stamp)});
  const fields={leaseId:'lease',rootId:'fixture-temporary',view:'presence',maxBytes:100,maxEntries:1};
  for(const paths of [{relativePath:'safe-run'},{relativePaths:['safe-run']}]) {
    const result=await h.invoke('loom.filesystem.observe',{...fields,...paths});
    assert.equal(result.availability,'observed'); assert.equal(FilesystemOutput.parse(result.data).entries[0]!.exists,false);
  }
  assert.equal(calls,2);
  for(const paths of [{},{relativePath:'safe-run',relativePaths:['safe-run']},{relativePaths:[]},
    {relativePath:''},{relativePath:'../foreign'},{relativePaths:['../foreign']},{relativePath:'/tmp/foreign'},
    {relativePath:'safe-run',command:'stat'}]) {
    await assert.rejects(h.invoke('loom.filesystem.observe',{...fields,...paths})); assert.equal(calls,2);
  }
  const foreign=await h.invoke('loom.filesystem.observe',{...fields,rootId:'foreign',relativePath:'safe-run'});
  assert.equal(foreign.availability,'error'); assert.equal(calls,2);
});

test('canonical bind resolves a later native actor to the exact second repository before observation',async t=>{
  const {createOwnedWorkspaceRoster}=await import('./workspaces.js');const {fixtureOwnerIdentity}=await import('./authority.js');
  const h=await setup(t),owner=fixtureOwnerIdentity(h.fixture),original=h.fixture.agents.get('agt_owned')!.row;
  const fields={identityKind:'native-agent-id' as const,workspaceId:'workspace',repo:original.repo,commonDir:'/owned/source/.git',storeId:'store',storeGeneration:'generation',
    agentIds:[],agentSources:[],repositories:[{repoName:'alpha',sourceRepoId:'alpha',repo:original.repo,commonDir:'/owned/source/.git',groups:[]},
      {repoName:'beta',sourceRepoId:'beta',repo:'/owned/beta',commonDir:'/owned/beta/.git',groups:[]}]};
  const creationReceipt=await h.evidenceStore.retain(JSON.stringify({kind:'workspace-created',...owner,...fields}));
  h.fixture.ownedWorkspaces=await createOwnedWorkspaceRoster(h.fixture,[{...fields,creationReceipt}],h.evidenceStore);
  h.fixture.agents.clear();let foreign=true,resolves=0;
  h.fixture.readWorkspaceAgent=async(workspaceId,agentId)=>({kind:'agent-enrolled',identityKind:'native-agent-id',...owner,workspaceId,agentId,
    repo:foreign?'/foreign/beta':'/owned/beta',commonDir:'/owned/beta/.git',storeId:'store',storeGeneration:'generation',
    parentAgentId:null,rootAgentId:null,createdByKind:'user',createdById:'actual-user',revision:1});
  h.fixture.resolveAgent=async(agentId,_signal,workspaceId)=>{resolves++;assert.equal(workspaceId,'workspace');return {row:AgentRow.parse({...original,agent_id:agentId,repo:'/owned/beta'}),commonDir:'/owned/beta/.git'};};
  const request={leaseId:'lease',workspaceId:'workspace',agentId:'agt_beta'};
  const denied=await h.invoke('loom.agent.bind',request);assert.equal(denied.availability,'error');assert.equal(resolves,0);
  foreign=false;const bound=await h.invoke('loom.agent.bind',request);assert.equal(bound.availability,'observed');
  assert.equal(z.object({repo:z.string()}).parse(bound.data).repo,'/owned/beta');
  const observed=await h.invoke('loom.agent.observe',{agent:{fixtureLeaseId:'lease',workspaceId:'workspace',agentId:'agt_beta'}});
  assert.equal(observed.availability,'observed');assert.equal(observed.provenance.identity.repo,'/owned/beta');
  assert.equal(z.object({agentId:z.string()}).parse(observed.data).agentId,'agt_beta');
  assert.equal(resolves,2);
});

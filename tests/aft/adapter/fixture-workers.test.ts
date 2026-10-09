import assert from 'node:assert/strict';
import { test } from 'node:test';
import { mkdtemp, realpath, readdir, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin, revokeCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createFixtureOperationAuthority } from './authority.js';
import { createEvidenceStore, putEvidenceStore } from './evidence.js';
import { createCoreProviders } from './index.js';
import { putFixture, getFixtureAuthority, getFixture, type OwnedFixture } from './ownership.js';
import { FixtureWorkersId, FixtureWorkersEffects, FixtureWorkersOutput } from './fixture-workers.js';

async function setup(t: { after(fn: () => Promise<void>): void }, missingStart = false, replacedDuringVerification = false, changedGrant = false) {
  const root = fileURLToPath(new URL('.', import.meta.url));
  const files = (await readdir(root)).filter(file => file.endsWith('.ts') && !file.endsWith('.test.ts'));
  const pin = calculateImplementationPin(root, files, 'index.ts', 'createCoreProviders');
  const registry = new CapabilityRegistry(); let verifies = 0; let factories = 0;
  // Producer is an explicit unit double. This exercises canonical registry and
  // resource authority, not a Host worker read, API stop or process proof.
  for(const provider of createCoreProviders(pin))registry.register(provider);
  const context = createCapabilityContext({ file: 'workers-unit.test.yaml', line: 1 }, registry);
  const evidenceRoot = await realpath(await mkdtemp(path.join(os.tmpdir(), 'loom-worker-authority-')));
  t.after(() => rm(evidenceRoot, { recursive: true }));
  putEvidenceStore(context, await createEvidenceStore(evidenceRoot));
  const fixture: OwnedFixture = { leaseId: 'lease', runId: context.runId, caseId: context.caseId, suiteId: context.suiteId,
    scope: context.scope, profile: 'deterministic', evidenceClass: 'deterministic', workspaceId: 'workspace', repo: '/owned/source',
    expiresAtUtcMs: Date.now() + 100000, roots: new Map(), agents: new Map(), secrets: [],
    readApi: async () => { assert.fail('No discovery transport is used by the authority test'); },
    readFiles: async () => { assert.fail('No filesystem producer is used by the authority test'); },
    resolveAgent: async () => { assert.fail('No actor lookup is used by the authority test'); },
    verify: async () => {
      verifies++;
      if (replacedDuringVerification) context.resources.set('@loom/aft-adapter/fixtures/v1:lease', { ...fixture });
      if (changedGrant) fixture.operationAuthority = createFixtureOperationAuthority(fixture, {
        [FixtureWorkersId]: { evidenceClass: 'deterministic', effects: [...FixtureWorkersEffects] } });
    },
    dispose: async () => {} };
  fixture.observeWorkers=async()=>{
    factories++;
    return {fixtureLeaseId:fixture.leaseId,coverage:'registered-builtin-running-workers',
      serve:{id:'serve',pid:23,generation:'owned-serve',state:'running'},
      workers:[{id:'retained-worker',generation:'owned-worker',kind:'worker',identityKind:'legacy-agent-name',
        workspaceId:fixture.workspaceId,agentId:'owned-name',sessionName:null}]};
  };
  fixture.operationAuthority = createFixtureOperationAuthority(fixture, { [FixtureWorkersId]: {
    evidenceClass: 'deterministic', effects: missingStart ? ['read-api', 'read-filesystem'] : [...FixtureWorkersEffects] } });
  putFixture(context, fixture);
  const invoke = (input: unknown = { leaseId: 'lease' }) => registry.invoke({ id: FixtureWorkersId, version: 1, input: {} }, input, context);
  return { fixture, context, invoke, counts: () => ({ verifies, factories }) };
}

test('worker registry effect grant admits the explicit producer only after canonical fixture verification', async t => {
  const h = await setup(t); const result = await h.invoke();
  assert.equal(result.availability, 'observed');
  assert.deepEqual(h.counts(), { verifies: 1, factories: 1 });
  const facts = FixtureWorkersOutput.parse(result.data);
  assert.equal(facts.serve.generation, 'owned-serve'); assert.equal(facts.workers[0]!.generation, 'owned-worker');
  await getFixture(h.context, 'lease'); assert.equal(h.counts().verifies, 2);
});
test('missing process-start grant denies before fixture verification or worker factory', async t => {
  const h = await setup(t, true); assert.equal((await h.invoke()).availability, 'unsupported');
  assert.deepEqual(h.counts(), { verifies: 0, factories: 0 });
});
test('foreign lease and malformed input cannot reach verification or discovery', async t => {
  const h = await setup(t); assert.equal((await h.invoke({ leaseId: 'foreign' })).availability, 'error');
  await assert.rejects(h.invoke({ leaseId: 'lease', executable: 'arbitrary' }));
  assert.deepEqual(h.counts(), { verifies: 0, factories: 0 });
});
test('cloned and revoked canonical contexts cannot use authority-only fixture access', async t => {
  const h = await setup(t); let suiteReads = 0;
  const copied = { ...h.context, resources: new Map(), suite: { id: h.context.suiteId, handles: ['lease'],
    getResource: () => { suiteReads++; return h.fixture; } } };
  assert.throws(() => getFixtureAuthority(copied as typeof h.context, 'lease'), /missing or revoked/);
  assert.equal(suiteReads, 0);
  revokeCapabilityContext(h.context); assert.throws(() => getFixtureAuthority(h.context, 'lease'), /missing or revoked/);
  assert.deepEqual(h.counts(), { verifies: 0, factories: 0 });
});
test('replacement during awaited verification denies the worker factory', async t => {
  const h = await setup(t, false, true); assert.equal((await h.invoke()).availability, 'error');
  assert.deepEqual(h.counts(), { verifies: 1, factories: 0 });
});
test('replacement of the trusted operation grant during verification denies the worker factory', async t => {
  const h = await setup(t, false, false, true); assert.equal((await h.invoke()).availability, 'error');
  assert.deepEqual(h.counts(), { verifies: 1, factories: 0 });
});
test('default worker provider rejects an absent private route before verification',async t=>{
  const h=await setup(t);delete h.fixture.observeWorkers;
  assert.equal((await h.invoke()).availability,'unsupported');
  assert.deepEqual(h.counts(),{verifies:0,factories:0});
});
test('default worker provider refuses callback replacement during verification',async t=>{
  const h=await setup(t);const original=h.fixture.verify;
  h.fixture.verify=async signal=>{await original(signal);h.fixture.observeWorkers=async()=>{assert.fail('Replacement cannot run');};};
  assert.equal((await h.invoke()).availability,'error');
  assert.deepEqual(h.counts(),{verifies:1,factories:0});
});
test('default worker provider refuses grant replacement during observation without crediting output',async t=>{
  const h=await setup(t);const original=h.fixture.observeWorkers!;
  h.fixture.observeWorkers=async signal=>{
    const value=await original(signal);
    h.fixture.operationAuthority=createFixtureOperationAuthority(h.fixture,{[FixtureWorkersId]:{evidenceClass:'deterministic',effects:[...FixtureWorkersEffects]}});
    return value;
  };
  const result=await h.invoke();assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.deepEqual(h.counts(),{verifies:1,factories:1});
});
test('default worker provider refuses foreign receipt identity and malformed rows',async t=>{
  const h=await setup(t);const original=h.fixture.observeWorkers!;
  h.fixture.observeWorkers=async signal=>({...await original(signal),fixtureLeaseId:'foreign'});
  const foreign=await h.invoke();assert.equal(foreign.availability,'error');assert.equal(foreign.data,undefined);
  h.fixture.observeWorkers=async signal=>{const value=await original(signal);return {...value,workers:[...value.workers,...value.workers]};};
  const malformed=await h.invoke();assert.equal(malformed.availability,'error');assert.equal(malformed.data,undefined);
});
test('default worker provider checks the grant before accessing a private producer getter',async t=>{
  const h=await setup(t,true);let getters=0;
  Object.defineProperty(h.fixture,'observeWorkers',{get(){getters++;assert.fail('Ungrantable producer cannot be accessed');}});
  assert.equal((await h.invoke()).availability,'unsupported');assert.equal(getters,0);
  assert.deepEqual(h.counts(),{verifies:0,factories:0});
});
test('default worker provider refuses callback replacement after its awaited observation',async t=>{
  const h=await setup(t);const original=h.fixture.observeWorkers!;
  h.fixture.observeWorkers=async signal=>{const value=await original(signal);h.fixture.observeWorkers=original;return value;};
  const result=await h.invoke();assert.equal(result.availability,'error');assert.equal(result.data,undefined);
  assert.deepEqual(h.counts(),{verifies:1,factories:1});
});

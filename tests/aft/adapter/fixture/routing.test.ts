import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fixtureRouting, fixtureOperationAuthority } from './routing.js';
import { getFixtureOperationAuthority } from '../authority.js';
import type { OwnedFixture } from '../ownership.js';
import type { FixturePlan } from './lifecycle.js';
import { LegacyOperationEffects, legacyTaskEffects } from '../legacy/effects.js';
const owner={leaseId:'lease',runId:'run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:'legacy-real-codex'};
const revision={repository:'test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)};
const plan:FixturePlan={profile:owner.profile,loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,model:'openai/model',maxCases:1,caseCount:1,selectionSha256:'e'.repeat(64),leaseDurationMs:10000};
test('real native fixture observations do not grant paid task execution',()=>{
 const fixture={...owner,operationAuthority:fixtureOperationAuthority(owner,plan)} as OwnedFixture;
 assert.equal(getFixtureOperationAuthority(fixture,'loom.cli.role',['read-api']).evidenceClass,'real-native');
 assert.throws(()=>getFixtureOperationAuthority(fixture,'loom.cli.task',['start-owned-process','external-provider']));
});
test('exact trusted backend/model policy grants only operation-scoped external execution',()=>{
 const fixture={...owner,operationAuthority:fixtureOperationAuthority(owner,{...plan,liveProvider:{backend:'codex',model:plan.model}})} as OwnedFixture;
 assert.equal(getFixtureOperationAuthority(fixture,'loom.cli.task',['start-owned-process','external-provider']).evidenceClass,'live-provider');
 assert.throws(()=>getFixtureOperationAuthority({...fixture,leaseId:'foreign'},'loom.cli.task',['start-owned-process']));
 assert.equal(getFixtureOperationAuthority(fixture,'loom.cli.usage',['read-api']).evidenceClass,'real-native');
});
for(const policy of [{backend:'claude' as const,model:plan.model},{backend:'codex' as const,model:'foreign/model'}])test('backend/model policy mismatch is refused before a grant exists '+policy.backend+policy.model,()=>{
 assert.throws(()=>fixtureRouting({...plan,liveProvider:policy}));
});
test('deterministic backend routing rejects paid authority and exposes only the supported closed backend set',()=>{
 const stub={...plan,profile:'legacy-deterministic',model:'aft/m'};assert.throws(()=>fixtureRouting({...stub,liveProvider:{backend:'opencode',model:'aft/m'}}));
 const fixture={...owner,profile:stub.profile,operationAuthority:fixtureOperationAuthority({...owner,profile:stub.profile},stub)} as OwnedFixture;
 assert.equal(getFixtureOperationAuthority(fixture,'loom.cli.task',['start-owned-process']).evidenceClass,'deterministic');
 assert.throws(()=>getFixtureOperationAuthority(fixture,'loom.cli.task',['external-provider']));
 assert.deepEqual(fixtureRouting(stub).allowedTaskBackends,['codex','claude','cursor','opencode']);
});
test('fixture grants preserve the shared API/filesystem/CLI effect contract for every fixed legacy operation',()=>{
 const stub={...plan,profile:'legacy-deterministic',model:'aft/m'};
 const fixture={...owner,profile:stub.profile,operationAuthority:fixtureOperationAuthority({...owner,profile:stub.profile},stub)} as OwnedFixture;
 for(const [operation,effects] of Object.entries(LegacyOperationEffects))assert.equal(getFixtureOperationAuthority(fixture,operation as keyof typeof LegacyOperationEffects,effects).evidenceClass,'deterministic');
 assert.equal(getFixtureOperationAuthority(fixture,'loom.runtime.stimulate',[...LegacyOperationEffects['loom.runtime.stimulate'],'restart-owned-service']).evidenceClass,'deterministic');
 const paid={...owner,operationAuthority:fixtureOperationAuthority(owner,{...plan,liveProvider:{backend:'codex',model:plan.model}})} as OwnedFixture;
 assert.equal(getFixtureOperationAuthority(paid,'loom.cli.task',legacyTaskEffects({taskExecution:'live-provider'})).evidenceClass,'live-provider');
 assert.throws(()=>getFixtureOperationAuthority(fixture,'loom.cli.task',legacyTaskEffects({taskExecution:'live-provider'})));
});
test('Cursor exposes backend-default selection; a named model cannot be attested by its source backend',()=>{
 assert.throws(()=>fixtureRouting({...plan,profile:'legacy-real-cursor'}));
 assert.deepEqual(fixtureRouting({...plan,profile:'legacy-real-cursor',model:'backend-default'}).modelSelection,{kind:'backend-default'});
 assert.deepEqual(fixtureRouting(plan).modelSelection,{kind:'exact-model',model:plan.model,selector:'agent-env'});
 assert.deepEqual(fixtureRouting({...plan,profile:'legacy-real-opencode'}).modelSelection,{kind:'exact-model',model:plan.model,selector:'opencode-env'});
});

test('worker discovery grants use the shared helper effects only for owned Host profiles',()=>{
 const effects=['read-api','read-filesystem','start-owned-process'] as const;
 for(const profile of ['legacy-deterministic','legacy-real-codex','legacy-real-claude','legacy-real-opencode','legacy-real-cursor']){
  const owned={...owner,profile},configured={...plan,profile,model:profile==='legacy-real-cursor'?'backend-default':plan.model};
  const fixture={...owned,operationAuthority:fixtureOperationAuthority(owned,configured)} as OwnedFixture;
  assert.deepEqual(getFixtureOperationAuthority(fixture,'loom.fixture.observeWorkers',effects).effects,effects);
  assert.throws(()=>getFixtureOperationAuthority(fixture,'loom.fixture.observeWorkers',['external-provider']));
 }
 for(const profile of ['agents-real-opencode','agents-emulator','legacy-real-codex-podman']){
  const owned={...owner,profile},fixture={...owned,operationAuthority:fixtureOperationAuthority(owned,{...plan,profile})} as OwnedFixture;
  assert.throws(()=>getFixtureOperationAuthority(fixture,'loom.fixture.observeWorkers',effects));
 }
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { fixtureRouting, fixtureOperationAuthority } from './routing.js';
import { getFixtureOperationAuthority } from '../authority.js';
import type { OwnedFixture } from '../ownership.js';
import type { FixturePlan } from './lifecycle.js';
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

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createFixtureOperationAuthority, getFixtureOperationAuthority, type FixtureAuthorityOwner } from './authority.js';
import type { OwnedFixture } from './ownership.js';

const owner: FixtureAuthorityOwner = {leaseId:'lease',runId:'run',suiteId:'suite',scope:'suite',caseId:'setup',profile:'legacy-real-codex'};
const fixture = (extra: Partial<OwnedFixture> = {}) => ({...owner,...extra}) as OwnedFixture;
test('trusted operation grants preserve mixed evidence classes and deny effects before any launch', () => {
  const operationAuthority = createFixtureOperationAuthority(owner,{
    'loom.cli.role':{evidenceClass:'real-native',effects:['read-api','start-owned-process']},
    'loom.cli.task':{evidenceClass:'live-provider',effects:['start-owned-process','external-provider']},
  });
  const owned = fixture({operationAuthority}); let effects = 0;
  const launch = (required: ('start-owned-process'|'external-provider'|'write-fixture')[]) => {
    const grant = getFixtureOperationAuthority(owned,'loom.cli.task',required); effects++; return grant;
  };
  assert.equal(getFixtureOperationAuthority(owned,'loom.cli.role',['read-api']).evidenceClass,'real-native');
  assert.equal(launch(['start-owned-process','external-provider']).evidenceClass,'live-provider');
  assert.throws(()=>launch(['write-fixture']),/not authorized/); assert.equal(effects,1);
  assert.throws(()=>getFixtureOperationAuthority(owned,'loom.fixture.configure',['write-fixture']),/not authorized/);
  assert.ok(Object.isFrozen(operationAuthority)); assert.ok(Object.isFrozen(operationAuthority['loom.cli.task']!.effects));
});
test('missing, forged and cross-fixture/run/scope authorities fail closed', () => {
  assert.throws(()=>getFixtureOperationAuthority(fixture(),'loom.cli.role',['read-api']),/no operation authority/);
  assert.throws(()=>getFixtureOperationAuthority(fixture({operationAuthority:{'loom.cli.role':{evidenceClass:'real-native',effects:['read-api']}}}),
    'loom.cli.role',['read-api']),/trusted owner/);
  const operationAuthority = createFixtureOperationAuthority(owner,{'loom.cli.role':{evidenceClass:'real-native',effects:['read-api']}});
  for (const different of [{runId:'other'},{leaseId:'other'},{suiteId:'other'},{profile:'other'},{scope:'case' as const},{caseId:'other'}])
    assert.throws(()=>getFixtureOperationAuthority(fixture({operationAuthority,...different}),'loom.cli.role',['read-api']),/trusted owner/);
  assert.throws(()=>createFixtureOperationAuthority(owner,{'loom.cli.role':{evidenceClass:'real-native',effects:['read-api','read-api']}}));
  assert.throws(()=>createFixtureOperationAuthority(owner,{'loom.cli.role':{evidenceClass:'real' as 'real-native',effects:['read-api']}}));
});

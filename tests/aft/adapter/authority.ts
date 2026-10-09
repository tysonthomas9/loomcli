import { z } from 'zod';
import { CAPABILITY_EFFECTS, EVIDENCE_CLASSES, type CapabilityEffect } from '@tysonthomas9/aft/types';
import { requireFact } from './protocol.js';
import type { OwnedFixture } from './ownership.js';

export const LoomAuthorizedOperation = z.enum(['loom.cli.role','loom.cli.usage','loom.cli.task',
  'loom.runtime.stimulate','loom.runtime.detachTerminal','loom.fixture.seedWorktree','loom.fixture.configure']);
export type LoomAuthorizedOperation = z.infer<typeof LoomAuthorizedOperation>;
const Grant = z.object({ evidenceClass:z.enum(EVIDENCE_CLASSES), effects:z.array(z.enum(CAPABILITY_EFFECTS)).min(1)
  .refine(values=>new Set(values).size===values.length,'Duplicate operation effects') }).strict();
const Grants = z.record(LoomAuthorizedOperation,Grant);
export type FixtureOperationAuthority = Readonly<Partial<Record<LoomAuthorizedOperation,
  {readonly evidenceClass:z.infer<typeof Grant>['evidenceClass']; readonly effects:readonly CapabilityEffect[]}>>>;
export type FixtureAuthorityOwner = Pick<OwnedFixture,'leaseId'|'runId'|'suiteId'|'scope'|'caseId'|'profile'>;
export function fixtureOwnerIdentity(owner:FixtureAuthorityOwner):FixtureAuthorityOwner {
  return {leaseId:owner.leaseId,runId:owner.runId,suiteId:owner.suiteId,scope:owner.scope,caseId:owner.caseId,profile:owner.profile};
}
const generated = new WeakMap<object,FixtureAuthorityOwner>();
/** Called only by a trusted fixture owner after exact profile/backend/model
 * preflight. An omitted grant denies the operation; no YAML authority exists. */
export function createFixtureOperationAuthority(owner: FixtureAuthorityOwner, grants: z.input<typeof Grants>): FixtureOperationAuthority {
  const parsed = Grants.parse(grants);
  const authority = Object.freeze(Object.fromEntries(Object.entries(parsed).map(([operation,grant])=>
    [operation,Object.freeze({evidenceClass:grant.evidenceClass,effects:Object.freeze([...grant.effects])})])));
  generated.set(authority,Object.freeze(fixtureOwnerIdentity(owner))); return authority;
}
export function validateFixtureOperationAuthority(authority: FixtureOperationAuthority, fixture: FixtureAuthorityOwner): void {
  const owner = generated.get(authority);
  requireFact(owner && Object.entries(owner).every(([key,value])=>fixture[key as keyof FixtureAuthorityOwner]===value),'ownership-mismatch','Fixture operation authority was not created by its trusted owner');
}
/** Providers call this before any transport factory, process launch or mutation.
 * The receipt must subsequently preserve this exact operation evidence class. */
export function getFixtureOperationAuthority(fixture: OwnedFixture, operation: LoomAuthorizedOperation,
  requiredEffects: readonly CapabilityEffect[]) {
  const authority = fixture.operationAuthority;
  requireFact(authority,'unsupported-capability','Fixture has no operation authority'); validateFixtureOperationAuthority(authority,fixture);
  const grant = authority[LoomAuthorizedOperation.parse(operation)];
  requireFact(grant && requiredEffects.every(effect=>grant.effects.includes(effect)),
    'unsupported-capability','Operation evidence or effects were not authorized by the fixture owner');
  return grant;
}

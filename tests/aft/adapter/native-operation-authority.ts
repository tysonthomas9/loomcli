import { createHash } from 'node:crypto';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ArtifactRefSchema, JsonValueSchema, type CapabilityEffect } from '@tysonthomas9/aft/types';
import { getFixtureOperationAuthority, fixtureOwnerIdentity } from './authority.js';
import { getFixtureEvidenceStore } from './evidence.js';
import { NativeOperationEffects, type NativeAuthorizedOperation } from './native-operation-effects.js';
import { getFixtureAuthority } from './ownership.js';
import { requireFact } from './protocol.js';

/** Private invocation guard, not a grant or a new ownership registry. Calling
 * this performs no fixture verification, query, process launch or file write.
 * The supported owner must already have issued the exact operation grant. */
export function beginNativeOperation(context: CapabilityContext, leaseId: string, operation: NativeAuthorizedOperation) {
  requireFact(Object.hasOwn(NativeOperationEffects, operation), 'unsupported-capability', 'Unknown native operation');
  return beginFixtureOperation(context,leaseId,operation,NativeOperationEffects[operation],4_000_000);
}

/** The same private guard for the three explicitly effectful worker/Compose
 * contracts. Required effects come from their code-owned canonical maps; this
 * function issues no grant and changes no unrelated operation descriptor. */
export function beginFixtureOperation(context:CapabilityContext,leaseId:string,
  operation:NativeAuthorizedOperation|'loom.fixture.observeWorkerState'|'loom.fixture.observeComposeServe'|'loom.runtime.restartComposeServe',
  effects:readonly CapabilityEffect[],maximumBytes:4_000_000|4194304=4194304) {
  const fixture = getFixtureAuthority(context, leaseId);
  const authority = fixture.operationAuthority;
  const grant = getFixtureOperationAuthority(fixture, operation, effects);
  const store = getFixtureEvidenceStore(context, leaseId);
  const owner = fixtureOwnerIdentity(fixture);
  const expiresAt = fixture.expiresAtUtcMs;
  const workspaceId = fixture.workspaceId, repo = fixture.repo;
  const signal = context.signal;
  const source = {...context.source}, registrySha256 = context.registrySha256;
  const callbacks = {
    verify: fixture.verify, resolveAgent: fixture.resolveAgent,
    readWorkspaceAgent: fixture.readWorkspaceAgent,
    retain: store.retain, resolve: store.resolve, resolveBounded: store.resolveBounded,
  };
  const roots = fixture.roots, agents = fixture.agents;
  const secrets = fixture.secrets, secretValues = [...secrets];
  const recheck = () => {
    signal.throwIfAborted();
    requireFact(context.signal === signal && context.registrySha256 === registrySha256 &&
      Object.keys(context.source).length === Object.keys(source).length &&
      Object.entries(source).every(([key, value]) => context.source[key as keyof typeof source] === value) &&
      getFixtureAuthority(context, leaseId) === fixture &&
      Object.entries(owner).every(([key, value]) => fixture[key as keyof typeof owner] === value) &&
      fixture.expiresAtUtcMs === expiresAt && fixture.workspaceId === workspaceId && fixture.repo === repo && fixture.operationAuthority === authority &&
      getFixtureOperationAuthority(fixture, operation, effects) === grant &&
      getFixtureEvidenceStore(context, leaseId) === store &&
      fixture.verify === callbacks.verify && fixture.resolveAgent === callbacks.resolveAgent &&
      fixture.readWorkspaceAgent === callbacks.readWorkspaceAgent &&
      fixture.roots === roots && fixture.agents === agents &&
      fixture.secrets === secrets && secrets.length === secretValues.length && secretValues.every((value,index)=>secrets[index]===value) &&
      store.retain === callbacks.retain && store.resolve === callbacks.resolve && store.resolveBounded === callbacks.resolveBounded,
    'ownership-mismatch', 'Owned operation ownership changed');
  };
  const checked = async <T>(call: () => Promise<T>): Promise<T> => {
    recheck();
    const value = await call();
    recheck();
    return value;
  };
  recheck();
  return Object.freeze({
    fixture, grant, recheck, checked,
    verify: () => checked(() => callbacks.verify.call(fixture, signal)),
    async retain(serialized: string) {
      recheck();
      // Preserve the existing small canonical output admission before allocating
      // an owned artifact. Callers redact first; this guard does not sanitize.
      // The lease-bound store is the only target.
      const bytes = Buffer.byteLength(serialized);
      requireFact(bytes <= maximumBytes, 'incomplete-pages', 'Owned evidence exceeds the canonical byte bound');
      JsonValueSchema.parse(JSON.parse(serialized));
      const digest = createHash('sha256').update(serialized).digest('hex');
      const receipt = ArtifactRefSchema.parse(await checked(() => callbacks.retain.call(store, serialized)));
      requireFact(receipt.bytes === bytes && receipt.sha256 === digest && receipt.mediaType === 'application/json' &&
        receipt.redaction === 'sanitized', 'identity-mismatch', 'Owned evidence receipt does not match the retained output');
      recheck();
      return receipt;
    },
  });
}

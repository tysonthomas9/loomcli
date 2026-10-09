import { z } from 'zod';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { CapabilityEffect } from '@tysonthomas9/aft/types';
import { Id, requireFact } from './protocol.js';
import { getFixtureAuthority } from './ownership.js';
import { getFixtureOperationAuthority } from './authority.js';

export const FixtureWorkersId = 'loom.fixture.observeWorkers' as const;
/** Fixed API/Git/kernel registration reads may start an owned helper, but
 * discovery itself sends no process signal or restart request. */
export const FixtureWorkersEffects = Object.freeze(['read-api', 'read-filesystem', 'start-owned-process'] as const satisfies readonly CapabilityEffect[]);
export const FixtureWorkersInput = z.object({ leaseId: Id }).strict();
export const RegisteredWorkerFact = z.object({ id: Id, generation: Id, kind: z.literal('worker'),
  identityKind: z.literal('legacy-agent-name'), workspaceId: Id, agentId: Id, sessionName: z.null() }).strict();
export const FixtureWorkersOutput = z.object({ fixtureLeaseId: Id,
  coverage: z.literal('registered-builtin-running-workers'),
  serve: z.object({ id: z.literal('serve'), pid: z.number().int().positive(), generation: Id, state: z.literal('running') }).strict(),
  workers: z.array(RegisteredWorkerFact).max(1000).refine(rows => new Set(rows.map(row => row.id)).size === rows.length,
    'Duplicate worker registration') }).strict();

/** Gate the fixture-owned producer before its verification/factory/discovery.
 * Returned facts still need exact actor/store/serve validation by that producer. */
export async function authorizeFixtureWorkers(context: CapabilityContext, input: z.infer<typeof FixtureWorkersInput>) {
  const fixture = getFixtureAuthority(context, input.leaseId);
  const grant = getFixtureOperationAuthority(fixture, FixtureWorkersId, FixtureWorkersEffects);
  await fixture.verify(context.signal);
  requireFact(getFixtureAuthority(context, input.leaseId) === fixture, 'ownership-mismatch', 'Worker fixture changed during verification');
  requireFact(getFixtureOperationAuthority(fixture, FixtureWorkersId, FixtureWorkersEffects) === grant,
    'ownership-mismatch', 'Worker operation authority changed during verification');
  return { fixture, grant };
}

import type { CapabilityEffect } from '@tysonthomas9/aft/types';
import type { LoomAuthorizedOperation } from '../authority.js';

export const TerminalDetachEffects = Object.freeze(['read-api', 'read-filesystem', 'stop-owned-process'] as const satisfies readonly CapabilityEffect[]);

// The host binding reads owned legacy API facts. Role/usage/seed/task also
// launch their original CLI actor. Role discovery is lazy and absent from
// configure; runtime ownership discovery starts fixed Git/kernel helpers and
// serve restart starts its replacement. Paid task discovery is covered by the
// task process effect.
export const LegacyOperationEffects: Readonly<Record<Exclude<LoomAuthorizedOperation, 'loom.runtime.detachTerminal' | 'loom.fixture.observeWorkers' | 'loom.fixture.observeWorkerState'>, readonly CapabilityEffect[]>> = Object.freeze({
  'loom.cli.role': Object.freeze(['read-api', 'read-filesystem', 'start-owned-process'] as const),
  'loom.cli.usage': Object.freeze(['read-api', 'read-filesystem', 'start-owned-process'] as const),
  'loom.cli.task': Object.freeze(['read-api', 'read-filesystem', 'start-owned-process'] as const),
  'loom.runtime.stimulate': Object.freeze(['read-api', 'read-filesystem', 'start-owned-process', 'stop-owned-process'] as const),
  'loom.fixture.seedWorktree': Object.freeze(['read-api', 'read-filesystem', 'write-fixture', 'start-owned-process'] as const),
  'loom.fixture.configure': Object.freeze(['read-api', 'read-filesystem', 'write-fixture'] as const),
});
export interface LegacyProviderOptions { readonly taskExecution: 'deterministic' | 'live-provider' }
export function legacyTaskEffects(options: LegacyProviderOptions): readonly CapabilityEffect[] {
  return options.taskExecution === 'live-provider' ? [...LegacyOperationEffects['loom.cli.task'], 'external-provider'] : LegacyOperationEffects['loom.cli.task'];
}

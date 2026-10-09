import type { CapabilityEffect } from '@tysonthomas9/aft/types';

/** Fixed public ceilings for the supported verification/retention path.
 * Bind's read-api ceiling does not imply a GET or grant native HTTP access.
 * stop-owned-process covers exact helper overflow/abort termination, not service teardown.
 * Owners grant only an actually supported route; descriptors alone grant nothing. */
export const NativeOperationEffects = Object.freeze({
  'loom.agent.bind': Object.freeze(['read-api', 'read-filesystem', 'write-fixture', 'start-owned-process', 'stop-owned-process'] as const),
  'loom.native.registration': Object.freeze(['read-native', 'read-filesystem', 'write-fixture', 'start-owned-process', 'stop-owned-process'] as const),
  'loom.native.observe': Object.freeze(['read-native', 'read-filesystem', 'write-fixture', 'start-owned-process', 'stop-owned-process'] as const),
} satisfies Record<string, readonly CapabilityEffect[]>);
export type NativeAuthorizedOperation = keyof typeof NativeOperationEffects;

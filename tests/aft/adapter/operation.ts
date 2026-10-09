import { z } from 'zod';
import type { CapabilityContext, CapabilityProvider, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import { JsonValueSchema, type CapabilityEffect, type EvidenceClass, type ObservationResult } from '@tysonthomas9/aft/types';
import { retainEvidence } from './evidence.js';
import type { EvidenceStore } from './evidence.js';
import { ObservationError, redact, type Json } from './protocol.js';

export interface OperationOptions<I, O> {
  id: string;
  implementationSha256: string;
  implementation: ImplementationPin;
  inputSchema: z.ZodType<I, z.ZodTypeDef, unknown>;
  outputSchema: z.ZodType<O>;
  effects: CapabilityEffect[];
  retry: CapabilityProvider['retry'];
  cleanup: CapabilityProvider['cleanup'];
  evidenceClasses: EvidenceClass[];
  run(input: I, context: CapabilityContext): Promise<{ value: O; evidenceClass: EvidenceClass;
    identity?: Omit<ObservationResult['provenance']['identity'], 'runId'>; secrets?: readonly string[];
    /** Code-owned invocation continuity and lease-bound retention. Never DATA. */
    retention?: { recheck(): void; retain: EvidenceStore['retain']; onFailure?(): void } }>;
  dispose?(context: CapabilityContext): Promise<void>;
}
/** One canonical envelope; only operation-specific success data lives here. */
export function defineOperation<I, O>(options: OperationOptions<I, O>): CapabilityProvider {
  return { id: options.id, version: 1, implementationSha256: options.implementationSha256, implementation: options.implementation,
    inputSchema: options.inputSchema, outputSchema: options.outputSchema, effects: options.effects,
    retry: options.retry, cleanup: options.cleanup, evidenceClasses: options.evidenceClasses, dispose: options.dispose,
    async execute(input, context) {
      let value: Json | undefined;
      let identity: Omit<ObservationResult['provenance']['identity'], 'runId'> = {};
      let evidenceClass = options.evidenceClasses[0]!;
      let error: ObservationResult['error'];
      const artifacts: ObservationResult['provenance']['artifacts'] = [];
      let retention: Awaited<ReturnType<typeof options.run>>['retention'];
      try {
        const parsed = options.inputSchema.parse(input);
        const observed = await options.run(parsed, context);
        retention = observed.retention;
        retention?.recheck();
        identity = observed.identity ?? {}; evidenceClass = observed.evidenceClass;
        const output = options.outputSchema.parse(observed.value);
        value = redact(JsonValueSchema.parse(output), observed.secrets);
        options.outputSchema.parse(value);
        retention?.recheck();
        const serialized = JSON.stringify(value);
        const artifact = await (retention ? retention.retain(serialized) : retainEvidence(context, serialized, identity.fixtureLeaseId));
        retention?.recheck();
        artifacts.push(artifact);
      } catch (cause) {
        retention?.onFailure?.();
        error = { code: cause instanceof ObservationError ? cause.code : context.signal.aborted ? 'deadline-exceeded' : 'observation-failed',
          message: cause instanceof ObservationError ? cause.message : 'Owned adapter observation failed' };
      }
      const monoMs = context.clock.now();
      const provenance: ObservationResult['provenance'] = { source: context.source, registrySha256: context.registrySha256,
        implementationSha256: options.implementationSha256, identity: { runId: context.runId, ...identity },
        observedAt: { clockId: context.clock.id, monoMs, utcMs: context.clock.epochUtcMs + monoMs, phase: 0 }, evidenceClass, artifacts };
      if (error) return { availability: error.code === 'unsupported-capability' ? 'unsupported' :
        error.code === 'incomplete-pages' ? 'incomplete' : 'error', provenance, error };
      return { availability: 'observed', provenance, data: value! };
    },
  };
}

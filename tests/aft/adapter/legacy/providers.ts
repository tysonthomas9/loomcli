import { z } from 'zod';
import type { CapabilityContext, CapabilityProvider, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { getFixtureOperationAuthority } from '../authority.js';
import { RedactionFacts, redactionFacts } from '../redaction.js';
import { defineOperation } from '../operation.js';
import { getFixture, disposeFixtures, type OwnedFixture } from '../ownership.js';
import { Id, Json, HttpResponse, ObservationError } from '../protocol.js';
import { requireOwnedWorkspace, requireOwnedWorkspaceRecord, enrollOwnedLegacyAgent } from '../workspaces.js';
import { getFixtureEvidenceStore } from '../evidence.js';
import { LegacyOperationEffects, legacyTaskEffects, type LegacyProviderOptions } from './effects.js';
export { LegacyOperationEffects, legacyTaskEffects, type LegacyProviderOptions } from './effects.js';
import { createLegacyOperations, LegacyError, LegacyEvidenceClasses, RuntimeInput, RoleInput, UsageInput, TaskInput, SeedInput, ConfigureInput,
  type LegacyAccess, type Invocation } from './operations.js';

const Receipt = z.object({ operation: Id, leaseId: Id, runId: Id, invocationId: Id,
  evidence: z.enum(LegacyEvidenceClasses), actor: z.enum(['loom-cli', 'runtime', 'fixture']), facts: Json, factsRedaction: RedactionFacts }).strict();
export const RoleOutput = z.object({ exitCode: z.number().int().nonnegative(), body: Json, receipt: Receipt, redaction: RedactionFacts }).strict();
export const UsageOutput = RoleOutput;
export const RuntimeOutput = z.object({ beforeGeneration: Id, afterGeneration: Id.nullable(), affectedIds: z.array(Id),
  complete: z.literal(true), response: HttpResponse.nullable(), receipt: Receipt, redaction: RedactionFacts }).strict();
export const TaskOutput = z.object({ ownedProcessId: Id, generation: Id, exitCode: z.number().int().nonnegative().nullable(),
  stdout: z.string(), stderr: z.string(), complete: z.boolean(), receipt: Receipt, redaction: RedactionFacts }).strict();
export const SeedOutput = z.object({ commit: z.string().regex(/^[a-f0-9]{40}$/), receipt: Receipt, redaction: RedactionFacts }).strict();
export const ConfigureOutput = z.object({ previous: Json, response: HttpResponse.nullable(), receipt: Receipt, redaction: RedactionFacts }).strict();

const operationIds = { role: 'loom.cli.role', usage: 'loom.cli.usage', task: 'loom.cli.task', stimulate: 'loom.runtime.stimulate',
  seedWorktree: 'loom.fixture.seedWorktree', configure: 'loom.fixture.configure' } as const;
const preflightBackends: Readonly<Record<string, string>> = {
  'legacy-real-codex': 'codex', 'legacy-real-claude': 'claude', 'legacy-real-cursor': 'cursor',
  'legacy-real-opencode': 'opencode', 'legacy-real-codex-podman': 'codex',
};

// The reviewed launcher binds a private transport to the canonical fixture.
// Neither transports, binaries, environment, process IDs nor native identities
// can be supplied by YAML. Root registration remains with the adapter owner.
export type LegacyAccessFactory = (context: CapabilityContext, fixture: OwnedFixture) => LegacyAccess;
export function createLegacyProviders(implementation: ImplementationPin, implementationSha256: string,
  accessFactory: LegacyAccessFactory, options: LegacyProviderOptions = { taskExecution: 'deterministic' }): CapabilityProvider[] {
  const taskExecution = z.enum(['deterministic', 'live-provider']).parse(options.taskExecution);
  const taskEffects = legacyTaskEffects({ taskExecution });
  // This is an operation-state cache keyed by the canonical private fixture,
  // not a second ownership registry. Suite/case access always goes through getFixture.
  const stores = new WeakMap<OwnedFixture, { operations: ReturnType<typeof createLegacyOperations>; sequence: number }>();
  const common = { implementation, implementationSha256, retry: 'never' as const, evidenceClasses: [...LegacyEvidenceClasses] };
  async function invoke<K extends keyof ReturnType<typeof createLegacyOperations>>(method: K, input: unknown,
    context: CapabilityContext, leaseId: string, workspaceId?: string) {
    if (method === 'stimulate' && RuntimeInput.parse(input).operation === 'terminal-close')
      throw new ObservationError('unsupported-capability', 'Tab metadata deletion is a separate observation, not a process transition');
    const fixture = await getFixture(context, leaseId);
    const operation = operationIds[method];
    let grant = getFixtureOperationAuthority(fixture, operation, LegacyOperationEffects[operation]);
    if (method === 'stimulate' && RuntimeInput.parse(input).operation === 'serve-restart')
      grant = getFixtureOperationAuthority(fixture, operation, [...LegacyOperationEffects[operation], 'restart-owned-service']);
    if (!z.enum(LegacyEvidenceClasses).safeParse(grant.evidenceClass).success)
      throw new ObservationError('source-mismatch', 'Legacy operation evidence is unsupported');
    if (method === 'task') {
      const task = TaskInput.parse(input);
      if (grant.evidenceClass !== taskExecution)
        throw new ObservationError('source-mismatch', 'Task route differs from trusted composition descriptor');
      grant = getFixtureOperationAuthority(fixture, operation, taskEffects);
      const preflightBackend = preflightBackends[fixture.profile];
      if (preflightBackend && (task.backend !== preflightBackend || grant.evidenceClass !== 'live-provider'))
        throw new ObservationError('unsupported-capability', 'Task backend or external execution authority differs from preflight');
      if (fixture.profile === 'legacy-deterministic' && grant.evidenceClass !== 'deterministic')
        throw new ObservationError('source-mismatch', 'Stub task execution cannot be labeled as a provider run');
      if (grant.evidenceClass === 'live-provider') grant = getFixtureOperationAuthority(fixture, operation, ['start-owned-process', 'external-provider']);
    }
    if (workspaceId !== undefined) {
      // Role and usage actors do not choose a physical repository. Authenticate
      // their workspace/store without inventing a primary repository selection.
      requireOwnedWorkspaceRecord(fixture, workspaceId, 'legacy-agent-name');
      const actor = method === 'usage' ? UsageInput.parse(input).agent.agentId : method === 'task' ? TaskInput.parse(input).agentName :
        method === 'seedWorktree' ? SeedInput.parse(input).agentName : undefined;
      if (actor !== undefined) {
        await enrollOwnedLegacyAgent(fixture, workspaceId, actor, context.signal, getFixtureEvidenceStore(context, leaseId));
        const record = requireOwnedWorkspaceRecord(fixture, workspaceId, 'legacy-agent-name');
        if (!record?.agentIds.includes(actor)) throw new ObservationError('ownership-mismatch', 'Legacy actor is absent from retained enrollment');
        if (method === 'task') {
          const repoName = TaskInput.parse(input).repoName;
          // Older single-source creation receipts have no named topology.
          // Their concrete CLI boundary still checks the source-backed name;
          // named topology can additionally reject affinity before the factory.
          if (repoName !== null && record.repositories) requireOwnedWorkspace(fixture, workspaceId, actor, 'legacy-agent-name', repoName);
        }
        if (method === 'seedWorktree') requireOwnedWorkspace(fixture, workspaceId, actor, 'legacy-agent-name');
      }
    }
    let store = stores.get(fixture);
    if (!store) { store = { operations: createLegacyOperations(accessFactory(context, fixture), op =>
      getFixtureOperationAuthority(fixture, op, []).evidenceClass), sequence: 0 }; stores.set(fixture, store); }
    const call: Invocation = { runId: context.runId, invocationId: `${context.caseId}:${store.sequence++}`, signal: context.signal };
    try {
      const value = await store.operations[method](input, call);
      if (value.receipt.evidence !== grant.evidenceClass) throw new ObservationError('source-mismatch', 'Legacy evidence changed during observation');
      return { value: { ...value, redaction: redactionFacts(Json.parse(value), fixture.secrets) }, evidenceClass: grant.evidenceClass,
        identity: { fixtureLeaseId: fixture.leaseId, workspaceId: workspaceId ?? fixture.workspaceId }, secrets: fixture.secrets };
    } catch (error) {
      if (!(error instanceof LegacyError)) throw error;
      const code: ObservationError['code'] = error.code === 'source-mismatch' ? 'source-mismatch' : error.code === 'ownership-mismatch' ? 'ownership-mismatch' :
        error.code === 'stale-generation' ? 'identity-mismatch' : error.code === 'unsupported-capability' ? 'unsupported-capability' : 'observation-failed';
      throw new ObservationError(code, `${error.code}: ${error.message}`);
    }
  }
  return [
    defineOperation({ ...common, id: 'loom.cli.role', inputSchema: RoleInput, outputSchema: RoleOutput,
      effects: [...LegacyOperationEffects['loom.cli.role']], cleanup: 'release-lease', dispose: disposeFixtures, async run(input, context) { return invoke('role', input, context, input.leaseId, input.workspaceId) as Promise<{ value: z.infer<typeof RoleOutput>; evidenceClass: EvidenceClass }>; } }),
    defineOperation({ ...common, id: 'loom.cli.usage', inputSchema: UsageInput, outputSchema: UsageOutput,
      effects: [...LegacyOperationEffects['loom.cli.usage']], cleanup: 'release-lease', dispose: disposeFixtures, async run(input, context) { return invoke('usage', input, context, input.agent.fixtureLeaseId, input.agent.workspaceId) as Promise<{ value: z.infer<typeof UsageOutput>; evidenceClass: EvidenceClass }>; } }),
    defineOperation({ ...common, id: 'loom.cli.task', inputSchema: TaskInput, outputSchema: TaskOutput,
      effects: [...taskEffects], cleanup: 'release-lease', dispose: disposeFixtures,
      async run(input, context) { return invoke('task', input, context, input.leaseId, input.workspaceId) as Promise<{ value: z.infer<typeof TaskOutput>; evidenceClass: EvidenceClass }>; } }),
    defineOperation({ ...common, id: 'loom.runtime.stimulate', inputSchema: RuntimeInput, outputSchema: RuntimeOutput,
      effects: [...LegacyOperationEffects['loom.runtime.stimulate'], 'restart-owned-service'], cleanup: 'release-lease', dispose: disposeFixtures,
      async run(input, context) { return invoke('stimulate', input, context, input.leaseId) as Promise<{ value: z.infer<typeof RuntimeOutput>; evidenceClass: EvidenceClass }>; } }),
    defineOperation({ ...common, evidenceClasses: ['deterministic'], id: 'loom.fixture.seedWorktree', inputSchema: SeedInput, outputSchema: SeedOutput,
      effects: [...LegacyOperationEffects['loom.fixture.seedWorktree']], cleanup: 'release-lease', dispose: disposeFixtures,
      async run(input, context) { return invoke('seedWorktree', input, context, input.leaseId, input.workspaceId) as Promise<{ value: z.infer<typeof SeedOutput>; evidenceClass: EvidenceClass }>; } }),
    defineOperation({ ...common, id: 'loom.fixture.configure', inputSchema: ConfigureInput, outputSchema: ConfigureOutput,
      effects: [...LegacyOperationEffects['loom.fixture.configure']], cleanup: 'restore-setting', dispose: disposeFixtures,
      async run(input, context) { return invoke('configure', input, context, input.leaseId, 'workspaceId' in input ? input.workspaceId : undefined) as Promise<{ value: z.infer<typeof ConfigureOutput>; evidenceClass: EvidenceClass }>; } }),
  ];
}

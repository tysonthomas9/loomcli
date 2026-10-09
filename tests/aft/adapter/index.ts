import { lstat } from 'node:fs/promises';
import type { CapabilityProvider, CapabilityRegistry, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import type { ObservationResult } from '@tysonthomas9/aft/types';
import { z } from 'zod';
import { AgentRef, AgentRow, Id, requireFact, redact, type AgentRow as Row } from './protocol.js';
import { defineOperation } from './operation.js';
import { getSyntheticProbe } from './synthetic-probe.js';
import { getFixture, getAgent, type OwnedFixture } from './ownership.js';
import { CorrelationInput, CorrelationOutput, correlateEvents } from './correlation.js';
import { FailureInput, FailureOutput, observeNativeFailure } from './native-failure.js';
import { FilesInput, FilesOutput, observeFiles } from './files.js';
import { NativeInput, NativeOutput, observeNative } from './native.js';
import { SavedEventsInput, SavedEventsOutput, collectSavedEvents } from './events.js';
import { FilesystemInput, FilesystemOutput, observeFilesystem } from './filesystem.js';
import { GitInput, GitOutput, observeGit, type GitReader } from './git.js';
export * from './operation.js';
export * from './evidence.js';
export * from './ownership.js';
export * from './protocol.js';
export * from './native-host.js';
export * from './synthetic-probe.js';
export * from './projection.js';
export * from './container-observations.js';
export { createLegacyProviders } from './legacy/providers.js';
export type { LegacyAccessFactory } from './legacy/providers.js';

export const BindAgentInput = z.object({ leaseId: Id, workspaceId: Id, agentId: Id }).strict();
export const BindAgentOutput = z.object({ fixtureLeaseId: Id, workspaceId: Id, agentId: Id,
  parentAgentId: Id.nullable(), rootAgentId: Id.nullable(), repo: Id, worktree: Id, branch: Id,
  nativeSessionId: Id, nativeRoot: z.string(), harness: z.literal('opencode'),
}).strict();
const identity = (fixture: OwnedFixture, row?: Row): Omit<ObservationResult['provenance']['identity'], 'runId'> => ({
  fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId, ...(row ? {
    agentId: row.agent_id, parentAgentId: row.parent_agent_id, rootAgentId: row.root_agent_id,
    repo: row.repo, worktree: row.worktree_path, harness: row.harness,
    nativeSessionId: row.harness_session_id, nativeRoot: row.harness_session_root,
  } : {}),
});
const AgentObserveInput = z.object({ agent: AgentRef }).strict();
const AgentObserveOutput = z.object({ agentId: Id, state: Id, runningTurnId: Id.nullable(),
  parentAgentId: Id.nullable(), rootAgentId: Id.nullable(), deletedAt: Id.nullable(), historyPurgedAt: Id.nullable(),
  revision: z.number().int().nonnegative(),
}).strict();

export function createCoreProviders(implementation: ImplementationPin & { sha256: string }, gitReader?: GitReader): CapabilityProvider[] {
  const read = { effects: ['read-api' as const], retry: 'read-only-until-deadline' as const, cleanup: 'none' as const,
    evidenceClasses: ['deterministic', 'persisted-public-api', 'real-native', 'live-provider'] as const };
  const common = { implementation, implementationSha256: implementation.sha256, ...read, evidenceClasses: [...read.evidenceClasses] };
  return [
    defineOperation({ ...common, id: 'loom.agent.bind', inputSchema: BindAgentInput, outputSchema: BindAgentOutput,
      async run(input, context) {
        const fixture = await getFixture(context, input.leaseId);
        requireFact(input.workspaceId === fixture.workspaceId && !fixture.agents.has(input.agentId), 'ownership-mismatch', 'Foreign or duplicate agent binding');
        const agent = await fixture.resolveAgent(input.agentId, context.signal);
        const row = AgentRow.parse(agent.row);
        requireFact(row.agent_id === input.agentId && row.workspace_id === fixture.workspaceId && row.repo === fixture.repo,
          'ownership-mismatch', 'Agent discovery returned a foreign identity');
        fixture.agents.set(input.agentId, { ...agent, row });
        try { await getAgent(context, { fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId, agentId: input.agentId }); }
        catch (error) { fixture.agents.delete(input.agentId); throw error; }
        return { value: { fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId, agentId: row.agent_id,
          parentAgentId: row.parent_agent_id, rootAgentId: row.root_agent_id, repo: row.repo, worktree: row.worktree_path, branch: row.branch,
          nativeSessionId: row.harness_session_id, nativeRoot: row.harness_session_root, harness: row.harness },
          identity: identity(fixture, row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.agent.observe', inputSchema: AgentObserveInput, outputSchema: AgentObserveOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const current = await fixture.resolveAgent(input.agent.agentId, context.signal);
        const row = AgentRow.parse(current.row);
        requireFact(row.agent_id === agent.row.agent_id && row.workspace_id === fixture.workspaceId && row.repo === agent.row.repo &&
          row.worktree_path === agent.row.worktree_path && row.branch === agent.row.branch && row.parent_agent_id === agent.row.parent_agent_id &&
          row.root_agent_id === agent.row.root_agent_id, 'identity-mismatch', 'Agent identity changed');
        return { value: { agentId: row.agent_id, state: row.state, runningTurnId: row.running_turn_id, parentAgentId: row.parent_agent_id,
          rootAgentId: row.root_agent_id, deletedAt: row.deleted_at, historyPurgedAt: row.history_purged_at, revision: row.revision },
        identity: identity(fixture, row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.api.savedEvents', inputSchema: SavedEventsInput, outputSchema: SavedEventsOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await collectSavedEvents(input, fixture.readApi, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined);
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.api.correlate', inputSchema: CorrelationInput, outputSchema: CorrelationOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await correlateEvents(input, fixture.readApi, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined);
        return { value, identity: { ...identity(fixture, agent.row), turnId: input.turnId,
          ...(input.requestId ? { requestId: input.requestId } : {}), ...(input.itemId ? { itemId: input.itemId } : {}) },
          evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.files.observe', inputSchema: FilesInput, outputSchema: FilesOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await observeFiles(input, fixture.repo, fixture.readFiles, context.signal);
        requireFact(value.view !== 'content' || value.content === null || redact(value.content, fixture.secrets) === value.content,
          'observation-failed', 'Exact Files content contains private material');
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.native.observe', effects: ['read-native'], inputSchema: NativeInput, outputSchema: NativeOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        requireFact(agent.native, 'unsupported-capability', 'Native observation is not available for this fixture');
        const value = await observeNative(input, agent.native, agent.row, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined);
        requireFact(!('complete' in value) || value.complete, 'incomplete-pages', 'Native message history is incomplete');
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.native.failure', effects: ['read-native'], inputSchema: FailureInput, outputSchema: FailureOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        requireFact(agent.native, 'unsupported-capability', 'Native access is unavailable');
        const value = await observeNativeFailure(input, agent.native, agent.row, context.signal);
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.filesystem.observe', effects: ['read-filesystem'], inputSchema: FilesystemInput, outputSchema: FilesystemOutput,
      async run(input, context) {
        const fixture = await getFixture(context, input.leaseId);
        const root = fixture.roots.get(input.rootId);
        requireFact(root, 'ownership-mismatch', 'Filesystem root is not owned');
        let value: z.infer<typeof FilesystemOutput>;
        if (root.remoteObserve) {
          value = FilesystemOutput.parse(await root.remoteObserve(input, context.signal));
          await fixture.verify(context.signal);
        } else {
          const before = await lstat(root.path);
          requireFact(before.dev === root.device && before.ino === root.inode, 'identity-mismatch', 'Filesystem root changed');
          value = await observeFilesystem(input, root.path);
          const after = await lstat(root.path);
          requireFact(after.dev === root.device && after.ino === root.inode, 'identity-mismatch', 'Filesystem root changed during read');
        }
        // Exact bytes are useful only if they can be shared safely. Do not
        // return a base64 encoding that evades credential redaction.
        for (const entry of value.entries) if (entry.contentBase64 !== null) {
          const text = Buffer.from(entry.contentBase64, 'base64').toString('utf8');
          requireFact(redact(text, fixture.secrets) === text, 'observation-failed', 'Exact file bytes contain private material');
        }
        return { value, identity: identity(fixture), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.git.observe', effects: ['read-filesystem'], inputSchema: GitInput, outputSchema: GitOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = agent.gitObserve ? GitOutput.parse(await agent.gitObserve(input, context.signal)) :
          await observeGit(input, { worktree: agent.row.worktree_path, commonDir: agent.commonDir, branch: agent.row.branch }, gitReader);
        requireFact(value.worktree === agent.row.worktree_path && value.commonDir === agent.commonDir && value.branch === agent.row.branch,
          'identity-mismatch', 'Git observation belongs to another agent');
        await fixture.verify(context.signal);
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
  ];
}
/** Called by a fixed reviewed launcher, never selected by a YAML module path. */
export function registerLoomCore(registry: CapabilityRegistry, implementation: ImplementationPin & { sha256: string }, gitReader?: GitReader): void {
  for (const provider of createCoreProviders(implementation, gitReader)) registry.register(provider);
}


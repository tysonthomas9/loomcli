import { lstat } from 'node:fs/promises';
import type { CapabilityProvider, CapabilityRegistry, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import type { ObservationResult } from '@tysonthomas9/aft/types';
import { z } from 'zod';
import { AgentRef, AgentRow, AgentHistory, Id, requireFact, redact, type AgentRow as Row } from './protocol.js';
import { FixtureWorkerStateId,FixtureWorkerStateEffects,FixtureWorkerStateInput,FixtureWorkerStateOutput,observeFixtureWorkerState } from './fixture-worker-state.js';
import { FixtureComposeServeId,RestartComposeServeId,FixtureComposeServeEffects,RestartComposeServeEffects,
  FixtureComposeServeInput,FixtureComposeServeOutput,RestartComposeServeInput,RestartComposeServeOutput,
  observeComposeServe,restartComposeServe } from './fixture-compose.js';
import { getFixtureEvidenceStore } from './evidence.js';
import { retainSavedCapture } from './saved-capture.js';
export { SavedCaptureInput, rereadSavedCapture } from './saved-capture.js';
import { enrollOwnedWorkspaceAgent, requireOwnedWorkspaceRecord, requireOwnedWorkspace } from './workspaces.js';
import { defineOperation } from './operation.js';
import { getSyntheticProbe } from './synthetic-probe.js';
import { FixtureWorkersId,FixtureWorkersEffects,FixtureWorkersInput,FixtureWorkersOutput,observeFixtureWorkers } from './fixture-workers.js';
import { getFixture, getAgent, type OwnedFixture } from './ownership.js';
import { CorrelationInput, CorrelationOutput, correlateEvents } from './correlation.js';
import { FailureInput, FailureOutput, observeNativeFailure } from './native-failure.js';
import { FilesInput, FilesOutput, observeFiles } from './files.js';
import { NativeInput, NativeOutput, observeNative, NativeRegistrationInput, NativeRegistrationOutput, observeNativeRegistration } from './native.js';
import { SavedEventsInput, SavedEventsOutput, collectSavedEvents } from './events.js';
import { FilesystemInput, FilesystemOutput, normalizeFilesystemInput, observeFilesystem } from './filesystem.js';
import { GitLifecycleInput, GitLifecycleOutput, observeGitLifecycle } from './git-lifecycle.js';
import { GitInput, GitOutput, observeGit, type GitReader } from './git.js';
export * from './operation.js';
export * from './evidence.js';
export * from './ownership.js';
export * from './protocol.js';
export * from './native-host.js';
export * from './native-operation-effects.js';
export * from './synthetic-probe.js';
export * from './projection.js';
export * from './container-observations.js';
export * from './renderer-target.js';
export * from './renderer-contract.js';
export * from './composition.js';
export * from './redaction.js';
export * from './authority.js';
export * from './fixture-workers.js';
export * from './fixture-worker-state.js';
export * from './fixture-compose.js';
export * from './git-lifecycle.js';
export * from './workspaces.js';
export { createLegacyProviders } from './legacy/providers.js';
export type { LegacyAccessFactory } from './legacy/providers.js';
export { productionLegacyAccess } from './legacy/host-access.js';
export { TerminalDetachId, TerminalDetachInput, TerminalDetachFacts } from './legacy/terminal-metadata.js';
export { TerminalDetachEffects } from './legacy/effects.js';
export { TerminalDetachOutput, createTerminalDetachProviders } from './legacy/terminal-providers.js';

export const BindAgentInput = z.object({ leaseId: Id, workspaceId: Id, agentId: Id }).strict();
export const BindAgentOutput = z.object({ agentRef: AgentRef, fixtureLeaseId: Id, workspaceId: Id, agentId: Id,
  parentAgentId: Id.nullable(), rootAgentId: Id.nullable(), repo: Id, worktree: Id, branch: Id,
  nativeSessionId: Id, nativeRoot: z.string(), harness: z.literal('opencode'),
}).strict();
const identity = (fixture: OwnedFixture, row?: Row): Omit<ObservationResult['provenance']['identity'], 'runId'> => ({
  fixtureLeaseId: fixture.leaseId, workspaceId: row?.workspace_id ?? fixture.workspaceId, ...(row ? {
    agentId: row.agent_id, parentAgentId: row.parent_agent_id, rootAgentId: row.root_agent_id,
    repo: row.repo, worktree: row.worktree_path, harness: row.harness,
    nativeSessionId: row.harness_session_id, nativeRoot: row.harness_session_root,
  } : {}),
});
const AgentObserveInput = z.object({ agent: AgentRef }).strict();
const NullableAgentFact = z.object({ present:z.boolean(), value:z.string().max(4096).nullable() }).strict();
const AgentObserveOutput = z.object({ agentId: Id, state: Id, runningTurnId: Id.nullable(),
  parentAgentId: Id.nullable(), rootAgentId: Id.nullable(), deletedAt: Id.nullable(), historyPurgedAt: Id.nullable(),
  revision: z.number().int().nonnegative(),
  requestedModel: NullableAgentFact, outcome: NullableAgentFact,
}).strict();
const nullableAgentFact = (row:Row,key:'model'|'outcome') => Object.hasOwn(row,key)
  ? {present:true,value:z.string().max(4096).nullable().parse(row[key])} : {present:false,value:null};

export function createCoreProviders(implementation: ImplementationPin & { sha256: string }, gitReader?: GitReader): CapabilityProvider[] {
  const read = { effects: ['read-api' as const], retry: 'read-only-until-deadline' as const, cleanup: 'none' as const,
    evidenceClasses: ['deterministic', 'persisted-public-api', 'real-native', 'live-provider'] as const };
  const common = { implementation, implementationSha256: implementation.sha256, ...read, evidenceClasses: [...read.evidenceClasses] };
  return [
    defineOperation({...common,id:FixtureComposeServeId,effects:[...FixtureComposeServeEffects],retry:'never',
      inputSchema:FixtureComposeServeInput,outputSchema:FixtureComposeServeOutput,
      async run(input,context) {
        const {fixture,grant,value}=await observeComposeServe(context,input,implementation.sha256);
        return {value,identity:identity(fixture),evidenceClass:grant.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({...common,id:RestartComposeServeId,effects:[...RestartComposeServeEffects],retry:'never',
      inputSchema:RestartComposeServeInput,outputSchema:RestartComposeServeOutput,
      async run(input,context) {
        const {fixture,grant,value}=await restartComposeServe(context,input,implementation.sha256);
        return {value,identity:identity(fixture),evidenceClass:grant.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({...common,id:FixtureWorkerStateId,effects:[...FixtureWorkerStateEffects],retry:'never',
      inputSchema:FixtureWorkerStateInput,outputSchema:FixtureWorkerStateOutput,
      async run(input,context) {
        const {fixture,grant,value}=await observeFixtureWorkerState(context,input);
        return {value,identity:{...identity(fixture),...(input.view==='state'?{workspaceId:input.workspaceId,agentId:input.agentName}:{})},
          evidenceClass:grant.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({...common,id:FixtureWorkersId,effects:[...FixtureWorkersEffects],retry:'never',
      inputSchema:FixtureWorkersInput,outputSchema:FixtureWorkersOutput,
      async run(input,context) {
        const {fixture,grant,value}=await observeFixtureWorkers(context,input);
        return {value,identity:identity(fixture),evidenceClass:grant.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({...common,id:'loom.native.registration',effects:['read-native'],inputSchema:NativeRegistrationInput,outputSchema:NativeRegistrationOutput,
      async run(input,context) {
        const {fixture,agent} = await getAgent(context,input.agent);
        requireFact(agent.native,'unsupported-capability','Owned native transport is missing');
        const value = await observeNativeRegistration(input,agent.native,agent.row,context.signal);
        return {value,identity:identity(fixture,agent.row),evidenceClass:fixture.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({ ...common, id: 'loom.agent.bind', inputSchema: BindAgentInput, outputSchema: BindAgentOutput,
      async run(input, context) {
        const fixture = await getFixture(context, input.leaseId);
        requireOwnedWorkspaceRecord(fixture,input.workspaceId);
        if(fixture.ownedWorkspaces&&!fixture.ownedWorkspaces.find(value=>value.workspaceId===input.workspaceId&&value.identityKind==='native-agent-id')!.agentIds.includes(input.agentId))
          await enrollOwnedWorkspaceAgent(fixture,input.workspaceId,input.agentId,context.signal,getFixtureEvidenceStore(context,fixture.leaseId));
        const workspace = requireOwnedWorkspace(fixture,input.workspaceId,input.agentId);
        requireFact(!fixture.agents.has(input.agentId), 'ownership-mismatch', 'Foreign or duplicate agent binding');
        const agent = await fixture.resolveAgent(input.agentId, context.signal, input.workspaceId);
        const row = AgentRow.parse(agent.row);
        requireFact(row.agent_id === input.agentId && row.workspace_id === input.workspaceId && row.repo === workspace.repo && (!workspace.commonDir || agent.commonDir===workspace.commonDir),
          'ownership-mismatch', 'Agent discovery returned a foreign identity');
        fixture.agents.set(input.agentId, { ...agent, row });
        try { await getAgent(context, { fixtureLeaseId: fixture.leaseId, workspaceId: input.workspaceId, agentId: input.agentId }); }
        catch (error) { fixture.agents.delete(input.agentId); throw error; }
        const agentRef = AgentRef.parse({ fixtureLeaseId: fixture.leaseId, workspaceId: row.workspace_id, agentId: row.agent_id });
        return { value: { agentRef, ...agentRef,
          parentAgentId: row.parent_agent_id, rootAgentId: row.root_agent_id, repo: row.repo, worktree: row.worktree_path, branch: row.branch,
          nativeSessionId: row.harness_session_id, nativeRoot: row.harness_session_root, harness: row.harness },
          identity: identity(fixture, row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.agent.observe', inputSchema: AgentObserveInput, outputSchema: AgentObserveOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const current = await fixture.resolveAgent(input.agent.agentId, context.signal, input.agent.workspaceId);
        const row = AgentRow.parse(current.row);
        requireFact(row.agent_id === agent.row.agent_id && row.workspace_id === agent.row.workspace_id && row.repo === agent.row.repo &&
          row.worktree_path === agent.row.worktree_path && row.branch === agent.row.branch && row.parent_agent_id === agent.row.parent_agent_id &&
          row.root_agent_id === agent.row.root_agent_id, 'identity-mismatch', 'Agent identity changed');
        return { value: { agentId: row.agent_id, state: row.state, runningTurnId: row.running_turn_id, parentAgentId: row.parent_agent_id,
          rootAgentId: row.root_agent_id, deletedAt: row.deleted_at, historyPurgedAt: row.history_purged_at, revision: row.revision,
          requestedModel:nullableAgentFact(row,'model'),outcome:nullableAgentFact(row,'outcome') },
        identity: identity(fixture, row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.api.savedEvents', effects:['read-api','read-filesystem'],inputSchema: SavedEventsInput, outputSchema: SavedEventsOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await collectSavedEvents(input, fixture.readApi, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined, fixture.secrets);
        const captureReceipt=await retainSavedCapture(context,input,value,implementation.sha256);
        return { value:{...value,captureReceipt}, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.api.correlate', inputSchema: CorrelationInput, outputSchema: CorrelationOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await correlateEvents(input, fixture.readApi, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined, fixture.secrets);
        return { value, identity: { ...identity(fixture, agent.row), turnId: input.turnId,
          ...(input.requestId ? { requestId: input.requestId } : {}), ...(input.itemId ? { itemId: input.itemId } : {}) },
          evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.files.observe', inputSchema: FilesInput, outputSchema: FilesOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        const value = await observeFiles(input, agent.row.repo, fixture.readFiles, context.signal);
        requireFact(value.view !== 'content' || value.content === null || redact(value.content, fixture.secrets) === value.content,
          'observation-failed', 'Exact Files content contains private material');
        return { value, identity: identity(fixture, agent.row), evidenceClass: fixture.evidenceClass, secrets: fixture.secrets };
      },
    }),
    defineOperation({ ...common, id: 'loom.native.observe', effects: ['read-native'], inputSchema: NativeInput, outputSchema: NativeOutput,
      async run(input, context) {
        const { fixture, agent } = await getAgent(context, input.agent);
        requireFact(agent.native, 'unsupported-capability', 'Native observation is not available for this fixture');
        const value = await observeNative(input, agent.native, agent.row, context.signal, input.probeHandle ? getSyntheticProbe(fixture, input.probeHandle) : undefined, fixture.secrets);
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
      async run(request, context) {
        const input = normalizeFilesystemInput(request);
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
    defineOperation({ ...common, id:'loom.agent.history',effects:['read-filesystem'],inputSchema:AgentObserveInput,outputSchema:AgentHistory,
      async run(input,context) {
        const {fixture,agent}=await getAgent(context,input.agent);
        requireFact(agent.native?.history,'unsupported-capability','Owned saved-history transport is unavailable');
        const value=AgentHistory.parse(await agent.native.history(input.agent.agentId));
        requireFact(value.agentId===agent.row.agent_id&&value.workspaceId===agent.row.workspace_id&&value.repo===agent.row.repo,
          'identity-mismatch','Saved history belongs to another agent');
        await fixture.verify(context.signal);
        return {value,identity:identity(fixture,agent.row),evidenceClass:fixture.evidenceClass,secrets:fixture.secrets};
      },
    }),
    defineOperation({ ...common,id:'loom.git.lifecycle',effects:['read-filesystem'],inputSchema:GitLifecycleInput,outputSchema:GitLifecycleOutput,
      async run(input,context) {
        const {fixture,agent}=await getAgent(context,input.agent);
        const value=agent.gitLifecycle ? GitLifecycleOutput.parse(await agent.gitLifecycle(input,context.signal)) :
          await observeGitLifecycle(input,{sourceRoot:agent.row.repo,commonDir:agent.commonDir,branch:agent.row.branch,worktree:agent.row.worktree_path},gitReader);
        requireFact(value.agentId===agent.row.agent_id&&value.sourceRoot===agent.row.repo&&value.commonDir===agent.commonDir&&
          value.branch===agent.row.branch&&value.worktree===agent.row.worktree_path,'identity-mismatch','Lifecycle Git belongs to another agent');
        await fixture.verify(context.signal);
        return {value,identity:identity(fixture,agent.row),evidenceClass:fixture.evidenceClass,secrets:fixture.secrets};
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


export { createFixtureProviders, productionFixtureOptions } from './fixture/providers.js';
export type { FixtureProviderOptions } from './fixture/providers.js';

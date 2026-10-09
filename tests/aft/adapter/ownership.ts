import { getRegisteredResource, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { z } from 'zod';
import type { FilesystemInput, FilesystemOutput } from './filesystem.js';
import type { GitInput, GitOutput } from './git.js';
import type { OwnedRendererTarget } from './renderer-target.js';
import type { SyntheticProbe } from './synthetic-probe.js';
import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { validateFixtureOperationAuthority, type FixtureOperationAuthority } from './authority.js';
import { bindEvidenceStore, evidenceKey } from './evidence.js';
import { AgentRow, AgentRef, requireFact, type NativeAccess, type ReadTransport } from './protocol.js';

export interface OwnedRoot {
  path: string; device: number; inode: number;
  remoteObserve?: (input: z.infer<typeof FilesystemInput>, signal: AbortSignal) => Promise<z.infer<typeof FilesystemOutput>>;
}
export interface OwnedAgent {
  row: AgentRow;
  commonDir: string;
  native?: NativeAccess;
  gitObserve?: (input: z.infer<typeof GitInput>, signal: AbortSignal) => Promise<z.infer<typeof GitOutput>>;
}
/** Private fixture state is never serialized into suite bindings or receipts. */
export interface OwnedFixture {
  leaseId: string;
  runId: string;
  suiteId: string;
  scope: 'suite' | 'case';
  caseId: string;
  workspaceId: string;
  repo: string;
  profile: string;
  expiresAtUtcMs: number;
  evidenceClass: EvidenceClass;
  roots: Map<string, OwnedRoot>;
  agents: Map<string, OwnedAgent>;
  secrets: readonly string[];
  syntheticProbe?: SyntheticProbe;
  rendererTarget?: OwnedRendererTarget;
  operationAuthority?: FixtureOperationAuthority;
  readApi: ReadTransport;
  readFiles: ReadTransport;
  resolveAgent(agentId: string, signal: AbortSignal): Promise<OwnedAgent>;
  verify(signal: AbortSignal): Promise<void>;
  dispose(): Promise<void>;
}
export const fixturesKey = '@loom/aft-adapter/fixtures/v1';
const resourceKey = (leaseId: string) => `${fixturesKey}:${leaseId}`;
export function putFixture(context: CapabilityContext, fixture: OwnedFixture): void {
  requireFact(fixture.runId === context.runId && fixture.suiteId === context.suiteId && fixture.scope === context.scope &&
    (fixture.scope === 'suite' || fixture.caseId === context.caseId) && fixture.leaseId && !context.resources.has(resourceKey(fixture.leaseId)),
    'ownership-mismatch', 'Fixture ownership is invalid or duplicated');
  if (fixture.operationAuthority) validateFixtureOperationAuthority(fixture.operationAuthority,fixture);
  bindEvidenceStore(context, fixture.leaseId);
  context.resources.set(resourceKey(fixture.leaseId), fixture);
  try { context.registerResource(fixture.leaseId, [resourceKey(fixture.leaseId), `${evidenceKey}:${fixture.leaseId}`]); }
  catch (error) {
    context.resources.delete(resourceKey(fixture.leaseId));
    context.resources.delete(`${evidenceKey}:${fixture.leaseId}`);
    throw error;
  }
}
export async function getFixture(context: CapabilityContext, leaseId: string): Promise<OwnedFixture> {
  let fixture = context.resources.get(resourceKey(leaseId)) as OwnedFixture | undefined;
  let fromSuite = false;
  if (fixture) {
    try { fixture = getRegisteredResource(context, resourceKey(leaseId), leaseId) as OwnedFixture; }
    catch { requireFact(false, 'ownership-mismatch', 'Fixture authority is missing or revoked'); }
  }
  if (!fixture && context.scope === 'case' && context.suite?.id === context.suiteId && context.suite.handles.includes(leaseId)) {
    fixture = context.suite.getResource(resourceKey(leaseId), leaseId) as OwnedFixture | undefined;
    fromSuite = true;
  }
  requireFact(fixture && fixture.runId === context.runId && fixture.suiteId === context.suiteId &&
    (fromSuite ? fixture.scope === 'suite' : fixture.scope === context.scope && (fixture.scope === 'suite' || fixture.caseId === context.caseId)),
    'ownership-mismatch', 'Fixture handle is missing or belongs to another scope');
  requireFact(context.clock.epochUtcMs + context.clock.now() < fixture.expiresAtUtcMs,
    'ownership-mismatch', 'Fixture lease expired');
  context.signal.throwIfAborted();
  await fixture.verify(context.signal);
  return fixture;
}
export async function getAgent(context: CapabilityContext, ref: AgentRef): Promise<{ fixture: OwnedFixture; agent: OwnedAgent }> {
  const fixture = await getFixture(context, ref.fixtureLeaseId);
  requireFact(fixture.workspaceId === ref.workspaceId, 'ownership-mismatch', 'Foreign agent workspace');
  const agent = fixture.agents.get(ref.agentId);
  requireFact(agent && agent.row.agent_id === ref.agentId && agent.row.workspace_id === ref.workspaceId &&
    agent.row.repo === fixture.repo, 'ownership-mismatch', 'Agent is not bound to the owned fixture');
  const parentId = agent.row.parent_agent_id;
  if (parentId !== null) {
    const parent = fixture.agents.get(parentId);
    requireFact(parent && agent.row.created_by_kind === 'agent' && agent.row.created_by_id === parentId &&
      parent.row.repo === fixture.repo && parent.row.workspace_id === fixture.workspaceId &&
      agent.row.root_agent_id === (parent.row.root_agent_id ?? parentId), 'identity-mismatch', 'Agent parent/root ownership is invalid');
  } else requireFact(agent.row.root_agent_id === null && agent.row.created_by_kind === 'user', 'identity-mismatch', 'Root agent ownership is invalid');
  return { fixture, agent };
}
export async function releaseFixture(context: CapabilityContext, leaseId: string): Promise<void> {
  const fixture = context.resources.get(resourceKey(leaseId)) as OwnedFixture | undefined;
  requireFact(fixture && fixture.runId === context.runId && fixture.suiteId === context.suiteId && fixture.scope === context.scope &&
    (fixture.scope === 'suite' || fixture.caseId === context.caseId), 'ownership-mismatch', 'Cannot release a foreign fixture');
  // Cleanup remains available after expiry or cancellation. Keep failed
  // disposals registered so final cleanup can retry the exact resources.
  await fixture.dispose();
  context.resources.delete(resourceKey(leaseId));
}
export async function disposeFixtures(context: CapabilityContext): Promise<void> {
  const failures: unknown[] = [];
  for (const [key, value] of context.resources) {
    if (!key.startsWith(fixturesKey + ':')) continue;
    const fixture = value as OwnedFixture;
    if (fixture.runId !== context.runId || fixture.suiteId !== context.suiteId || fixture.scope !== context.scope ||
      (fixture.scope === 'case' && fixture.caseId !== context.caseId)) continue;
    try { await releaseFixture(context, fixture.leaseId); } catch (error) { failures.push(error); }
  }
  if (failures.length) throw new Error('Owned fixture cleanup failed');
}
export function fixtureReceipt(fixture: OwnedFixture) {
  return { leaseId: fixture.leaseId, workspaceId: fixture.workspaceId, repo: fixture.repo,
    profile: fixture.profile, ...(fixture.syntheticProbe ? { syntheticProbeHandle: fixture.syntheticProbe.handle, syntheticProbeRunId: fixture.syntheticProbe.runId } : {}), roots: [...fixture.roots.keys()].sort(), agentIds: [...fixture.agents.keys()].sort() };
}

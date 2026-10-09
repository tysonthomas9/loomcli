import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { AgentRow, AgentRef, requireFact, type NativeAccess, type ReadTransport } from './protocol.js';

export interface OwnedRoot { path: string; device: number; inode: number }
export interface OwnedAgent {
  row: AgentRow;
  commonDir: string;
  native?: NativeAccess;
}
/** Private fixture state is never serialized into suite bindings or receipts. */
export interface OwnedFixture {
  leaseId: string;
  runId: string;
  caseId: string;
  workspaceId: string;
  repo: string;
  profile: string;
  expiresAtUtcMs: number;
  evidenceClass: EvidenceClass;
  roots: Map<string, OwnedRoot>;
  agents: Map<string, OwnedAgent>;
  secrets: readonly string[];
  readApi: ReadTransport;
  readFiles: ReadTransport;
  resolveAgent(agentId: string, signal: AbortSignal): Promise<OwnedAgent>;
  verify(signal: AbortSignal): Promise<void>;
  dispose(): Promise<void>;
}
const fixturesKey = Symbol.for('@loom/aft-adapter/fixtures/v1');
function store(context: CapabilityContext): Map<string, OwnedFixture> {
  let fixtures = context.resources.get(String(fixturesKey)) as Map<string, OwnedFixture> | undefined;
  if (!fixtures) { fixtures = new Map(); context.resources.set(String(fixturesKey), fixtures); }
  return fixtures;
}
export function putFixture(context: CapabilityContext, fixture: OwnedFixture): void {
  requireFact(fixture.runId === context.runId && fixture.caseId === context.caseId && fixture.leaseId &&
    !store(context).has(fixture.leaseId), 'ownership-mismatch', 'Fixture ownership is invalid or duplicated');
  store(context).set(fixture.leaseId, fixture);
}
export async function getFixture(context: CapabilityContext, leaseId: string): Promise<OwnedFixture> {
  const fixture = store(context).get(leaseId);
  requireFact(fixture && fixture.runId === context.runId && fixture.caseId === context.caseId,
    'ownership-mismatch', 'Fixture handle is missing or belongs to another case');
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
  const fixture = store(context).get(leaseId);
  requireFact(fixture && fixture.runId === context.runId && fixture.caseId === context.caseId, 'ownership-mismatch', 'Cannot release a foreign fixture');
  // Cleanup is deliberately available after lease expiry or cancellation. Keep
  // failed disposals registered so final cleanup can retry the exact resources.
  await fixture.dispose();
  store(context).delete(leaseId);
}
export async function disposeFixtures(context: CapabilityContext): Promise<void> {
  const failures: unknown[] = [];
  for (const [id, fixture] of store(context)) {
    if (fixture.runId !== context.runId || fixture.caseId !== context.caseId) continue;
    try { await releaseFixture(context, id); } catch (error) { failures.push(error); }
  }
  if (failures.length) throw new Error('Owned fixture cleanup failed');
}
export function fixtureReceipt(fixture: OwnedFixture) {
  return { leaseId: fixture.leaseId, workspaceId: fixture.workspaceId, repo: fixture.repo,
    profile: fixture.profile, roots: [...fixture.roots.keys()].sort(), agentIds: [...fixture.agents.keys()].sort() };
}

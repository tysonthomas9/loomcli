import type { EvidenceStore } from '../evidence.js';
import type { OwnedFixture } from '../ownership.js';
import { createOwnedWorkspaceRoster, LegacyWorkspaceAgentFact } from '../workspaces.js';
import { fixtureOwnerIdentity } from '../authority.js';

// Retained injected creation facts exercise authority only. These test records
// are never evidence of product-created workspaces or real actor activity.
export async function testLegacyRoster(fixture: OwnedFixture, store: EvidenceStore,
  records = [{ workspaceId: fixture.workspaceId, repo: fixture.repo, agentIds: [] as string[] }]) {
  const owner = fixtureOwnerIdentity(fixture);
  return createOwnedWorkspaceRoster(owner, await Promise.all(records.map(async record => {
    const fields = { identityKind: 'legacy-agent-name' as const, ...record, commonDir: `${record.repo}/.git`,
      storeId: 'injected-legacy-store', storeGeneration: 'injected-store-generation' };
    const creationReceipt = await store.retain(JSON.stringify({ kind: 'workspace-created', ...owner, ...fields, agentIds: [] }));
    const enrollmentReceipts = [];
    for (const name of record.agentIds) {
      if (!fixture.readWorkspaceLegacyAgent) throw new Error('Injected actor creation facts are required');
      const fact = LegacyWorkspaceAgentFact.parse(await fixture.readWorkspaceLegacyAgent(record.workspaceId, name, new AbortController().signal));
      enrollmentReceipts.push(await store.retain(JSON.stringify(fact)));
    }
    return { ...fields, creationReceipt, enrollmentReceipts };
  })), store);
}

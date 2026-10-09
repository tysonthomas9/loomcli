import type { FixtureAuthorityOwner } from '../authority.js';
import { fixtureOwnerIdentity } from '../authority.js';
import { AgentRow } from '../protocol.js';
import { WorkspaceAgentFact } from '../workspaces.js';
import { FixtureError } from './lifecycle.js';
import { HostWorkspaceRecords, type WorkspaceRecordPorts } from './workspace-records.js';

const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
/** Bound to the canonical native-host rawAgent reader in the exact owning
 * workspace/store. This port cannot select SQL, a module or an executable. */
export type NativeRowRead=(workspaceId:string,agentId:string,signal:AbortSignal)=>Promise<AgentRow>;
/** Creation topology comes from successful product creation and exact GETs.
 * Native immutable IDs remain distinct from workspace-scoped legacy names. */
export class NativeWorkspaceRecords extends HostWorkspaceRecords {
 constructor(ports:WorkspaceRecordPorts,private readonly readRow:NativeRowRead){super(ports,'native-agent-id');}
 async nativeAgent(owner:FixtureAuthorityOwner,workspaceId:string,agentId:string,signal:AbortSignal){
  signal.throwIfAborted();const before=await this.workspace(workspaceId,signal);
  const row=AgentRow.parse(await this.readRow(workspaceId,agentId,signal));
  check(row.agent_id===agentId&&row.workspace_id===workspaceId);
  const matches=before.repositories.filter(repository=>repository.repo===row.repo);check(matches.length===1);
  const selected=matches[0]!;
  const after=await this.workspace(workspaceId,signal);
  check(after===before);signal.throwIfAborted();
  return WorkspaceAgentFact.parse({kind:'agent-enrolled',identityKind:'native-agent-id',...fixtureOwnerIdentity(owner),
   workspaceId,agentId:row.agent_id,repo:row.repo,commonDir:selected.commonDir,...before.store,
   parentAgentId:row.parent_agent_id,rootAgentId:row.root_agent_id,createdByKind:row.created_by_kind,
   createdById:row.created_by_id,revision:row.revision});
 }
}

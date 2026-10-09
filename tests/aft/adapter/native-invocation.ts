import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { getFixtureEvidenceStore, type EvidenceStore } from './evidence.js';
import { beginNativeOperation } from './native-operation-authority.js';
import { getAgent } from './ownership.js';
import { AgentRow, requireFact, type AgentRef, type NativeAccess } from './protocol.js';
import { requireOwnedWorkspace, requireOwnedWorkspaceRecord } from './workspaces.js';

export type NativeInvocation = ReturnType<typeof beginNativeOperation>;
const identityKeys = ['agent_id','workspace_id','repo','worktree_path','branch','harness','harness_session_id',
  'harness_session_root','parent_agent_id','root_agent_id','created_by_kind','created_by_id','created_at'] as const;

/** Snapshot the existing bound actor BEFORE an awaited verifier can replace it.
 * Mutable model/state/revision fields are not incarnation or source identity. */
export function captureNativeAgent(guard: NativeInvocation, ref: AgentRef) {
  guard.recheck();
  const fixture = guard.fixture, agent = fixture.agents.get(ref.agentId);
  requireFact(agent, 'ownership-mismatch', 'Agent is not bound to the owned fixture');
  const row = AgentRow.parse(agent.row), commonDir = agent.commonDir, native = agent.native;
  const workspace = requireOwnedWorkspace(fixture, ref.workspaceId, ref.agentId);
  const record = requireOwnedWorkspaceRecord(fixture,ref.workspaceId);
  const parent = row.parent_agent_id === null ? undefined : fixture.agents.get(row.parent_agent_id);
  const parentRow = parent ? AgentRow.parse(parent.row) : undefined, parentCommonDir = parent?.commonDir;
  const sameIdentity = (before: AgentRow, after: AgentRow) => identityKeys.every(key =>
    Object.hasOwn(before,key) === Object.hasOwn(after,key) && before[key] === after[key]);
  const recheck = () => {
    guard.recheck();
    const selected = requireOwnedWorkspace(fixture, ref.workspaceId, ref.agentId);
    requireFact(fixture.agents.get(ref.agentId) === agent && sameIdentity(row,agent.row) &&
      requireOwnedWorkspaceRecord(fixture,ref.workspaceId) === record &&
      agent.commonDir === commonDir && agent.native === native &&
      Object.entries(workspace).every(([key,value]) => selected[key as keyof typeof selected] === value) &&
      (!parentRow || fixture.agents.get(row.parent_agent_id!) === parent && parent &&
        sameIdentity(parentRow,parent.row) && parent.commonDir === parentCommonDir),
    'identity-mismatch', 'Bound native actor or source changed');
  };
  const checked = async <T>(call:()=>Promise<T>) => {recheck();const value=await call();recheck();return value;};
  recheck();
  return {agent,row,recheck,checked};
}

/** Preserve the existing private NativeAccess signatures. Methods lacking a
 * signal are continuity-checked, not advertised as interruptible I/O. */
function guardNativeAccess(captured: ReturnType<typeof captureNativeAgent>) {
  const source = captured.agent.native;
  requireFact(source,'unsupported-capability','Owned native transport is missing');
  const methods = {registration:source.registration,process:source.process,sessions:source.sessions,agent:source.agent,
    agentIdentity:source.agentIdentity,history:source.history,read:source.read,log:source.log};
  const executable = source.pinnedExecutable;
  const recheck = () => {
    captured.recheck();
    requireFact(source.pinnedExecutable === executable && Object.entries(methods).every(([key,value]) =>
      source[key as keyof typeof methods] === value),'identity-mismatch','Native transport changed');
  };
  const checked = async <T>(call:()=>Promise<T>) => {recheck();const value=await call();recheck();return value;};
  const access: NativeAccess = {
    pinnedExecutable:executable,
    registration:()=>checked(()=>methods.registration.call(source)),
    process:()=>checked(()=>methods.process.call(source)),
    sessions:id=>checked(()=>methods.sessions.call(source,id)),
    agent:id=>checked(()=>methods.agent.call(source,id)),
    read:(path,signal)=>checked(()=>methods.read.call(source,path,signal)),
    ...(methods.agentIdentity?{agentIdentity:(id:string,signal:AbortSignal)=>checked(()=>methods.agentIdentity!.call(source,id,signal))}:{}),
    ...(methods.history?{history:(id:string)=>checked(()=>methods.history!.call(source,id))}:{}),
    ...(methods.log?{log:(id:string,signal:AbortSignal)=>checked(()=>methods.log!.call(source,id,signal))}:{}),
  };
  recheck();return {access,recheck,checked};
}

export async function ownedNativeInvocation(context: CapabilityContext, ref: AgentRef,
  operation:'loom.native.registration'|'loom.native.observe') {
  const guard=beginNativeOperation(context,ref.fixtureLeaseId,operation),actor=captureNativeAgent(guard,ref);
  const native=guardNativeAccess(actor),captured={...actor,recheck:native.recheck,checked:native.checked};
  await captured.checked(()=>getAgent(context,ref));
  return {guard,captured,access:native.access};
}

/** A forwarding port over the SAME registered store, not another authority or
 * artifact store. Enrollment's internal receipt reads share invocation checks. */
export function guardedEnrollmentStore(context:CapabilityContext,guard:NativeInvocation,recheck:()=>void):EvidenceStore {
  const store=getFixtureEvidenceStore(context,guard.fixture.leaseId);
  const retain=store.retain,resolve=store.resolve,resolveBounded=store.resolveBounded;
  const checked=async<T>(call:()=>Promise<T>)=>{recheck();const value=await call();recheck();return value;};
  return {retain:serialized=>checked(()=>retain.call(store,serialized)),resolve:id=>checked(()=>resolve.call(store,id)),
    resolveBounded:(receipt,maxBytes)=>checked(()=>resolveBounded.call(store,receipt,maxBytes))};
}

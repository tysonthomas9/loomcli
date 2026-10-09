import { z } from 'zod';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { CapabilityEffect } from '@tysonthomas9/aft/types';
import { AgentRef,Id,NativeAgentIdentity,requireFact,redact } from './protocol.js';
import { beginFixtureOperation } from './native-operation-authority.js';
import { captureNativeAgent } from './native-invocation.js';

export const ArchiveAgentId='loom.agent.archive' as const;
// Verification may write evidence and terminate bounded inspection helpers.
// The fixed archive route can retire an existing native launch. No OS exit
// or cancellation outcome follows from a successful HTTP status.
export const ArchiveAgentEffects=Object.freeze(['read-api','read-native','read-filesystem','write-fixture',
  'start-owned-process','stop-owned-process','release-owned-resource'] as const satisfies readonly CapabilityEffect[]);
const AgentId=z.string().regex(/^agt_[A-Za-z0-9_-]+$/).max(512);
const Key=z.string().min(1).max(512).regex(/^[A-Za-z0-9._:-]+$/);
export const ArchiveAgentInput=z.object({agent:AgentRef.extend({agentId:AgentId}),namePrefixes:z.array(Id).min(1).max(2),idempotencyKey:Key}).strict();
export type ArchiveAgentInput=z.infer<typeof ArchiveAgentInput>;
export const ArchiveAgentIdentity=NativeAgentIdentity.pick({agent_id:true,workspace_id:true,name:true,created_at:true,
  repo:true,worktree_path:true,branch:true,parent_agent_id:true,root_agent_id:true,created_by_kind:true,created_by_id:true}).strict();
export type ArchiveAgentIdentity=z.infer<typeof ArchiveAgentIdentity>;
/** Project only actual identity fields from the full store/API row. The
 * retained request/reply identity itself remains a closed wire schema. */
export function archiveAgentIdentity(raw:unknown):ArchiveAgentIdentity {
  return ArchiveAgentIdentity.strip().parse(raw);
}
export const ArchiveAgentRequest=ArchiveAgentInput.extend({incarnation:ArchiveAgentIdentity,
  body:z.object({cancel:z.literal(true)}).strict(),requestTimeoutMs:z.literal(15000)}).strict();
export type ArchiveAgentRequest=z.infer<typeof ArchiveAgentRequest>;
export const ArchiveAgentFacts=z.object({agent:AgentRef,observed:ArchiveAgentIdentity,
  status:z.number().int().min(100).max(599),requestTimeoutMs:z.literal(15000),
  idempotencyKey:Key,body:z.object({cancel:z.literal(true)}).strict(),responseJsonParsed:z.literal(false)}).strict();
export type ArchiveAgentFacts=z.infer<typeof ArchiveAgentFacts>;
export const ArchiveAgentOutput=ArchiveAgentFacts.extend({conflictIgnored:z.boolean()}).strict();

/** The fixed owning callback calls this on the actual fresh GET/store identity
 * BEFORE dispatch. Name is mutable; creation/source/lineage are incarnation.
 * YAML retains source-specific prefix, ordering and stable key predicates. */
export function assertArchiveAgentTarget(request:ArchiveAgentRequest,raw:unknown):ArchiveAgentIdentity {
  const current=archiveAgentIdentity(raw);
  requireFact(current.agent_id===request.agent.agentId&&current.workspace_id===request.agent.workspaceId&&
    request.namePrefixes.some(prefix=>current.name.startsWith(prefix))&&Object.entries(request.incarnation).every(([key,value])=>key==='name'||
      current[key as keyof ArchiveAgentIdentity]===value),
  'identity-mismatch','Archive target name, incarnation or source changed');
  return current;
}

/** No generic HTTP escape or separate actor registry. The provisioner installs
 * a callback only after authentic primary/store/current actor acquisition. */
export async function archiveAgent(context:CapabilityContext,input:ArchiveAgentInput) {
  const guard=beginFixtureOperation(context,input.agent.fixtureLeaseId,ArchiveAgentId,ArchiveAgentEffects),{fixture,grant}=guard;
  const producer=fixture.archiveAgent;
  requireFact(producer,'unsupported-capability','Owned archive route is unavailable');
  const captured=captureNativeAgent(guard,input.agent);
  const incarnation=archiveAgentIdentity(captured.row);
  const recheck=()=>{captured.recheck();requireFact(fixture.archiveAgent===producer,
    'ownership-mismatch','Archive callback changed');};
  const checked=async<T>(call:()=>Promise<T>)=>{recheck();const value=await call();recheck();return value;};
  recheck();
  const request=ArchiveAgentRequest.parse({...input,incarnation,body:{cancel:true},requestTimeoutMs:15000});
  Object.freeze(request.agent);Object.freeze(request.namePrefixes);Object.freeze(request.incarnation);
  Object.freeze(request.body);Object.freeze(request);
  requireFact(redact(JSON.stringify(request),fixture.secrets)===JSON.stringify(request),
    'observation-failed','Archive request contains private material');
  await checked(()=>guard.verify());
  const facts=ArchiveAgentFacts.parse(await checked(()=>producer.call(fixture,request,context.signal)));
  assertArchiveAgentTarget(request,facts.observed);
  requireFact(facts.agent.fixtureLeaseId===request.agent.fixtureLeaseId&&facts.agent.workspaceId===request.agent.workspaceId&&
    facts.agent.agentId===request.agent.agentId&&facts.idempotencyKey===request.idempotencyKey,
    'identity-mismatch','Archive response belongs to another request');
  requireFact(facts.status>=200&&facts.status<300||facts.status===409,'observation-failed','Archive HTTP request failed');
  recheck();
  return {fixture,grant,value:ArchiveAgentOutput.parse({...facts,conflictIgnored:facts.status===409}),
    retention:{recheck,retain:(serialized:string)=>checked(()=>guard.retain(serialized))}};
}

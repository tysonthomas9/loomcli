import { readFile } from 'node:fs/promises';
import { z } from 'zod';
import { ArtifactRefSchema } from '@tysonthomas9/aft/types';
import { Id, requireFact, sha256, redact } from './protocol.js';
import type { EvidenceStore } from './evidence.js';
import { fixtureOwnerIdentity, type FixtureAuthorityOwner } from './authority.js';
import type { OwnedFixture } from './ownership.js';

const Fields = { workspaceId:Id,repo:Id,commonDir:Id,storeId:Id,storeGeneration:Id,
  agentIds:z.array(Id).max(1000) };
export const WorkspaceCreationFact = z.object({kind:z.literal('workspace-created'),leaseId:Id,runId:Id,suiteId:Id,
  scope:z.enum(['suite','case']),caseId:Id,profile:Id,...Fields}).strict();
export const WorkspaceAgentFact = z.object({kind:z.literal('agent-enrolled'),leaseId:Id,runId:Id,suiteId:Id,
  scope:z.enum(['suite','case']),caseId:Id,profile:Id,workspaceId:Id,agentId:Id,repo:Id,commonDir:Id,storeId:Id,storeGeneration:Id,
  parentAgentId:Id.nullable(),rootAgentId:Id.nullable(),createdByKind:z.enum(['user','agent']),createdById:Id.nullable(),
  revision:z.number().int().nonnegative()}).strict();
export type WorkspaceAgentFact=z.infer<typeof WorkspaceAgentFact>;
const RecordSchema = z.object({...Fields,creationReceipt:ArtifactRefSchema,enrollmentReceipts:z.array(ArtifactRefSchema).max(1000).default([])}).strict();
export type OwnedWorkspaceRecord = Readonly<Omit<z.infer<typeof RecordSchema>,'agentIds'|'enrollmentReceipts'> & {readonly agentIds:readonly string[];readonly enrollmentReceipts:readonly z.infer<typeof ArtifactRefSchema>[]}>;
export type OwnedWorkspaceRoster = readonly OwnedWorkspaceRecord[];
const generated = new WeakMap<object,Readonly<FixtureAuthorityOwner>>();
const current = new WeakMap<OwnedFixture,OwnedWorkspaceRoster>();
/** Trusted finite fixture setup calls this only after actual owned creation.
 * A listing or requested identifier cannot replace the retained creation fact. */
export async function createOwnedWorkspaceRoster(owner:FixtureAuthorityOwner,records:readonly z.input<typeof RecordSchema>[],store:EvidenceStore):Promise<OwnedWorkspaceRoster> {
  const identity=fixtureOwnerIdentity(owner);
  requireFact(records.length>0&&records.length<=32,'ownership-mismatch','Owned workspace roster exceeds bound');
  const workspaceIds=new Set<string>(); const agentIds=new Set<string>();
  const parsed=[];
  for(const input of records) {
    const record=RecordSchema.parse(input);
    requireFact(!workspaceIds.has(record.workspaceId),'ownership-mismatch','Duplicate owned workspace'); workspaceIds.add(record.workspaceId);
    for(const agentId of record.agentIds) { requireFact(!agentIds.has(agentId),'ownership-mismatch','Duplicate owned agent membership');agentIds.add(agentId); }
    const receiptFact=async(receipt:z.infer<typeof ArtifactRefSchema>)=>{
      const bytes=await readFile(await store.resolve(receipt.id));
      requireFact(bytes.byteLength===receipt.bytes&&await sha256(bytes)===receipt.sha256,'identity-mismatch','Workspace ownership receipt changed');
      const serialized=bytes.toString('utf8');
      requireFact(redact(serialized)===serialized,'observation-failed','Workspace ownership receipt contains private material');
      return JSON.parse(serialized) as unknown;
    };
    const fact=WorkspaceCreationFact.parse(await receiptFact(record.creationReceipt));
    const {creationReceipt,enrollmentReceipts,...fields}=record;
    const sameIdentity=(fact:WorkspaceAgentFact|z.infer<typeof WorkspaceCreationFact>)=>Object.entries(identity).every(([key,value])=>fact[key as keyof typeof identity]===value);
    requireFact(sameIdentity(fact)&&Object.entries(fields).every(([key,value])=>key==='agentIds'||fact[key as keyof typeof fields]===value),
      'ownership-mismatch','Workspace creation receipt belongs to another fixture or source');
    const members=[...fact.agentIds];
    for(const receipt of enrollmentReceipts) {
      const enrolled=WorkspaceAgentFact.parse(await receiptFact(receipt));
      requireFact(sameIdentity(enrolled)&&['workspaceId','repo','commonDir','storeId','storeGeneration'].every(key=>enrolled[key as keyof typeof enrolled]===fields[key as keyof typeof fields])&&
        !members.includes(enrolled.agentId),'ownership-mismatch','Agent enrollment receipt is foreign or duplicated');
      if(enrolled.parentAgentId===null)requireFact(enrolled.rootAgentId===null&&enrolled.createdByKind==='user',
        'identity-mismatch','Enrolled root actor is invalid');
      else requireFact(members.includes(enrolled.parentAgentId)&&enrolled.createdByKind==='agent'&&enrolled.createdById===enrolled.parentAgentId&&enrolled.rootAgentId!==null&&members.includes(enrolled.rootAgentId),
        'identity-mismatch','Enrolled agent lineage is not owned');
      members.push(enrolled.agentId);
    }
    requireFact(JSON.stringify(members)===JSON.stringify(fields.agentIds),'ownership-mismatch','Agent membership differs from retained creation facts');
    parsed.push(Object.freeze({...fields,creationReceipt:Object.freeze({...creationReceipt}),enrollmentReceipts:Object.freeze(enrollmentReceipts.map(value=>Object.freeze({...value}))),agentIds:Object.freeze([...record.agentIds])}));
  }
  const roster=Object.freeze(parsed); generated.set(roster,Object.freeze(identity));return roster;
}
export function validateOwnedWorkspaceRoster(fixture:OwnedFixture):void {
  const roster=fixture.ownedWorkspaces;
  requireFact(!current.has(fixture)||roster,'ownership-mismatch','Workspace roster authority was removed');
  if(!roster)return;
  const owner=generated.get(roster);
  requireFact(owner&&Object.entries(owner).every(([key,value])=>fixture[key as keyof FixtureAuthorityOwner]===value),
    'ownership-mismatch','Workspace roster was not created by the owning fixture');
  requireFact(!current.has(fixture)||current.get(fixture)===roster,'ownership-mismatch','Workspace roster is stale or replaced');
  requireFact(roster.some(record=>record.workspaceId===fixture.workspaceId&&record.repo===fixture.repo),
    'ownership-mismatch','Workspace roster omits the primary owned source');
  current.set(fixture,roster);
}
export function requireOwnedWorkspace(fixture:OwnedFixture,workspaceId:string,agentId?:string):{workspaceId:string;repo:string;commonDir?:string} {
  validateOwnedWorkspaceRoster(fixture);
  if(!fixture.ownedWorkspaces) {
    requireFact(workspaceId===fixture.workspaceId,'ownership-mismatch','Workspace is not owned by this fixture');
    return {workspaceId,repo:fixture.repo};
  }
  const record=fixture.ownedWorkspaces.find(record=>record.workspaceId===workspaceId);
  requireFact(record&&(!agentId||record.agentIds.includes(agentId)),'ownership-mismatch','Workspace or agent is absent from owned creation records');
  return record;
}

/** Resolves a requested new actor only through the provisioner's fixed private
 * store port, never an ambient/global API listing. Enrollment grants no effect. */
export async function enrollOwnedWorkspaceAgent(fixture:OwnedFixture,workspaceId:string,agentId:string,signal:AbortSignal,store:EvidenceStore):Promise<void> {
  const workspace=requireOwnedWorkspace(fixture,workspaceId);
  const roster=fixture.ownedWorkspaces;
  requireFact(roster&&fixture.readWorkspaceAgent,'unsupported-capability','Owned workspace enrollment is unavailable');
  const record=roster.find(record=>record.workspaceId===workspaceId)!;
  if(record.agentIds.includes(agentId))return;
  signal.throwIfAborted();await fixture.verify(signal);
  const fact=WorkspaceAgentFact.parse(await fixture.readWorkspaceAgent(workspaceId,agentId,signal));
  requireFact(fact.agentId===agentId&&fact.workspaceId===workspace.workspaceId&&fact.repo===workspace.repo&&
    fact.commonDir===record.commonDir&&fact.storeId===record.storeId&&fact.storeGeneration===record.storeGeneration&&
    Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>fact[key as keyof FixtureAuthorityOwner]===value),
    'identity-mismatch','Requested actor belongs to another owned workspace/store');
  if(fact.parentAgentId!==null) {
    requireFact(record.agentIds.includes(fact.parentAgentId),'ownership-mismatch','Requested actor parent is not enrolled');
    const parent=WorkspaceAgentFact.parse(await fixture.readWorkspaceAgent(workspaceId,fact.parentAgentId,signal));
    requireFact(parent.agentId===fact.parentAgentId&&parent.workspaceId===workspaceId&&parent.repo===record.repo&&
      parent.commonDir===record.commonDir&&parent.storeId===record.storeId&&parent.storeGeneration===record.storeGeneration&&
      Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>parent[key as keyof FixtureAuthorityOwner]===value)&&
      fact.rootAgentId===(parent.rootAgentId??parent.agentId),'identity-mismatch','Requested actor lineage differs from owned parent');
  }
  signal.throwIfAborted();await fixture.verify(signal);
  requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during enrollment');
  const serialized=JSON.stringify(fact);
  requireFact(redact(serialized,fixture.secrets)===serialized,'observation-failed','Agent enrollment fact contains private material');
  const receipt=await store.retain(serialized);
  const next=await createOwnedWorkspaceRoster(fixture,roster.map(value=>({...value,agentIds:[...value.agentIds,...(value===record?[agentId]:[])],
    enrollmentReceipts:[...value.enrollmentReceipts,...(value===record?[receipt]:[])]})),store);
  requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during enrollment');
  signal.throwIfAborted();
  fixture.ownedWorkspaces=next;current.set(fixture,next);
}

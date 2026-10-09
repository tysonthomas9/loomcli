import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import { z } from 'zod';
import { ArtifactRefSchema } from '@tysonthomas9/aft/types';
import { Id, requireFact, sha256, redact } from './protocol.js';
import type { EvidenceStore } from './evidence.js';
import { fixtureOwnerIdentity, type FixtureAuthorityOwner } from './authority.js';
import type { OwnedFixture } from './ownership.js';

export const WorkspaceIdentityKind=z.enum(['native-agent-id','legacy-agent-name']);
export type WorkspaceIdentityKind=z.infer<typeof WorkspaceIdentityKind>;
/** Actual workspace-scoped Name and physical source/commonDir associations. */
export const WorkspaceRepositoryFact=z.object({repoName:Id,sourceRepoId:Id,repo:Id,commonDir:Id,groups:z.array(Id).max(32)}).strict();
const AgentSource=z.object({agentId:Id,repoNames:z.array(Id).min(1).max(32)}).strict();
const Fields = { identityKind:WorkspaceIdentityKind,workspaceId:Id,repo:Id,commonDir:Id,storeId:Id,storeGeneration:Id,
  agentIds:z.array(Id).max(1000),repositories:z.array(WorkspaceRepositoryFact).min(1).max(32).optional(),
  agentSources:z.array(AgentSource).max(1000).optional() };
export const WorkspaceCreationFact = z.object({kind:z.literal('workspace-created'),leaseId:Id,runId:Id,suiteId:Id,
  scope:z.enum(['suite','case']),caseId:Id,profile:Id,...Fields}).strict();
const EmptyCreationFields=WorkspaceCreationFact.pick({leaseId:true,runId:true,suiteId:true,scope:true,caseId:true,profile:true,
  workspaceId:true,storeId:true,storeGeneration:true}).shape;
/** The successful initial empty response has no physical repository authority. */
export const WorkspaceEmptyCreationFact=z.object({kind:z.literal('workspace-created-empty'),...EmptyCreationFields,
  identityKind:z.literal('native-agent-id'),httpStatus:z.literal(201),
  repositories:z.array(WorkspaceRepositoryFact).length(0),agentIds:z.array(Id).length(0)}).strict();
export type WorkspaceEmptyCreationFact=z.infer<typeof WorkspaceEmptyCreationFact>;
/** The separate repository-addition response links its exact retained empty creation. */
export const WorkspaceRepositoriesAddedFact=z.object({...WorkspaceCreationFact.omit({kind:true}).shape,
  kind:z.literal('workspace-repositories-added'),identityKind:z.literal('native-agent-id'),httpStatus:z.literal(201),
  repositories:z.array(WorkspaceRepositoryFact).min(1).max(32),agentIds:z.array(Id).length(0),
  agentSources:z.array(AgentSource).length(0),initialCreationReceipt:ArtifactRefSchema}).strict();
export type WorkspaceRepositoriesAddedFact=z.infer<typeof WorkspaceRepositoriesAddedFact>;
export const FinalWorkspaceCreationFact=z.discriminatedUnion('kind',[WorkspaceCreationFact,WorkspaceRepositoriesAddedFact]);
export type FinalWorkspaceCreationFact=z.infer<typeof FinalWorkspaceCreationFact>;
export const WorkspaceAgentFact = z.object({kind:z.literal('agent-enrolled'),identityKind:z.literal('native-agent-id'),leaseId:Id,runId:Id,suiteId:Id,
  scope:z.enum(['suite','case']),caseId:Id,profile:Id,workspaceId:Id,agentId:Id,repo:Id,commonDir:Id,storeId:Id,storeGeneration:Id,
  parentAgentId:Id.nullable(),rootAgentId:Id.nullable(),createdByKind:z.enum(['user','agent']),createdById:Id.nullable(),
  revision:z.number().int().nonnegative()}).strict();
export type WorkspaceAgentFact=z.infer<typeof WorkspaceAgentFact>;
export const LegacyWorkspaceAgentFact = z.object({kind:z.literal('legacy-agent-enrolled'),identityKind:z.literal('legacy-agent-name'),
  leaseId:Id,runId:Id,suiteId:Id,scope:z.enum(['suite','case']),caseId:Id,profile:Id,workspaceId:Id,name:Id,repo:Id.nullable(),commonDir:Id.nullable(),
  storeId:Id,storeGeneration:Id,parentName:Id.nullable(),createdAt:Id,updatedAt:Id,
  assignedRepos:z.array(Id).max(32).optional(),assignedRepoGroups:z.array(Id).max(32).optional()}).strict()
  .refine(fact=>(fact.repo===null)===(fact.commonDir===null),'Legacy physical source must be an explicit selected or unselected pair');
export type LegacyWorkspaceAgentFact=z.infer<typeof LegacyWorkspaceAgentFact>;
const ActorFact=z.union([WorkspaceAgentFact,LegacyWorkspaceAgentFact]);
const RecordSchema = z.object({...Fields,creationReceipt:ArtifactRefSchema,enrollmentReceipts:z.array(ArtifactRefSchema).max(1000).default([])}).strict();
export type OwnedWorkspaceRecord = Readonly<Omit<z.infer<typeof RecordSchema>,'agentIds'|'enrollmentReceipts'|'repositories'|'agentSources'> & {
  readonly agentIds:readonly string[];readonly enrollmentReceipts:readonly z.infer<typeof ArtifactRefSchema>[];
  readonly repositories?:readonly Readonly<Omit<z.infer<typeof WorkspaceRepositoryFact>,'groups'> & {groups:readonly string[]}>[];
  readonly agentSources?:readonly Readonly<{agentId:string;repoNames:readonly string[]}>[];
}>;
export type OwnedWorkspaceRoster = readonly OwnedWorkspaceRecord[];
const generated = new WeakMap<object,Readonly<FixtureAuthorityOwner>>();
const current = new WeakMap<OwnedFixture,OwnedWorkspaceRoster>();
type Topology=Pick<OwnedWorkspaceRecord,'identityKind'|'repo'|'commonDir'|'agentIds'|'repositories'|'agentSources'>;
/** Pure source policy over retained topology; it neither enrolls an actor nor
 * grants access to a workspace, repository or selected physical worktree. */
export function resolveLegacyRepositoryAssignments(
  repositories:NonNullable<OwnedWorkspaceRecord['repositories']>,
  assignedRepos:readonly string[]|undefined,assignedRepoGroups:readonly string[]|undefined,
):readonly string[] {
  requireFact(repositories.length>0&&repositories.length<=32&&new Set(repositories.map(row=>row.repoName)).size===repositories.length,
    'ownership-mismatch','Legacy repository topology is missing or ambiguous');
  requireFact(assignedRepos!==undefined&&assignedRepoGroups!==undefined&&assignedRepos.length<=32&&assignedRepoGroups.length<=32&&
    new Set(assignedRepos).size===assignedRepos.length&&new Set(assignedRepoGroups).size===assignedRepoGroups.length,
    'incomplete-pages','Actual legacy repository assignments are missing or duplicated');
  // Source WorkspaceAgentInfo uses names and groups. Only BOTH empty grants
  // the complete finite owned set (gitops.go), never a missing observation.
  const names=assignedRepos.length||assignedRepoGroups.length
    ? [...new Set([...assignedRepos,...repositories.filter(repository=>repository.groups.some(group=>assignedRepoGroups.includes(group))).map(repository=>repository.repoName)])]
    : repositories.map(repository=>repository.repoName);
  requireFact(names.length>0&&names.every(name=>repositories.some(repository=>repository.repoName===name)),
    'ownership-mismatch','Legacy actor targets a foreign repository or source');
  return Object.freeze(names);
}
function validateTopology(record:Topology):void {
  requireFact((record.repositories===undefined)===(record.agentSources===undefined),
    'ownership-mismatch','Named repository topology requires actor source associations');
  if(!record.repositories)return;
  const names=new Set<string>(),paths=new Set<string>();
  for(const repository of record.repositories) {
    requireFact(!names.has(repository.repoName)&&!paths.has(repository.repo)&&new Set(repository.groups).size===repository.groups.length,
      'ownership-mismatch','Repository name or physical source is ambiguous');
    names.add(repository.repoName);paths.add(repository.repo);
  }
  requireFact(record.repositories.filter(repository=>repository.repo===record.repo&&repository.commonDir===record.commonDir).length===1,
    'ownership-mismatch','Primary physical source is absent from named topology');
  requireFact(isDeepStrictEqual(record.agentSources!.map(source=>source.agentId),record.agentIds),
    'ownership-mismatch','Actor source associations differ from enrolled membership');
  for(const source of record.agentSources!)requireFact((record.identityKind!=='native-agent-id'||source.repoNames.length===1)&&new Set(source.repoNames).size===source.repoNames.length&&
    source.repoNames.every(name=>names.has(name)), 'ownership-mismatch','Actor repository membership is foreign or duplicated');
}
function factSources(record:Topology,fact:WorkspaceAgentFact|LegacyWorkspaceAgentFact):string[]|undefined {
  if(!record.repositories) {
    requireFact(fact.repo===record.repo&&fact.commonDir===record.commonDir,'ownership-mismatch','Actor source differs from owned repository');
    return undefined;
  }
  const observed=record.repositories.filter(repository=>repository.repo===fact.repo&&repository.commonDir===fact.commonDir);
  if(fact.kind==='agent-enrolled') {
    requireFact(observed.length===1,'ownership-mismatch','Actor physical source has no exact owned repository');
    return [observed[0]!.repoName];
  }
  const names=resolveLegacyRepositoryAssignments(record.repositories,fact.assignedRepos,fact.assignedRepoGroups);
  // Membership does not select a physical worktree. The topology anchor belongs
  // to the workspace, and cannot stand in for an actor's observed source.
  if(fact.repo!==null)requireFact(observed.length===1&&names.includes(observed[0]!.repoName),
    'ownership-mismatch','Legacy selected physical source is not an assigned owned repository');
  return [...names];
}
async function readWorkspaceOwnershipReceipt(receipt:z.infer<typeof ArtifactRefSchema>,store:EvidenceStore):Promise<unknown> {
  ArtifactRefSchema.parse(receipt);
  requireFact(receipt.bytes>0&&receipt.bytes<=4_000_000,'incomplete-pages','Repository creation receipt exceeds the evidence bound');
  const filename=await store.resolveBounded(receipt,4_000_000),handle=await open(filename,constants.O_RDONLY|constants.O_NOFOLLOW);
  let bytes:Buffer;
  try {
    const before=await handle.stat();
    requireFact(before.isFile()&&before.nlink===1&&before.size===receipt.bytes,'identity-mismatch','Repository creation receipt changed');
    bytes=Buffer.alloc(receipt.bytes);let offset=0;
    while(offset<bytes.length) {
      const read=await handle.read(bytes,offset,bytes.length-offset,offset);
      requireFact(read.bytesRead>0,'identity-mismatch','Repository creation receipt is incomplete');offset+=read.bytesRead;
    }
    const extra=await handle.read(Buffer.alloc(1),0,1,bytes.length),after=await handle.stat();
    requireFact(extra.bytesRead===0&&before.dev===after.dev&&before.ino===after.ino&&before.size===after.size&&
      await sha256(bytes)===receipt.sha256,'identity-mismatch','Repository creation receipt bytes changed');
  } finally {await handle.close();}
  await store.resolveBounded(receipt,4_000_000);
  const serialized=bytes.toString('utf8');
  requireFact(redact(serialized)===serialized,'observation-failed','Repository creation receipt contains private material');
  return JSON.parse(serialized) as unknown;
}
/** Authenticate both actual responses in the same owning evidence store. This
 * validates retained facts only; the fixture producer owns dispatch, transition
 * reservation, current store/topology rereads and uncertain-outcome cleanup. */
export async function readWorkspaceRepositoriesAddedFact(owner:FixtureAuthorityOwner,receipt:z.infer<typeof ArtifactRefSchema>,store:EvidenceStore):Promise<WorkspaceRepositoriesAddedFact> {
  const identity=fixtureOwnerIdentity(owner);
  const final=WorkspaceRepositoriesAddedFact.parse(await readWorkspaceOwnershipReceipt(receipt,store));
  const initial=WorkspaceEmptyCreationFact.parse(await readWorkspaceOwnershipReceipt(final.initialCreationReceipt,store));
  requireFact(Object.entries(identity).every(([key,value])=>final[key as keyof FixtureAuthorityOwner]===value&&initial[key as keyof FixtureAuthorityOwner]===value)&&
    ['identityKind','workspaceId','storeId','storeGeneration'].every(key=>initial[key as keyof WorkspaceEmptyCreationFact]===final[key as keyof WorkspaceRepositoriesAddedFact]),
    'ownership-mismatch','Repository addition belongs to another empty workspace creation or owning store');
  validateTopology(final);
  requireFact(new Set(final.repositories.map(repository=>repository.sourceRepoId)).size===final.repositories.length,
    'ownership-mismatch','Repository addition contains duplicate actual source identities');
  return final;
}
/** Trusted finite fixture setup calls this only after actual owned creation.
 * A listing or requested identifier cannot replace the retained creation fact. */
export async function createOwnedWorkspaceRoster(owner:FixtureAuthorityOwner,records:readonly (z.input<typeof RecordSchema>|OwnedWorkspaceRecord)[],store:EvidenceStore):Promise<OwnedWorkspaceRoster> {
  const identity=fixtureOwnerIdentity(owner);
  requireFact(records.length>0&&records.length<=32,'ownership-mismatch','Owned workspace roster exceeds bound');
  const workspaceIds=new Set<string>(); const agentIds=new Set<string>();
  const parsed=[];
  for(const input of records) {
    const record=RecordSchema.parse(input);
    validateTopology(record);
    const workspaceKey=JSON.stringify([record.workspaceId,record.identityKind]);
    requireFact(!workspaceIds.has(workspaceKey),'ownership-mismatch','Duplicate owned workspace/store identity kind');workspaceIds.add(workspaceKey);
    for(const agentId of record.agentIds) {
      const key=JSON.stringify([record.identityKind,record.storeId,record.storeGeneration,record.identityKind==='legacy-agent-name'?record.workspaceId:null,agentId]);
      requireFact(!agentIds.has(key),'ownership-mismatch','Duplicate owned agent membership');agentIds.add(key);
    }
    const receiptFact=(receipt:z.infer<typeof ArtifactRefSchema>)=>readWorkspaceOwnershipReceipt(receipt,store);
    let fact=FinalWorkspaceCreationFact.parse(await receiptFact(record.creationReceipt));
    if(fact.kind==='workspace-repositories-added')fact=await readWorkspaceRepositoriesAddedFact(owner,record.creationReceipt,store);
    validateTopology(fact);
    const {creationReceipt,enrollmentReceipts,...fields}=record;
    const sameIdentity=(fact:WorkspaceAgentFact|LegacyWorkspaceAgentFact|FinalWorkspaceCreationFact)=>Object.entries(identity).every(([key,value])=>fact[key as keyof typeof identity]===value);
    requireFact(sameIdentity(fact)&&Object.entries(fields).every(([key,value])=>['agentIds','agentSources'].includes(key)||isDeepStrictEqual(fact[key as keyof typeof fields],value)),
      'ownership-mismatch','Workspace creation receipt belongs to another fixture or source');
    const members=[...fact.agentIds];
    const sources=fact.agentSources?.map(source=>({...source,repoNames:[...source.repoNames]}));
    for(const receipt of enrollmentReceipts) {
      const enrolled=ActorFact.parse(await receiptFact(receipt));
      const actor=enrolled.kind==='agent-enrolled'?enrolled.agentId:enrolled.name;
      requireFact(sameIdentity(enrolled)&&['identityKind','workspaceId','storeId','storeGeneration'].every(key=>enrolled[key as keyof typeof enrolled]===fields[key as keyof typeof fields])&&
        !members.includes(actor),'ownership-mismatch','Agent enrollment receipt is foreign or duplicated');
      const repoNames=factSources(record,enrolled);
      if(enrolled.kind==='agent-enrolled') {
        if(enrolled.parentAgentId===null)requireFact(enrolled.rootAgentId===null&&enrolled.createdByKind==='user',
          'identity-mismatch','Enrolled root actor is invalid');
        else requireFact(members.includes(enrolled.parentAgentId)&&enrolled.createdByKind==='agent'&&enrolled.createdById===enrolled.parentAgentId&&enrolled.rootAgentId!==null&&members.includes(enrolled.rootAgentId),
          'identity-mismatch','Enrolled agent lineage is not owned');
      } else requireFact(enrolled.parentName===null||members.includes(enrolled.parentName),'identity-mismatch','Enrolled legacy parent is not owned');
      members.push(actor);
      if(repoNames)sources!.push({agentId:actor,repoNames});
    }
    requireFact(JSON.stringify(members)===JSON.stringify(fields.agentIds),'ownership-mismatch','Agent membership differs from retained creation facts');
    requireFact(isDeepStrictEqual(sources,record.agentSources),'ownership-mismatch','Actor source associations differ from retained enrollment facts');
    parsed.push(Object.freeze({...fields,creationReceipt:Object.freeze({...creationReceipt}),enrollmentReceipts:Object.freeze(enrollmentReceipts.map(value=>Object.freeze({...value}))),agentIds:Object.freeze([...record.agentIds]),
      ...(record.repositories?{repositories:Object.freeze(record.repositories.map(value=>Object.freeze({...value,groups:Object.freeze([...value.groups])}))),
        agentSources:Object.freeze(record.agentSources!.map(value=>Object.freeze({...value,repoNames:Object.freeze([...value.repoNames])})))}:{})}));
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
/** Topology authority does not choose a repository or authorize an actor. */
export function requireOwnedWorkspaceRecord(fixture:OwnedFixture,workspaceId:string,identityKind:WorkspaceIdentityKind='native-agent-id'):OwnedWorkspaceRecord|undefined {
  validateOwnedWorkspaceRoster(fixture);
  if(!fixture.ownedWorkspaces) {
    requireFact(identityKind==='native-agent-id'&&workspaceId===fixture.workspaceId,'ownership-mismatch','Workspace/store identity kind is not owned by this fixture');
    return undefined;
  }
  const record=fixture.ownedWorkspaces.find(record=>record.workspaceId===workspaceId&&record.identityKind===identityKind);
  requireFact(record,'ownership-mismatch','Workspace/store identity kind is absent from creation records');return record;
}
export function requireOwnedWorkspace(fixture:OwnedFixture,workspaceId:string,agentId?:string,identityKind:WorkspaceIdentityKind='native-agent-id',repoName?:string):{workspaceId:string;repo:string;commonDir?:string;repoName?:string;sourceRepoId?:string} {
  const record=requireOwnedWorkspaceRecord(fixture,workspaceId,identityKind);
  if(!record) {
    requireFact(repoName===undefined,'ownership-mismatch','Named repository source has no retained topology');
    return {workspaceId,repo:fixture.repo};
  }
  requireFact(!agentId||record.agentIds.includes(agentId),'ownership-mismatch','Actor is absent from owned creation records');
  if(!record.repositories) {
    requireFact(repoName===undefined,'ownership-mismatch','Named repository source has no retained topology');return record;
  }
  const sources=agentId?record.agentSources!.find(source=>source.agentId===agentId)!.repoNames:record.repositories.map(repository=>repository.repoName);
  const chosen=repoName??(sources.length===1?sources[0]:undefined);
  requireFact(chosen&&sources.includes(chosen),'ownership-mismatch','Repository selection is missing, ambiguous or foreign to the actor');
  const repository=record.repositories.find(repository=>repository.repoName===chosen)!;
  return {workspaceId,...repository};
}

/** Append only actual retained creation facts; API listings cannot acquire
 * ownership. Replacement is authenticated and rejects concurrent/stale rosters. */
export async function appendCreatedWorkspaces(fixture:OwnedFixture,records:readonly (z.input<typeof RecordSchema>|OwnedWorkspaceRecord)[],signal:AbortSignal,store:EvidenceStore):Promise<void> {
  validateOwnedWorkspaceRoster(fixture);const prior=fixture.ownedWorkspaces;
  requireFact(prior&&records.length>0,'ownership-mismatch','Existing owned roster and created workspace receipts are required');
  signal.throwIfAborted();await fixture.verify(signal);
  const next=await createOwnedWorkspaceRoster(fixture,[...prior,...records],store);
  await fixture.verify(signal);signal.throwIfAborted();
  requireFact(fixture.ownedWorkspaces===prior,'identity-mismatch','Workspace roster changed during creation enrollment');
  fixture.ownedWorkspaces=next;current.set(fixture,next);
}

/** Resolves a requested new actor only through the provisioner's fixed private
 * store port, never an ambient/global API listing. Enrollment grants no effect. */
export async function enrollOwnedWorkspaceAgent(fixture:OwnedFixture,workspaceId:string,agentId:string,signal:AbortSignal,store:EvidenceStore):Promise<void> {
  requireOwnedWorkspaceRecord(fixture,workspaceId);
  const roster=fixture.ownedWorkspaces;
  requireFact(roster&&fixture.readWorkspaceAgent,'unsupported-capability','Owned workspace enrollment is unavailable');
  const record=roster.find(record=>record.workspaceId===workspaceId&&record.identityKind==='native-agent-id')!;
  if(record.agentIds.includes(agentId))return;
  signal.throwIfAborted();await fixture.verify(signal);
  const fact=WorkspaceAgentFact.parse(await fixture.readWorkspaceAgent(workspaceId,agentId,signal));
  requireFact(fact.agentId===agentId&&fact.workspaceId===workspaceId&&fact.storeId===record.storeId&&fact.storeGeneration===record.storeGeneration&&
    Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>fact[key as keyof FixtureAuthorityOwner]===value),
    'identity-mismatch','Requested actor belongs to another owned workspace/store');
  const repoNames=factSources(record,fact);
  if(fact.parentAgentId!==null) {
    requireFact(record.agentIds.includes(fact.parentAgentId),'ownership-mismatch','Requested actor parent is not enrolled');
    const parentSource=requireOwnedWorkspace(fixture,workspaceId,fact.parentAgentId);
    const parent=WorkspaceAgentFact.parse(await fixture.readWorkspaceAgent(workspaceId,fact.parentAgentId,signal));
    requireFact(parent.agentId===fact.parentAgentId&&parent.workspaceId===workspaceId&&parent.repo===parentSource.repo&&
      parent.commonDir===parentSource.commonDir&&parent.storeId===record.storeId&&parent.storeGeneration===record.storeGeneration&&
      Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>parent[key as keyof FixtureAuthorityOwner]===value)&&
      fact.rootAgentId===(parent.rootAgentId??parent.agentId),'identity-mismatch','Requested actor lineage differs from owned parent');
  }
  signal.throwIfAborted();await fixture.verify(signal);
  requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during enrollment');
  const serialized=JSON.stringify(fact);
  requireFact(redact(serialized,fixture.secrets)===serialized,'observation-failed','Agent enrollment fact contains private material');
  const receipt=await store.retain(serialized);
  const next=await createOwnedWorkspaceRoster(fixture,roster.map(value=>({...value,agentIds:[...value.agentIds,...(value===record?[agentId]:[])],
    enrollmentReceipts:[...value.enrollmentReceipts,...(value===record?[receipt]:[])],
    ...(value.agentSources?{agentSources:[...value.agentSources,...(value===record?[{agentId,repoNames:repoNames!}]:[])]}:{})})),store);
  requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during enrollment');
  signal.throwIfAborted();
  fixture.ownedWorkspaces=next;current.set(fixture,next);
}

/** Legacy domain.Agent names are workspace-scoped and never native IDs. No
 * native creator/root values are synthesized from legacy Parent observations. */
export async function enrollOwnedLegacyAgent(fixture:OwnedFixture,workspaceId:string,name:string,signal:AbortSignal,store:EvidenceStore):Promise<void> {
  requireOwnedWorkspaceRecord(fixture,workspaceId,'legacy-agent-name');
  const roster=fixture.ownedWorkspaces;
  requireFact(roster&&fixture.readWorkspaceLegacyAgent,'unsupported-capability','Owned legacy store enrollment is unavailable');
  const record=roster.find(value=>value.workspaceId===workspaceId&&value.identityKind==='legacy-agent-name')!;
  const existing=record.agentIds.includes(name);
  // A workspace-scoped name can be deleted and reused. Membership alone does
  // not attest the current incarnation, and current API data cannot replace a
  // missing original creation receipt. This local index grants no authority.
  const originals=new Map<string,LegacyWorkspaceAgentFact>();
  for(const receipt of record.enrollmentReceipts) {
    signal.throwIfAborted();
    requireFact(receipt.bytes>0&&receipt.bytes<=4_000_000,'incomplete-pages','Legacy enrollment receipt exceeds the evidence bound');
    const file=await store.resolveBounded(receipt,4_000_000),handle=await open(file,constants.O_RDONLY|constants.O_NOFOLLOW);
    let bytes:Buffer;
    try {
      const before=await handle.stat();
      requireFact(before.isFile()&&before.nlink===1&&before.size===receipt.bytes,'identity-mismatch','Legacy enrollment receipt changed');
      bytes=Buffer.alloc(receipt.bytes);let offset=0;
      while(offset<bytes.length) {
        signal.throwIfAborted();const read=await handle.read(bytes,offset,bytes.length-offset,offset);
        requireFact(read.bytesRead>0,'identity-mismatch','Legacy enrollment receipt is incomplete');offset+=read.bytesRead;
      }
      const extra=await handle.read(Buffer.alloc(1),0,1,bytes.length),after=await handle.stat();
      requireFact(extra.bytesRead===0&&before.dev===after.dev&&before.ino===after.ino&&before.size===after.size&&
        await sha256(bytes)===receipt.sha256,'identity-mismatch','Legacy enrollment receipt bytes changed');
    } finally {await handle.close();}
    await store.resolveBounded(receipt,4_000_000);
    const serialized=bytes.toString('utf8');
    requireFact(redact(serialized,fixture.secrets)===serialized,'observation-failed','Legacy enrollment receipt contains private material');
    const original=LegacyWorkspaceAgentFact.parse(JSON.parse(serialized));
    requireFact(!originals.has(original.name)&&record.agentIds.includes(original.name)&&
      ['workspaceId','storeId','storeGeneration'].every(key=>original[key as keyof typeof original]===record[key as keyof typeof record])&&
      Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>original[key as keyof FixtureAuthorityOwner]===value),
      'identity-mismatch','Retained legacy enrollment identity is foreign or duplicated');
    factSources(record,original);originals.set(original.name,original);
  }
  if(existing)requireFact(originals.has(name),'incomplete-pages','Legacy actor has no retained creation identity');
  const sameCreation=(before:LegacyWorkspaceAgentFact,after:LegacyWorkspaceAgentFact)=>
    ['identityKind','workspaceId','storeId','storeGeneration','name','createdAt','parentName'].every(key=>
      before[key as keyof typeof before]===after[key as keyof typeof after])&&
    Object.keys(fixtureOwnerIdentity(fixture)).every(key=>before[key as keyof FixtureAuthorityOwner]===after[key as keyof FixtureAuthorityOwner]);
  const read=async(actor:string)=>{
    signal.throwIfAborted();
    const fact=LegacyWorkspaceAgentFact.parse(await fixture.readWorkspaceLegacyAgent!(workspaceId,actor,signal));
    requireFact(fact.name===actor&&['workspaceId','storeId','storeGeneration'].every(key=>fact[key as keyof typeof fact]===record[key as keyof typeof record])&&
      Object.entries(fixtureOwnerIdentity(fixture)).every(([key,value])=>fact[key as keyof FixtureAuthorityOwner]===value),
      'identity-mismatch','Legacy actor belongs to another workspace/store');
    const repoNames=factSources(record,fact);
    if(record.agentIds.includes(actor)) {
      const original=originals.get(actor);
      requireFact(original,'incomplete-pages','Legacy actor has no retained creation identity');
      requireFact(sameCreation(original,fact),'identity-mismatch','Legacy actor creation identity changed');
      const retainedSources=record.agentSources?.find(source=>source.agentId===actor)?.repoNames;
      requireFact(!repoNames||retainedSources&&repoNames.every(repo=>retainedSources.includes(repo)),
        'ownership-mismatch','Current legacy assignments exceed retained source authority');
    }
    return fact;
  };
  await fixture.verify(signal);
  const fact=await read(name);const repoNames=factSources(record,fact);const seen=new Set([name]),observed=[fact];let parent=fact.parentName;
  while(parent!==null) {
    requireFact(seen.size<1000&&!seen.has(parent)&&record.agentIds.includes(parent),'identity-mismatch','Legacy parent lineage is foreign, cyclic or exceeds bound');
    seen.add(parent);const value=await read(parent);observed.push(value);parent=value.parentName;
  }
  // Re-read immutable identities across the bounded observation. Mutable
  // updatedAt/model/assignment values are not creation identity.
  for(const before of observed)requireFact(sameCreation(before,await read(before.name)),
    'identity-mismatch','Legacy actor creation identity changed during observation');
  await fixture.verify(signal);signal.throwIfAborted();
  requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during legacy enrollment');
  if(existing)return;
  const serialized=JSON.stringify(fact);
  requireFact(redact(serialized,fixture.secrets)===serialized,'observation-failed','Legacy enrollment contains private material');
  const receipt=await store.retain(serialized);
  const next=await createOwnedWorkspaceRoster(fixture,roster.map(value=>({...value,agentIds:[...value.agentIds,...(value===record?[name]:[])],
    enrollmentReceipts:[...value.enrollmentReceipts,...(value===record?[receipt]:[])],
    ...(value.agentSources?{agentSources:[...value.agentSources,...(value===record?[{agentId:name,repoNames:repoNames!}]:[])]}:{})})),store);
  signal.throwIfAborted();requireFact(fixture.ownedWorkspaces===roster,'identity-mismatch','Workspace roster changed during legacy enrollment');
  fixture.ownedWorkspaces=next;current.set(fixture,next);
}

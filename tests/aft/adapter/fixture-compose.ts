import { z } from 'zod';
import { isDeepStrictEqual } from 'node:util';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ArtifactRefSchema,ClockSchema,ProvenanceSchema,JsonValueSchema,type CapabilityEffect } from '@tysonthomas9/aft/types';
import { Id,Digest,requireFact,redact } from './protocol.js';
import { RedactionFacts } from './redaction.js';
import { type OwnedFixture } from './ownership.js';
import { fixtureOwnerIdentity } from './authority.js';
import { readFixtureArtifact } from './evidence.js';
import { beginFixtureOperation } from './native-operation-authority.js';

export const FixtureComposeServeId='loom.fixture.observeComposeServe' as const;
export const RestartComposeServeId='loom.runtime.restartComposeServe' as const;
// Verification writes lifecycle artifacts; fixed inspection helpers may be
// terminated on overflow. Restart additionally owns its transition artifacts.
export const FixtureComposeServeEffects=Object.freeze(['read-filesystem','start-owned-process',
  'write-fixture','stop-owned-process'] as const satisfies readonly CapabilityEffect[]);
export const RestartComposeServeEffects=Object.freeze(['read-api','read-filesystem','start-owned-process',
  'stop-owned-process','restart-owned-service','external-provider','write-fixture'] as const satisfies readonly CapabilityEffect[]);
export const ComposeContainerIdentity=z.object({containerId:Id,initPid:z.number().int().positive(),startedAt:Id,generation:Id}).strict();
export const ComposeProcessInventory=z.object({text:z.string().max(4*1024*1024),complete:z.literal(true),redaction:RedactionFacts}).strict();
export const ComposeServeTarget=z.object({fixtureLeaseId:Id,kind:z.literal('compose-container'),service:z.literal('loom-local'),
  scope:z.literal('loom-local-plus-OpenCode'),project:Id,fixtureRunId:Id,container:ComposeContainerIdentity,
  imageId:z.string().regex(/^sha256:[a-f0-9]{64}$/),namespaceSha256:Digest,processInventory:ComposeProcessInventory}).strict();
export type ComposeServeTarget=z.infer<typeof ComposeServeTarget>;
export const FixtureComposeServeInput=z.object({leaseId:Id}).strict();
export const FixtureComposeServeOutput=z.object({target:ComposeServeTarget,targetReceipt:ArtifactRefSchema}).strict();
export const RestartComposeServeInput=z.object({leaseId:Id,targetReceipt:ArtifactRefSchema}).strict();
export type RestartComposeServeInput=z.infer<typeof RestartComposeServeInput>;
const ClockPolicy=z.object({unit:z.literal('integer-seconds'),deadlineEvaluation:z.literal('after-failed-request'),
  windowSeconds:z.literal(180),requestTimeoutMs:z.literal(3000),cadenceMs:z.literal(1000)}).strict();
const Readiness=z.object({path:z.literal('/api/config'),status:z.number().int().min(200).max(399),complete:z.literal(true),
  attempts:z.number().int().positive().max(181),elapsedMs:z.number().finite().nonnegative(),clockPolicy:ClockPolicy,
  startedAtSeconds:z.number().int().nonnegative(),deadlineAtSeconds:z.number().int().nonnegative(),
  completedAtSeconds:z.number().int().nonnegative()}).strict()
  .refine(value=>value.deadlineAtSeconds===value.startedAtSeconds+180&&value.completedAtSeconds>=value.startedAtSeconds,
    'Readiness clock facts are inconsistent');
export const ComposeRestartFacts=z.object({fixtureLeaseId:Id,scope:z.literal('loom-local-plus-OpenCode'),
  before:ComposeContainerIdentity,after:ComposeContainerIdentity,
  successor:z.object({predecessorContainerId:Id,containerIdChanged:z.boolean(),namespaceSha256:Digest}).strict(),
  readiness:Readiness,processInventory:z.object({before:ComposeProcessInventory,after:ComposeProcessInventory}).strict(),
  sourceRestrictions:z.object({dispatchTimeoutMs:z.literal(60000),maximumReadinessBodyBytes:z.literal(4194304)}).strict(),
}).strict();
export type ComposeRestartFacts=z.infer<typeof ComposeRestartFacts>;
export const RestartComposeServeOutput=ComposeRestartFacts.extend({receipt:ArtifactRefSchema}).strict();
const Owner=z.object({runId:Id,suiteId:Id,caseId:Id,scope:z.enum(['suite','case'])}).strict();
const FixtureOwner=Owner.extend({leaseId:Id,profile:z.literal('agents-real-opencode')}).strict();
const Capture=z.object({kind:z.literal('loom-compose-target-capture'),owner:Owner,fixtureOwner:FixtureOwner,
  provenance:ProvenanceSchema,clock:ClockSchema,target:ComposeServeTarget}).strict();
const owner=(context:CapabilityContext)=>({runId:context.runId,suiteId:context.suiteId,caseId:context.caseId,scope:context.scope});
const maxBytes=4*1024*1024;

function checkTarget(fixture:OwnedFixture,target:ComposeServeTarget) {
  requireFact(target.fixtureLeaseId===fixture.leaseId,'ownership-mismatch','Compose target belongs to another fixture');
  requireFact(redact(JSON.stringify(target),fixture.secrets)===JSON.stringify(target),
    'incomplete-pages','Compose target contains private material');
}
function checkContextOwner(context:CapabilityContext,fixture:OwnedFixture,capture:z.infer<typeof Capture>) {
  const captured=capture.owner;
  requireFact(captured.runId===context.runId&&captured.suiteId===context.suiteId&&
    (captured.scope==='case'?context.scope==='case'&&captured.caseId===context.caseId:
      fixture.scope==='suite'&&captured.caseId===fixture.caseId&&
      (context.scope==='suite'?captured.caseId===context.caseId:context.suite?.id===context.suiteId&&context.suite.handles.includes(fixture.leaseId))),
    'ownership-mismatch','Compose target capture belongs to another owning context');
  requireFact(isDeepStrictEqual(capture.fixtureOwner,fixtureOwnerIdentity(fixture)),
    'ownership-mismatch','Compose target fixture owner changed');
}
function provenance(context:CapabilityContext,fixture:OwnedFixture,implementationSha256:string,evidenceClass:z.infer<typeof ProvenanceSchema>['evidenceClass']) {
  const monoMs=context.clock.now();
  return ProvenanceSchema.parse({source:context.source,registrySha256:context.registrySha256,implementationSha256,
    identity:{runId:context.runId,fixtureLeaseId:fixture.leaseId,workspaceId:fixture.workspaceId},
    observedAt:{clockId:context.clock.id,monoMs,utcMs:context.clock.epochUtcMs+monoMs,phase:0},evidenceClass,artifacts:[]});
}
async function retain(fixture:OwnedFixture,value:unknown,guardedRetain:(serialized:string)=>Promise<z.infer<typeof ArtifactRefSchema>>) {
  const bytes=JSON.stringify(JsonValueSchema.parse(value));
  requireFact(Buffer.byteLength(bytes)<=maxBytes,'incomplete-pages','Compose capture exceeds the bounded receipt size');
  requireFact(redact(bytes,fixture.secrets)===bytes,'incomplete-pages','Compose receipt contains private material');
  return guardedRetain(bytes);
}
/** Both operations use the same canonical fixture registry, not a separate
 * target authority map. The supported owning producer must authenticate its
 * exact retained container/marker/namespace and lifecycle receipt under lease. */
export async function observeComposeServe(context:CapabilityContext,input:z.infer<typeof FixtureComposeServeInput>,implementationSha256:string) {
  const guard=beginFixtureOperation(context,input.leaseId,FixtureComposeServeId,FixtureComposeServeEffects),{fixture,grant}=guard;
  requireFact(fixture.profile==='agents-real-opencode','unsupported-capability','Compose route is unavailable');
  const producer=fixture.observeComposeServe;
  requireFact(producer,'unsupported-capability','Owned Compose observation producer is unavailable');
  const recheck=()=>{guard.recheck();requireFact(fixture.observeComposeServe===producer,
    'ownership-mismatch','Compose observation authority changed');};
  const checked=async<T>(call:()=>Promise<T>)=>{recheck();const value=await call();recheck();return value;};
  const retention={recheck,retain:(serialized:string)=>checked(()=>guard.retain(serialized))};
  await checked(()=>guard.verify());context.signal.throwIfAborted();recheck();
  const target=ComposeServeTarget.parse(await checked(()=>producer.call(fixture,context.signal)));
  context.signal.throwIfAborted();recheck();checkTarget(fixture,target);
  const clock=ClockSchema.parse({id:context.clock.id,domain:context.clock.domain,epochUtcMs:context.clock.epochUtcMs,maxErrorMs:context.clock.maxErrorMs});
  const captured=Capture.parse({kind:'loom-compose-target-capture',owner:owner(context),fixtureOwner:fixtureOwnerIdentity(fixture),
    provenance:provenance(context,fixture,implementationSha256,grant.evidenceClass),clock,target});
  const targetReceipt=await retain(fixture,captured,retention.retain);context.signal.throwIfAborted();recheck();
  return {fixture,grant,value:FixtureComposeServeOutput.parse({target,targetReceipt}),retention};
}
export async function restartComposeServe(context:CapabilityContext,input:RestartComposeServeInput,implementationSha256:string) {
  const guard=beginFixtureOperation(context,input.leaseId,RestartComposeServeId,RestartComposeServeEffects),{fixture,grant}=guard;
  requireFact(fixture.profile==='agents-real-opencode','unsupported-capability','Compose route is unavailable');
  const producer=fixture.restartComposeServe;
  requireFact(producer,'unsupported-capability','Owned Compose restart producer is unavailable');
  const recheck=()=>{guard.recheck();requireFact(fixture.restartComposeServe===producer,
    'ownership-mismatch','Compose restart authority changed');};
  const checked=async<T>(call:()=>Promise<T>)=>{recheck();const value=await call();recheck();return value;};
  const retention={recheck,retain:(serialized:string)=>checked(()=>guard.retain(serialized))};
  await checked(()=>guard.verify());context.signal.throwIfAborted();recheck();
  const capture=Capture.parse(await checked(()=>readFixtureArtifact(context,fixture.leaseId,input.targetReceipt,recheck)));
  context.signal.throwIfAborted();recheck();checkContextOwner(context,fixture,capture);checkTarget(fixture,capture.target);
  requireFact(capture.provenance.registrySha256===context.registrySha256&&capture.provenance.implementationSha256===implementationSha256&&
    capture.provenance.identity.runId===context.runId&&capture.provenance.identity.fixtureLeaseId===fixture.leaseId&&
    capture.provenance.identity.workspaceId===fixture.workspaceId&&capture.provenance.observedAt.clockId===capture.clock.id&&
    Math.abs(capture.provenance.observedAt.utcMs-capture.clock.epochUtcMs-capture.provenance.observedAt.monoMs)<=capture.clock.maxErrorMs,
    'identity-mismatch','Compose capture source or pin differs');
  const facts=ComposeRestartFacts.parse(await checked(()=>producer.call(fixture,capture.target,context.signal)));
  context.signal.throwIfAborted();recheck();
  requireFact(facts.fixtureLeaseId===fixture.leaseId&&isDeepStrictEqual(facts.before,capture.target.container)&&
    facts.successor.predecessorContainerId===facts.before.containerId&&
    facts.successor.containerIdChanged===(facts.after.containerId!==facts.before.containerId)&&
    facts.successor.namespaceSha256===capture.target.namespaceSha256,
    'identity-mismatch','Compose restart receipt does not bind the observed target');
  const receipt=await retain(fixture,{kind:'loom-compose-restart-capture',owner:owner(context),fixtureOwner:fixtureOwnerIdentity(fixture),
    targetReceipt:input.targetReceipt,provenance:provenance(context,fixture,implementationSha256,grant.evidenceClass),facts},retention.retain);
  context.signal.throwIfAborted();recheck();
  return {fixture,grant,value:RestartComposeServeOutput.parse({...facts,receipt}),retention};
}

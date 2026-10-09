import { constants } from 'node:fs';
import { open } from 'node:fs/promises';
import { isDeepStrictEqual } from 'node:util';
import { z } from 'zod';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ArtifactRefSchema, ClockSchema, ProvenanceSchema } from '@tysonthomas9/aft/types';
import { AgentRef, Digest, Id, redact, requireFact, sha256 } from './protocol.js';
import { SavedEventsInput, SavedEventsOutput, collectSavedEvents } from './events.js';
import { getAgent } from './ownership.js';
import { getFixtureEvidenceStore } from './evidence.js';
import { getSyntheticProbe } from './synthetic-probe.js';

const Owner=z.object({runId:Id,suiteId:Id,scope:z.enum(['suite','case']),caseId:Id}).strict();
const Capture=z.object({kind:z.literal('loom-saved-events-capture'),owner:Owner,query:SavedEventsInput,
  provenance:ProvenanceSchema,clock:ClockSchema,data:SavedEventsOutput}).strict();
export const SavedCaptureInput=z.object({agent:AgentRef,captureReceipt:ArtifactRefSchema}).strict();
export type SavedCaptureInput=z.infer<typeof SavedCaptureInput>;
const maxBytes=4*1024*1024;
const owner=(context:CapabilityContext)=>({runId:context.runId,suiteId:context.suiteId,scope:context.scope,caseId:context.caseId});

/** Uses the existing fixture evidence store; the receipt is DATA and cannot
 * acquire a fixture, store, browser observation or scope authority. */
export async function retainSavedCapture(context:CapabilityContext,input:z.infer<typeof SavedEventsInput>,
  value:z.infer<typeof SavedEventsOutput>,implementationSha256:string) {
  const {fixture,agent}=await getAgent(context,input.agent),monoMs=context.clock.now();
  requireFact(value.captureReceipt===undefined,'identity-mismatch','Saved capture cannot nest another capture receipt');
  const provenance=ProvenanceSchema.parse({source:context.source,registrySha256:context.registrySha256,
    implementationSha256:Digest.parse(implementationSha256),identity:{runId:context.runId,fixtureLeaseId:fixture.leaseId,
      workspaceId:agent.row.workspace_id,agentId:agent.row.agent_id,repo:agent.row.repo,worktree:agent.row.worktree_path,
      parentAgentId:agent.row.parent_agent_id,rootAgentId:agent.row.root_agent_id,harness:agent.row.harness,
      nativeSessionId:agent.row.harness_session_id,nativeRoot:agent.row.harness_session_root},
    observedAt:{clockId:context.clock.id,monoMs,utcMs:context.clock.epochUtcMs+monoMs,phase:0},
    evidenceClass:fixture.evidenceClass,artifacts:[]});
  const data=SavedEventsOutput.parse(redact(value,fixture.secrets));
  const clock={id:context.clock.id,domain:context.clock.domain,epochUtcMs:context.clock.epochUtcMs,maxErrorMs:context.clock.maxErrorMs};
  const capture=Capture.parse({kind:'loom-saved-events-capture',owner:owner(context),query:input,provenance,clock,data});
  const bytes=JSON.stringify(capture);
  requireFact(redact(bytes,fixture.secrets)===bytes,'observation-failed','Saved capture metadata contains private material');
  requireFact(Buffer.byteLength(bytes)<=maxBytes,'incomplete-pages','Saved capture exceeds its retained byte budget');
  await fixture.verify(context.signal);context.signal.throwIfAborted();
  return getFixtureEvidenceStore(context,fixture.leaseId).retain(bytes);
}

/** Reads exactly the retained byte count with nofollow and no unbounded readFile.
 * The evidence store first requires a known retained ID and checks its digest. */
async function readCapture(filename:string,receipt:z.infer<typeof ArtifactRefSchema>) {
  requireFact(receipt.mediaType==='application/json'&&receipt.redaction==='sanitized'&&receipt.bytes>0,
    'identity-mismatch','Saved capture receipt type differs');
  requireFact(receipt.bytes<=maxBytes,'incomplete-pages','Saved capture exceeds its retained byte budget');
  const file=await open(filename,constants.O_RDONLY|constants.O_NOFOLLOW);
  try {
    const before=await file.stat();requireFact(before.isFile()&&before.nlink===1&&before.size===receipt.bytes,
      'identity-mismatch','Saved capture file is not the retained byte sequence');
    const bytes=Buffer.alloc(receipt.bytes);let offset=0;
    while(offset<bytes.length) {
      const read=await file.read(bytes,offset,bytes.length-offset,offset);
      requireFact(read.bytesRead>0,'incomplete-pages','Saved capture bytes are incomplete');offset+=read.bytesRead;
    }
    const extra=await file.read(Buffer.alloc(1),0,1,bytes.length),after=await file.stat();
    requireFact(extra.bytesRead===0&&before.dev===after.dev&&before.ino===after.ino&&before.size===after.size&&
      await sha256(bytes)===receipt.sha256,'identity-mismatch','Saved capture bytes changed');
    return Capture.parse(JSON.parse(bytes.toString('utf8')));
  } finally {await file.close();}
}

/** Canonical context/lease access precedes artifact access. A case capture
 * stays case-owned even if its fixture is exported from suite setup. */
export async function rereadSavedCapture(context:CapabilityContext,input:SavedCaptureInput,implementationSha256:string) {
  const {fixture,agent}=await getAgent(context,input.agent),store=getFixtureEvidenceStore(context,fixture.leaseId);
  const receipt=ArtifactRefSchema.parse(input.captureReceipt);
  requireFact(receipt.bytes<=maxBytes,'incomplete-pages','Saved capture exceeds its retained byte budget');
  const capture=await readCapture(await store.resolve(receipt.id),receipt),identity=capture.provenance.identity;
  requireFact(capture.owner.runId===context.runId&&capture.owner.suiteId===context.suiteId&&
    (capture.owner.scope==='case'?context.scope==='case'&&capture.owner.caseId===context.caseId:
      context.scope==='suite'?capture.owner.caseId===context.caseId:
        context.suite?.id===context.suiteId&&context.suite.handles.includes(fixture.leaseId)),
    'ownership-mismatch','Saved capture belongs to another owning scope');
  requireFact(capture.provenance.registrySha256===context.registrySha256&&capture.provenance.implementationSha256===implementationSha256&&
    capture.provenance.observedAt.clockId===capture.clock.id&&
    Math.abs(capture.provenance.observedAt.utcMs-capture.clock.epochUtcMs-capture.provenance.observedAt.monoMs)<=capture.clock.maxErrorMs&&
    identity.runId===context.runId&&identity.fixtureLeaseId===fixture.leaseId&&identity.workspaceId===agent.row.workspace_id&&
    identity.agentId===agent.row.agent_id&&identity.repo===agent.row.repo&&identity.worktree===agent.row.worktree_path&&
    identity.parentAgentId===agent.row.parent_agent_id&&identity.rootAgentId===agent.row.root_agent_id&&identity.harness===agent.row.harness&&
    identity.nativeSessionId===agent.row.harness_session_id&&identity.nativeRoot===agent.row.harness_session_root&&
    isDeepStrictEqual(capture.query.agent,input.agent)&&capture.data.captureReceipt===undefined&&
    capture.data.after===capture.query.after&&(capture.query.snapshotSeq===undefined||capture.query.snapshotSeq===capture.data.snapshotSeq),
    'identity-mismatch','Saved capture source, pin, actor or query identity differs');
  const query={...capture.query,snapshotSeq:capture.data.snapshotSeq};
  const probe=query.probeHandle?getSyntheticProbe(fixture,query.probeHandle):undefined;
  await fixture.verify(context.signal);context.signal.throwIfAborted();
  const observed=SavedEventsOutput.parse(redact(await collectSavedEvents(query,fixture.readApi,context.signal,probe,fixture.secrets),fixture.secrets));
  requireFact(isDeepStrictEqual(observed,capture.data),'identity-mismatch','Saved capture payload, cursor or redaction facts changed');
  await fixture.verify(context.signal);context.signal.throwIfAborted();
  const redactionAffected=observed.events.some(event=>event.redaction.omittedPaths.length||event.redaction.replacedTextPaths.length);
  return {data:observed,query,provenance:capture.provenance,clock:capture.clock,captureReceipt:receipt,redactionAffected};
}

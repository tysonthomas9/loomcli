import { z } from 'zod';
import type { CapabilityContext } from '@tysonthomas9/aft/capabilities';
import type { CapabilityEffect } from '@tysonthomas9/aft/types';
import { Id, Digest, ProcessIdentitySchema, requireFact, redact, sha256 } from './protocol.js';
import { RedactionFacts, redactionFacts } from './redaction.js';
import { LegacyWorkspaceAgentFact } from './workspaces.js';
import { RegisteredWorkerFact } from './fixture-workers.js';
import { fixtureOwnerIdentity } from './authority.js';
import { beginFixtureOperation } from './native-operation-authority.js';

export const FixtureWorkerStateId = 'loom.fixture.observeWorkerState' as const;
// Verification retains lifecycle artifacts and may terminate an owned helper
// on overflow. The producer reads API/actor/kernel facts without replaying stop.
export const FixtureWorkerStateEffects = Object.freeze(['read-api','read-filesystem','start-owned-process',
  'write-fixture','stop-owned-process'] as const satisfies readonly CapabilityEffect[]);
const ParentIdentity = z.object({id:Id,pid:z.number().int().positive(),generation:Id}).strict();
/** The owning producer reads this association from its retained launched handle
 * and captured kernel registration. Neither DATA nor schema parsing enrolls a
 * process, and handle/kernel generations may legitimately differ. */
export const WorkerParentBinding = z.object({handle:ParentIdentity,kernel:ParentIdentity}).strict()
  .refine(value=>value.handle.pid===value.kernel.pid,'Parent handle and captured kernel PID differ');
export const WorkerParentBindings = z.object({
  serve:WorkerParentBinding.refine(value=>value.handle.id==='serve','Expected owned serve handle'),
  daemon:WorkerParentBinding.refine(value=>value.handle.id==='daemon','Expected owned daemon handle'),
}).strict().refine(value=>value.serve.kernel.id!==value.daemon.kernel.id&&value.serve.handle.pid!==value.daemon.handle.pid,
  'Parent registrations must refer to distinct owned processes');
export type WorkerParentBindings = z.infer<typeof WorkerParentBindings>;
export const WorkerParentObservationInput = z.object({view:z.literal('parents'),leaseId:Id}).strict();
export const WorkerStateObservationInput = z.object({view:z.literal('state'),leaseId:Id,workspaceId:Id,agentName:Id,workerId:Id,
  expectedWorkerGeneration:Id,parents:WorkerParentBindings}).strict();
export const FixtureWorkerStateInput = z.discriminatedUnion('view',[WorkerParentObservationInput,WorkerStateObservationInput]);
export type FixtureWorkerStateInput = z.infer<typeof FixtureWorkerStateInput>;
export const WorkerStateKernelFact = ProcessIdentitySchema.extend({id:Id,argvSha256:Digest,
  parentPid:z.number().int().nonnegative(),configurationRoot:z.string().min(1).max(4096),
  state:z.enum(['running','exited'])}).strict();
export type WorkerStateKernelFact = z.infer<typeof WorkerStateKernelFact>;
const KernelSample = z.object({serve:WorkerStateKernelFact,daemon:WorkerStateKernelFact,worker:WorkerStateKernelFact}).strict();
export const WorkerStateApiRow = z.object({name:Id,state:Id,desired_state:Id}).strict();
// UTF-16 half-open content boundaries followed by the next line start. Every
// separator remains reconstructable from text; no stdout/stderr concatenation.
const Line = z.tuple([z.number().int().nonnegative(),z.number().int().nonnegative(),z.number().int().nonnegative()]);
export function workerStderrLineRanges(text:string):[number,number,number][] {
  requireFact(text.length<=1_000_000,'incomplete-pages','Stderr snapshot exceeds the public string bound');
  const result:[number,number,number][]=[];let start=0;
  // The source grep oracle separates lines only at LF. CR and Unicode/control
  // separators are actual line content, not invented additional boundaries.
  const separators=/\n/g;
  for(const match of text.matchAll(separators)) {
    requireFact(result.length<10_000,'incomplete-pages','Stderr snapshot exceeds the public line bound');
    result.push([start,match.index,match.index+match[0].length]);start=match.index+match[0].length;
  }
  if(start<text.length){requireFact(result.length<10_000,'incomplete-pages','Stderr snapshot exceeds the public line bound');result.push([start,text.length,text.length]);}
  return result;
}
export const WorkerStateStderr = z.object({channel:z.literal('stderr'),text:z.string().max(1_000_000),
  lines:z.array(Line).max(10_000),redaction:RedactionFacts,
  transportComplete:z.boolean(),overflow:z.boolean(),closed:z.boolean(),prefix:z.boolean(),
  capturedBytes:z.number().int().nonnegative().max(4*1024*1024),
  captureProcess:WorkerStateKernelFact,
  source:z.object({commit:z.string().regex(/^[a-f0-9]{40}$/),sourceManifestSha256:Digest,buildManifestSha256:Digest}).strict(),
}).strict().superRefine((value,context)=>{
  if(value.prefix===value.closed)context.addIssue({code:'custom',message:'Output closure and prefix facts disagree'});
  if(JSON.stringify(value.lines)!==JSON.stringify(workerStderrLineRanges(value.text)))
    context.addIssue({code:'custom',message:'Stderr line boundaries do not match actual text'});
});
/** Pure sanitization over an already captured owning stderr snapshot. The
 * producer must attest its process/source and retain independent error facts. */
export function workerStateStderrFacts(
  snapshot:Omit<z.input<typeof WorkerStateStderr>,'channel'|'lines'|'redaction'|'prefix'>,
  secrets:readonly string[]=[],
):z.infer<typeof WorkerStateStderr> {
  requireFact(snapshot.text.length<=1_000_000,'incomplete-pages','Stderr snapshot exceeds the public string bound');
  const safe=z.string().parse(redact(snapshot.text,secrets));
  requireFact(safe.length<=1_000_000,'incomplete-pages','Stderr snapshot exceeds the public string bound');
  const lines=workerStderrLineRanges(safe);
  requireFact(lines.length<=10_000,'incomplete-pages','Stderr snapshot exceeds the public line bound');
  return WorkerStateStderr.parse({...snapshot,text:safe,channel:'stderr',lines,
    redaction:redactionFacts(snapshot.text,secrets),prefix:!snapshot.closed});
}
export const WorkerParentObservationOutput = z.object({fixtureLeaseId:Id,
  coverage:z.literal('retained-builtin-worker-parent-bindings'),before:WorkerParentBindings,after:WorkerParentBindings}).strict();
export const WorkerStateObservationOutput = z.object({fixtureLeaseId:Id,
  coverage:z.literal('retained-builtin-worker-state'),actor:LegacyWorkspaceAgentFact,worker:RegisteredWorkerFact,
  parents:WorkerParentBindings,before:KernelSample,after:KernelSample,
  api:z.object({httpStatus:z.number().int().min(200).max(299),complete:z.literal(true),
    rows:z.array(WorkerStateApiRow).max(1000),matchedRowCount:z.number().int().nonnegative().max(1000),
    totalRowCount:z.number().int().nonnegative().max(1000)}).strict(),
  stderr:WorkerStateStderr,
}).strict().superRefine((value,context)=>{
  if(value.api.matchedRowCount!==value.api.rows.length||value.api.totalRowCount<value.api.rows.length)
    context.addIssue({code:'custom',message:'API row counts are inconsistent'});
});
export const FixtureWorkerStateOutput = z.union([WorkerParentObservationOutput,WorkerStateObservationOutput]);
export type FixtureWorkerStateOutput = z.infer<typeof FixtureWorkerStateOutput>;

function sameKernel(before:WorkerStateKernelFact,after:WorkerStateKernelFact):boolean {
  const {state:prior,...original}=before,{state:current,...observed}=after;
  return JSON.stringify(original)===JSON.stringify(observed)&&!(prior==='exited'&&current==='running');
}
/** Callback installed only by a supported owning fixture. No lookup by current
 * name/PID, rediscovery, stop retry or new successor enrollment belongs here. */
export async function observeFixtureWorkerState(context:CapabilityContext,input:FixtureWorkerStateInput) {
  const guard=beginFixtureOperation(context,input.leaseId,FixtureWorkerStateId,FixtureWorkerStateEffects),{fixture,grant}=guard;
  const producer=fixture.observeWorkerState;
  requireFact(producer,'unsupported-capability','Owned worker-state producer is unavailable');
  const recheck=()=>{guard.recheck();requireFact(fixture.observeWorkerState===producer,
    'ownership-mismatch','Worker-state producer authority changed');};
  const checked=async<T>(call:()=>Promise<T>)=>{recheck();const value=await call();recheck();return value;};
  const retention={recheck,retain:(serialized:string)=>checked(()=>guard.retain(serialized))};
  await checked(()=>guard.verify());
  const value=FixtureWorkerStateOutput.parse(await checked(()=>producer.call(fixture,input,context.signal)));
  context.signal.throwIfAborted();recheck();
  requireFact(value.fixtureLeaseId===fixture.leaseId,'ownership-mismatch','Worker-state observation belongs to a different fixture');
  if(input.view==='parents') {
    requireFact(value.coverage==='retained-builtin-worker-parent-bindings',
      'identity-mismatch','Parent observation returned another view');
    requireFact(JSON.stringify(value.before)===JSON.stringify(value.after),
      'identity-mismatch','Parent handle or kernel binding changed during observation');
    requireFact(redact(JSON.stringify(value),fixture.secrets)===JSON.stringify(value),
      'incomplete-pages','Parent binding contains private material');
    recheck();context.signal.throwIfAborted();return {fixture,grant,value,retention};
  }
  requireFact(value.coverage==='retained-builtin-worker-state',
    'identity-mismatch','Worker state returned another view');
  requireFact(JSON.stringify(value.parents)===JSON.stringify(input.parents),
    'identity-mismatch','Worker-state handle to kernel bindings differ');
  requireFact(value.fixtureLeaseId===fixture.leaseId&&value.worker.id===input.workerId&&
    value.worker.generation===input.expectedWorkerGeneration&&value.worker.workspaceId===input.workspaceId&&
    value.worker.agentId===input.agentName&&value.actor.workspaceId===input.workspaceId&&value.actor.name===input.agentName&&
    Object.entries(fixtureOwnerIdentity(fixture)).every(([key,expected])=>value.actor[key as keyof typeof value.actor]===expected),
    'identity-mismatch','Worker-state actor or retained target differs');
  for(const sample of [value.before,value.after]) {
    for(const kind of ['serve','daemon'] as const) {
      const kernel=input.parents[kind].kernel;
      requireFact(sample[kind].id===kernel.id&&sample[kind].pid===kernel.pid&&sample[kind].generation===kernel.generation,
        'identity-mismatch','Worker-state captured parent kernel identity differs');
    }
    requireFact(sample.worker.id===input.workerId&&sample.worker.generation===input.expectedWorkerGeneration,
      'identity-mismatch','Worker-state kernel generation differs');
  }
  for(const kind of ['serve','daemon','worker'] as const)requireFact(sameKernel(value.before[kind],value.after[kind]),
    'identity-mismatch','Worker-state kernel identity changed');
  requireFact(sameKernel(value.before.daemon,value.stderr.captureProcess)&&sameKernel(value.stderr.captureProcess,value.after.daemon),
    'identity-mismatch','Stderr snapshot belongs to a different daemon');
  for(const sample of [value.before,value.after])for(const process of Object.values(sample))
    requireFact(await checked(()=>sha256([process.executable,...process.argv].join('\0')+'\0'))===process.argvSha256,
      'identity-mismatch','Worker-state process arguments differ from captured digest');
  requireFact(value.api.rows.every(row=>row.name===input.agentName),'identity-mismatch','Worker-state API rows belong to another actor');
  requireFact(value.stderr.redaction.omittedPaths.length===0&&value.stderr.redaction.replacedTextPaths.length===0&&
    redact(value.stderr.text,fixture.secrets)===value.stderr.text,'incomplete-pages','Redaction prevents exact stderr line evidence');
  requireFact(value.stderr.transportComplete&&!value.stderr.overflow,
    'incomplete-pages','Stderr transport or capacity is incomplete');
  requireFact(Buffer.byteLength(value.stderr.text)===value.stderr.capturedBytes,
    'identity-mismatch','Stderr byte count differs from captured text');
  requireFact(redact(JSON.stringify(value),fixture.secrets)===JSON.stringify(value),
    'incomplete-pages','Worker-state identity contains private material');
  recheck();context.signal.throwIfAborted();
  return {fixture,grant,value,retention};
}

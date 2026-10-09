import { z } from 'zod';
import { FixtureError } from './lifecycle.js';
export type BaselineTarget='fake-model'|'fake-github';
type Response={status:number;body:unknown};
interface BaselinePort {
 generation(target:BaselineTarget,signal:AbortSignal):Promise<string>;
 request(target:BaselineTarget,method:'GET'|'POST',relativePath:'/__requests'|'/__reset',signal:AbortSignal,expectedGeneration:string):Promise<Response>;
}
const check=(value:unknown)=>{if(!value)throw new FixtureError('identity-mismatch');};
const ModelState=z.object({requests:z.array(z.unknown()).max(10000),queued:z.number().int().nonnegative()}).strict();
/** Private startup facts belong to successful launches of the source-attested
 * fixture services. An empty later request log cannot mint a startup fact. */
export class StartupBaselines {
 private readonly starts=new Map<BaselineTarget,string>();
 constructor(private readonly port:BaselinePort){}
 private async generation(target:BaselineTarget,expected:string,signal:AbortSignal){signal.throwIfAborted();check(expected&&await this.port.generation(target,signal)===expected);}
 private async empty(target:BaselineTarget,signal:AbortSignal,generation:string){
  const response=await this.port.request(target,'GET','/__requests',signal,generation);check(response.status===200);
  if(target==='fake-model'){const value=ModelState.parse(response.body);check(value.requests.length===0&&value.queued===0);}
  else {const value=z.array(z.unknown()).max(10000).parse(response.body);check(value.length===0);}
 }
 async captureSuccessfulStart(target:BaselineTarget,generation:string,signal:AbortSignal){
  check(this.starts.get(target)!==generation);await this.generation(target,generation,signal);await this.empty(target,signal,generation);await this.generation(target,generation,signal);
  // The GitHub DEFAULTS proof is the actual successful fresh launch of the
  // reviewed server, whose startup initializes DEFAULTS, plus this empty log.
  this.starts.set(target,generation);
 }
 async freshFixtureBaseline(target:BaselineTarget,signal:AbortSignal){
  const generation=this.starts.get(target);check(generation);await this.generation(target,generation!,signal);
  return {target,generation:generation!,complete:true as const,restore:{kind:'startup-empty' as const}};
 }
 async resetFixtureBaseline(target:BaselineTarget,generation:string,signal:AbortSignal){
  check(this.starts.get(target)===generation);await this.generation(target,generation,signal);
  const response=await this.port.request(target,'POST','/__reset',signal,generation);check(response.status===200);z.object({ok:z.literal(true)}).strict().parse(response.body);
  await this.generation(target,generation,signal);await this.empty(target,signal,generation);await this.generation(target,generation,signal);
  return response;
 }
}

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { EventEmitter } from 'node:events';
import { PassThrough } from 'node:stream';
import { type spawn } from 'node:child_process';
import { createNodeProcesses, LaunchNotStarted } from './process.js';
const command = { executable:'/attested/loom',argv:['usage','--format','json'],cwd:'/owned',env:{} };
function rig(pid:number|undefined=42) {
 const child=Object.assign(new EventEmitter(),{pid,stdin:new PassThrough(),stdout:new PassThrough(),stderr:new PassThrough(),kill(){return true;}});
 let exists=!!pid;const signals:NodeJS.Signals[]=[];
 const processes=createNodeProcesses((()=>child) as unknown as typeof spawn,((target:number,signal:NodeJS.Signals|number)=>{
  assert.equal(target,-pid!);if(signal===0){if(exists)return true;throw Object.assign(new Error('exited'),{code:'ESRCH'});}
  signals.push(signal as NodeJS.Signals);exists=false;child.emit('close',137);return true;
 }) as typeof process.kill);
 return {processes,child,signals,finish(code=0){exists=false;child.emit('close',code);},linger(){exists=true;}};
}
for(const code of [0,7])test(`fixed child transport preserves stdout/stderr and exit ${code}`,async()=>{
 const r=rig();const p=r.processes.launch!(command,'content','registered');r.child.emit('spawn');r.child.stdout.write('output');r.child.stderr.write('failure detail');r.finish(code);
 const result=await p.completion(new AbortController().signal);assert.deepEqual(result,{exitCode:code,stdout:'output',stderr:'failure detail',complete:true});await p.stop();assert.deepEqual(r.signals,[]);
});
for(const stream of ['stdout','stderr'] as const)test(`${stream} overflow is incomplete, never captured as successful empty output`,async()=>{
 const r=rig();const p=r.processes.launch!(command,'','registered');r.child.emit('spawn');r.child[stream].write(Buffer.alloc(4*1024*1024+1));r.finish();await assert.rejects(p.completion(new AbortController().signal));await p.stop();
});
test('synchronous spawn exception is explicitly proven not started',()=>{
 const processes=createNodeProcesses((()=>{throw new Error('Bearer private-spawn-token');}) as typeof spawn);assert.throws(()=>processes.launch!(command,'','registered'),LaunchNotStarted);
});
test('asynchronous no-pid error has no guessed cleanup target',async()=>{
 const r=rig(undefined);Object.assign(r.child,{pid:undefined});const p=r.processes.launch!(command,'','registered');r.child.emit('error',new Error('private'));await assert.rejects(p.ready(new AbortController().signal));await p.stop();assert.deepEqual(r.signals,[]);
});
test('cancelled completion keeps exact launched handle available for owned cleanup',async()=>{
 const r=rig();const p=r.processes.launch!(command,'','registered');r.child.emit('spawn');const controller=new AbortController();const pending=p.completion(controller.signal);controller.abort();await assert.rejects(pending);await p.stop();assert.deepEqual(r.signals,['SIGKILL']);
});
test('a completed parent with uncertain surviving group cannot be called cleaned up or guessed killed',async()=>{
 const r=rig();const p=r.processes.launch!(command,'','registered');r.child.emit('spawn');r.finish();r.linger();await assert.rejects(p.stop());assert.deepEqual(r.signals,[]);
});

test('owned service transport retains independent bounded log bytes without claiming a running prefix is closed',async()=>{
 const r=rig(),p=r.processes.start(command,'ready','registered');
 r.child.stdout.write('ready\n');await p.ready(new AbortController().signal);
 const bytes=Buffer.from('agent stopped via control socket worktree=nova café\n');
 r.child.stderr.write(bytes.subarray(0,bytes.length-3));r.child.stderr.write(bytes.subarray(bytes.length-3));
 assert.deepEqual(p.output!(),{stdout:'ready\n',stderr:bytes.toString('utf8'),stdoutComplete:true,stderrComplete:true,closed:false});
 r.finish();assert.equal(p.output!().closed,true);await p.stop();assert.deepEqual(r.signals,[]);
});

for(const stream of ['stdout','stderr'] as const)test(`owned service ${stream} overflow marks incomplete without killing or repairing output`,async()=>{
 const r=rig(),p=r.processes.start(command,'ready','registered');
 r.child[stream].write('ready\n');await p.ready(new AbortController().signal);
 r.child[stream].write(Buffer.alloc(4*1024*1024+1));
 assert.equal(p.output!()[`${stream}Complete`],false);assert.equal(p.output!()[stream],'ready\n');
 assert.equal(p.state(),'running');assert.deepEqual(r.signals,[]);r.finish();await p.stop();
});

test('owned service transport error cannot turn discarded or unfinished logs into a complete empty snapshot',async()=>{
 const r=rig(undefined),p=r.processes.start(command,'ready','registered');
 r.child.emit('error',new Error('private transport error'));
 await assert.rejects(p.ready(new AbortController().signal));
 assert.deepEqual(p.output!(),{stdout:'',stderr:'',stdoutComplete:false,stderrComplete:false,closed:false});
});

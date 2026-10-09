import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createCodexProtocolProbe } from './codex-probe.js';

class Socket extends EventTarget {
  sent:unknown[]=[];closed=false;
  send(bytes:string){this.sent.push(JSON.parse(bytes));}
  close(){this.closed=true;}
  message(data:unknown){this.dispatchEvent(new MessageEvent('message',{data}));}
}
test('Codex preflight initializes only the app-server protocol without a provider request',async()=>{
 const socket=new Socket(), endpoints:string[]=[];
 const probe=createCodexProtocolProbe(endpoint=>{endpoints.push(endpoint);return socket as unknown as WebSocket;});
 const result=probe('ws://127.0.0.1:4444',new AbortController().signal);
 socket.dispatchEvent(new Event('open'));assert.deepEqual(socket.sent,[{id:1,method:'initialize',params:{clientInfo:{name:'loom',title:'Loom',version:'dev'},capabilities:{experimentalApi:true}}}]);
 socket.message(JSON.stringify({id:1,result:{userAgent:'codex'}}));await result;assert.equal(socket.closed,true);assert.deepEqual(endpoints,['ws://127.0.0.1:4444']);assert.deepEqual(socket.sent.at(-1),{method:'initialized',params:null});
});
for(const data of ['broken',JSON.stringify({id:2,result:{}}),JSON.stringify({id:1,error:{message:'private'}}),JSON.stringify({id:1}), 'x'.repeat(65537)])test('Codex malformed or unrelated reply cannot establish protocol readiness '+data.length,async()=>{
 const socket=new Socket();const result=createCodexProtocolProbe(()=>socket as unknown as WebSocket)('ws://127.0.0.1:4444',new AbortController().signal);
 socket.dispatchEvent(new Event('open'));socket.message(data);await assert.rejects(result);assert.equal(socket.closed,true);assert.equal(socket.sent.length,1);
});
test('Codex cancelled protocol closes its socket; a foreign endpoint is refused before connect',async()=>{
 const socket=new Socket(), controller=new AbortController();let calls=0;
 const probe=createCodexProtocolProbe(()=>{calls++;return socket as unknown as WebSocket;});const result=probe('ws://127.0.0.1:4444',controller.signal);controller.abort();await assert.rejects(result);assert.equal(socket.closed,true);
 await assert.rejects(probe('ws://foreign:4444',new AbortController().signal));assert.equal(calls,1);
});

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { StartupBaselines } from './baseline.js';
function setup(){
 let generation='owned-start',body:unknown={requests:[],queued:0},reset:unknown={ok:true},status=200,swap=false;
 const effects:string[]=[];
 const baseline=new StartupBaselines({async generation(){return generation;},async request(target,method,route){effects.push(`${target}:${method}:${route}`);if(swap)generation='replaced';return {status,body:method==='POST'?reset:body};}});
 return {baseline,effects,signal:new AbortController().signal,set(value:unknown){body=value;},replace(){generation='foreign';},swapDuringRead(){swap=true;},badReset(){reset={ok:false};},unavailable(){status=503;}};
}
test('startup checkpoint retains initial empty state without treating a later queue as pristine',async()=>{
 const r=setup();await r.baseline.captureSuccessfulStart('fake-model','owned-start',r.signal);r.set({requests:[{private:'not retained'}],queued:2});
 const fact=await r.baseline.freshFixtureBaseline('fake-model',r.signal);assert.deepEqual(fact,{target:'fake-model',generation:'owned-start',complete:true,restore:{kind:'startup-empty'}});
 await assert.rejects(r.baseline.resetFixtureBaseline('fake-model',fact.generation,r.signal));r.set({requests:[],queued:0});assert.deepEqual(await r.baseline.resetFixtureBaseline('fake-model',fact.generation,r.signal),{status:200,body:{ok:true}});
});
for(const state of [{requests:[]},{requests:[],queued:1},{requests:[{}],queued:0},[],null])test('missing, malformed, or nonempty startup data cannot acquire an empty baseline '+JSON.stringify(state),async()=>{
 const r=setup();r.set(state);await assert.rejects(r.baseline.captureSuccessfulStart('fake-model','owned-start',r.signal));await assert.rejects(r.baseline.freshFixtureBaseline('fake-model',r.signal));
});
test('GitHub empty request log without trusted successful startup cannot become a defaults receipt',async()=>{
 const r=setup();r.set([]);await assert.rejects(r.baseline.freshFixtureBaseline('fake-github',r.signal));assert.deepEqual(r.effects,[]);
 await r.baseline.captureSuccessfulStart('fake-github','owned-start',r.signal);assert.equal((await r.baseline.freshFixtureBaseline('fake-github',r.signal)).generation,'owned-start');
});
test('replaced generation denies reset before mutation, and replacement during capture retains no receipt',async()=>{
 const r=setup();await r.baseline.captureSuccessfulStart('fake-model','owned-start',r.signal);const before=r.effects.length;r.replace();await assert.rejects(r.baseline.resetFixtureBaseline('fake-model','owned-start',r.signal));assert.equal(r.effects.length,before);
 const interrupted=setup();interrupted.swapDuringRead();await assert.rejects(interrupted.baseline.captureSuccessfulStart('fake-model','owned-start',interrupted.signal));await assert.rejects(interrupted.baseline.freshFixtureBaseline('fake-model',interrupted.signal));
});
test('failed reset or unavailable response is never a successful restoration',async()=>{
 const r=setup();await r.baseline.captureSuccessfulStart('fake-model','owned-start',r.signal);r.badReset();await assert.rejects(r.baseline.resetFixtureBaseline('fake-model','owned-start',r.signal));r.unavailable();await assert.rejects(r.baseline.resetFixtureBaseline('fake-model','owned-start',r.signal));
});

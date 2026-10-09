import assert from 'node:assert/strict';
import { test } from 'node:test';
import type * as fs from 'node:fs/promises';
import { attestContainerTemporaryRoot, ContainerReadRequest } from './container-read.js';

function namespaceFiles(issue='') {
  let reads=0;
  return {
    async realpath(name:string){return issue==='foreign-root'&&name==='/tmp'?'/host/tmp':name;},
    async lstat(name:string){
      assert.ok(['/run/.containerenv','/tmp'].includes(name));
      if(issue==='missing-marker'&&name==='/run/.containerenv')throw Object.assign(new Error('missing'),{code:'ENOENT'});
      if(name==='/run/.containerenv')reads++;
      return {dev:1,ino:issue==='replaced-marker'&&reads>1?999:name==='/tmp'?2:3,
        isFile:()=>name==='/run/.containerenv',isDirectory:()=>name==='/tmp',
        isSymbolicLink:()=>issue==='symlink'};
    },
  } as unknown as Pick<typeof fs,'lstat'|'realpath'>;
}
test('temporary root is fixed to the attested owned container namespace',async()=>{
  assert.deepEqual(await attestContainerTemporaryRoot('owned-container','lease-owned',namespaceFiles()),
    {temporaryRoot:'/tmp',device:1,inode:3});
});
for(const issue of ['missing-marker','foreign-root','replaced-marker','symlink'])test(`${issue}: temporary namespace cannot become an owned root`,async()=>{
  await assert.rejects(attestContainerTemporaryRoot('owned-container','lease-owned',namespaceFiles(issue)));
});
test('missing lease or host namespace rejects before inspecting any host path',async()=>{
  const files={async lstat(){assert.fail('must not inspect host');},async realpath(){assert.fail('must not inspect host');}};
  await assert.rejects(attestContainerTemporaryRoot(undefined,'lease-owned',files));
  await assert.rejects(attestContainerTemporaryRoot('owned-container',undefined,files));
});
test('history and surviving Git lifecycle use closed requests with no paths or commands',()=>{
  assert.deepEqual(ContainerReadRequest.parse({operation:'agent-history',agentId:'agt_owned'}),{operation:'agent-history',agentId:'agt_owned'});
  assert.ok(ContainerReadRequest.safeParse({operation:'git-lifecycle',agentId:'agt_owned',maxBytes:1000}).success);
  for(const extra of [{command:'true'},{modulePath:'/host/helper'},{path:'/host/tmp'},{sql:'SELECT 1'}])
    assert.equal(ContainerReadRequest.safeParse({operation:'agent-history',agentId:'agt_owned',...extra}).success,false);
  assert.equal(ContainerReadRequest.safeParse({operation:'filesystem-root',root:{kind:'fixture-temporary',path:'/host/tmp'}}).success,false);
});

test('fixed model protocol cannot parse script, reset or fixture mutations',()=>{
 for(const method of ['POST','PATCH','DELETE'])for(const relativePath of ['/__script','/__reset','/__fixture'])
   assert.equal(ContainerReadRequest.safeParse({operation:'fixture-http',method,relativePath,body:{}}).success,false);
 assert.ok(ContainerReadRequest.safeParse({operation:'fixture-http',method:'GET',relativePath:'/__requests',body:null}).success);
});

test('runtime identity is a closed read with no supplied token or root override',()=>{
 assert.ok(ContainerReadRequest.safeParse({operation:'runtime-identity'}).success);
 for(const extra of [{runId:'guessed'},{leaseId:'foreign'},{root:'/host/tmp'},{command:'true'}])
  assert.equal(ContainerReadRequest.safeParse({operation:'runtime-identity',...extra}).success,false);
});

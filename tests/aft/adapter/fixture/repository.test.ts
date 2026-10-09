import assert from 'node:assert/strict';
import {test} from 'node:test';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import {createHash} from 'node:crypto';
import {seedOwnedRepository,type RepositoryCoordinates} from './repository.js';
import type {HostCommand} from './process.js';
const signal=new AbortController().signal;
async function setup(){
 const root=await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname),'test-repository-'));
 const runtimeRoot=path.join(root,'runtime'),source=path.join(root,'source'),fixture=path.join(source,'tests/fixtures/slack-clone');
 await fs.mkdir(runtimeRoot);await fs.mkdir(fixture,{recursive:true});
 const entries=[];
 for(const [relative,value] of [['app.js','actual source app'],['assets/logo.svg','actual image'],['data.json','seed data removed']] as const){
  const filename=path.join(fixture,relative);await fs.mkdir(path.dirname(filename),{recursive:true});await fs.writeFile(filename,value);
  entries.push({relativePath:`tests/fixtures/slack-clone/${relative}`,sha256:createHash('sha256').update(value).digest('hex')});
 }
 const coordinates:RepositoryCoordinates={runtimeRoot,destination:path.join(runtimeRoot,'source-repo'),gitBinary:'/attested/git',toolPath:'/attested',
  source:{source:{root:source,entries},build:{root:source,entries:[]},revision:{repository:'test',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)}}};
 const commands:HostCommand[]=[];
 return {root,fixture,coordinates,commands,processes:{async run(command:HostCommand){commands.push(command);return '';}},async cleanup(){await fs.rm(root,{recursive:true});}};
}
test('actual pinned Slack bytes and original fixed Git metadata are preserved without data.json',async()=>{
 const r=await setup();try{
  const receipt=await seedOwnedRepository('slack-clone',r.coordinates,r.processes,signal);
  assert.equal(await fs.readFile(path.join(receipt.path,'app.js'),'utf8'),'actual source app');
  assert.equal(await fs.readFile(path.join(receipt.path,'assets/logo.svg'),'utf8'),'actual image');
  await assert.rejects(fs.stat(path.join(receipt.path,'data.json')));
  assert.deepEqual(r.commands.map(c=>c.argv),[['init','-q','-b','main'],['add','-A'],['-c','user.name=Loom Fixture','-c','user.email=fixture@loom.invalid','commit','-q','-m','Initial Slack clone']]);
  assert.ok(r.commands.every(c=>c.executable==='/attested/git'&&c.cwd===receipt.path));
 }finally{await r.cleanup();}
});
test('legacy empty source keeps original default branch and empty commit, without invented files',async()=>{
 const r=await setup();try{
  const receipt=await seedOwnedRepository('empty-legacy',r.coordinates,r.processes,signal);
  assert.deepEqual(await fs.readdir(receipt.path),[]);
  assert.deepEqual(r.commands.map(c=>c.argv),[['init','-q'],['-c','user.email=e2e@x','-c','user.name=e2e','commit','--allow-empty','-m','init','-q']]);
 }finally{await r.cleanup();}
});
test('missing, extra, changed and symlink source files refuse before creating a repository or running Git',async()=>{
 for(const change of ['missing','extra','changed','symlink'] as const){const r=await setup();try{
  const filename=path.join(r.fixture,'app.js');
  if(change==='missing')await fs.unlink(filename);
  if(change==='extra')await fs.writeFile(path.join(r.fixture,'foreign.txt'),'foreign');
  if(change==='changed')await fs.writeFile(filename,'changed bytes');
  if(change==='symlink'){await fs.unlink(filename);await fs.symlink(path.join(r.fixture,'data.json'),filename);}
  await assert.rejects(seedOwnedRepository('slack-clone',r.coordinates,r.processes,signal));
  assert.equal(r.commands.length,0);await assert.rejects(fs.stat(r.coordinates.destination));
 }finally{await r.cleanup();}}
});
test('pre-existing or foreign destination and aborted setup have no Git or replacement effects',async()=>{
 const r=await setup();try{
  await fs.mkdir(r.coordinates.destination);await fs.writeFile(path.join(r.coordinates.destination,'foreign.txt'),'keep');
  await assert.rejects(seedOwnedRepository('empty-legacy',r.coordinates,r.processes,signal));
  assert.equal(await fs.readFile(path.join(r.coordinates.destination,'foreign.txt'),'utf8'),'keep');
  await assert.rejects(seedOwnedRepository('empty-legacy',{...r.coordinates,destination:r.root+'/foreign'},r.processes,signal));
  await assert.rejects(seedOwnedRepository('empty-legacy',r.coordinates,r.processes,AbortSignal.abort()));assert.equal(r.commands.length,0);
 }finally{await r.cleanup();}
});

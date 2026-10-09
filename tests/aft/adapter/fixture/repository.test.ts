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

test('Agent API main sources preserve supplied launch-token README bytes and fixed Git metadata',async()=>{
 const r=await setup();try{
  const readme='agents-v1 children original-launch-token\n';
  const receipt=await seedOwnedRepository('main-readme',{...r.coordinates,readme},r.processes,signal);
  assert.equal(await fs.readFile(path.join(receipt.path,'README.md'),'utf8'),readme);
  assert.deepEqual(await fs.readdir(receipt.path),['README.md']);
  assert.deepEqual(r.commands.map(c=>c.argv),[['init','-q','-b','main'],['add','README.md'],
   ['-c','user.email=e2e@x','-c','user.name=e2e','commit','-q','-m','init']]);
 }finally{await r.cleanup();}
});

test('two main repositories retain independent owned roots and their distinct original source bytes',async()=>{
 const r=await setup();try{
  const receipts=[];
  for(const name of ['agv1-par-alpha','agv1-par-beta']){
   const receipt=await seedOwnedRepository('main-readme',{...r.coordinates,destination:path.join(r.coordinates.runtimeRoot,name),readme:`${name} actual-run-token\n`},r.processes,signal);
   receipts.push(receipt);assert.equal(await fs.readFile(path.join(receipt.path,'README.md'),'utf8'),`${name} actual-run-token\n`);
  }
  assert.notEqual(receipts[0]!.path,receipts[1]!.path);assert.notEqual(receipts[0]!.inode,receipts[1]!.inode);
  assert.deepEqual(r.commands.filter(c=>c.argv[0]==='init').map(c=>c.cwd),receipts.map(receipt=>receipt.path));
 }finally{await r.cleanup();}
});

test('empty main and GitHub-reader source contracts create no README and accept only the fixed local origin',async()=>{
 for(const githubReadOrigin of [undefined,true] as const){const r=await setup();try{
  const receipt=await seedOwnedRepository('empty-main',{...r.coordinates,githubReadOrigin},r.processes,signal);
  assert.deepEqual(await fs.readdir(receipt.path),[]);
  assert.deepEqual(r.commands.map(c=>c.argv),[['init','-q','-b','main'],
   ['-c','user.email=e2e@x','-c','user.name=e2e','commit','-q','--allow-empty','-m','init'],
   ...(githubReadOrigin?[['remote','add','origin','https://github.com/loom-e2e/agv1-ghread.git']]:[])]);
 }finally{await r.cleanup();}}
});

test('invalid main startup data refuses before filesystem creation or any Git command',async()=>{
 const r=await setup();try{
  for(const readme of [undefined,'','missing newline','two\nlines\n','nul\0\n','x'.repeat(2049)+'\n']){
   await assert.rejects(seedOwnedRepository('main-readme',{...r.coordinates,readme},r.processes,signal));
  }
  await assert.rejects(seedOwnedRepository('empty-main',{...r.coordinates,readme:'extra\n'},r.processes,signal));
  await assert.rejects(seedOwnedRepository('main-readme',{...r.coordinates,readme:'readme\n',githubReadOrigin:true},r.processes,signal));
  assert.equal(r.commands.length,0);await assert.rejects(fs.stat(r.coordinates.destination));
 }finally{await r.cleanup();}
});

test('failed main Git initialization retains its exact owned partial source for enclosing fixture cleanup',async()=>{
 const r=await setup();try{
  let attempts=0;
  await assert.rejects(seedOwnedRepository('main-readme',{...r.coordinates,readme:'agents-v1 ui retained-token\n'},
   {async run(){attempts++;throw new Error('injected failed Git initialization');}},signal));
  assert.equal(attempts,1);
  assert.equal(await fs.readFile(path.join(r.coordinates.destination,'README.md'),'utf8'),'agents-v1 ui retained-token\n');
  await assert.rejects(seedOwnedRepository('empty-main',r.coordinates,r.processes,signal));assert.equal(r.commands.length,0);
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

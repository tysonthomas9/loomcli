import assert from 'node:assert/strict';
import { test } from 'node:test';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { registerLoomAdapter, pinLoomImplementation } from './composition.js';
import { getFixture } from './ownership.js';
import { evidenceKey } from './evidence.js';
import { HostFixtureDriver, type HostConfig, type Http } from './fixture/host.js';
import type { HostCommand, HostProcesses, OwnedProcess } from './fixture/process.js';
import type { FixturePlan } from './fixture/lifecycle.js';
import { productionFixtureOptions, AcquireOutput } from './fixture/providers.js';
import { materializeRenderer } from './fixture/renderer-fixtures.test.js';

const hash=(bytes:string)=>createHash('sha256').update(bytes).digest('hex');
test('default public composition binds the managed host checkout and existing evidence authority before CLI effects',async t=>{
  // Physical files exercise preflight and retained creation receipts. All
  // product process/HTTP/kernel execution is injected; this is not runtime proof.
  const root=await fs.realpath(await fs.mkdtemp(path.join(os.tmpdir(),'loom-default-composition-')));
  let cleanup:()=>Promise<unknown>=async()=>{};
  t.after(async()=>{try{await cleanup();}finally{await fs.rm(root,{recursive:true,force:true});}});
  const source=path.join(root,'source'),build=path.join(root,'build'),home=path.join(root,'home'),locks=path.join(root,'locks');
  for(const directory of [source,build,home,locks])await fs.mkdir(directory);
  const sourceEntries:{relativePath:string;sha256:string}[]=[],buildEntries:typeof sourceEntries=[];
  for(const binary of ['loom','fleet','node','git','opencode']) {
    await fs.writeFile(path.join(build,binary),binary,{mode:0o700});buildEntries.push({relativePath:binary,sha256:hash(binary)});
  }
  const farm=path.join(source,'e2e/stubs');await fs.mkdir(farm,{recursive:true});
  for(const binary of ['codex','claude','cursor-agent','opencode','gemini','gh']) {
    await fs.writeFile(path.join(farm,binary),binary,{mode:0o700});sourceEntries.push({relativePath:`e2e/stubs/${binary}`,sha256:hash(binary)});
  }
  const revision={repository:'injected-composition',commit:'a'.repeat(40),tree:'b'.repeat(40),sourceManifestSha256:'c'.repeat(64),buildManifestSha256:'d'.repeat(64)};
  const registered={revision,source:{root:source,entries:sourceEntries},build:{root:build,entries:buildEntries}};
  await materializeRenderer(registered);
  const config:HostConfig={loom:registered,fleet:registered,engine:registered,adapter:registered,tempParent:root,lockParent:locks,
    hostHome:home,toolPath:'/injected/toolchain',connection:'injected',connectionFingerprint:'e'.repeat(64),minimumFreeBytes:1,attestedImages:false,
    loomBinary:path.join(build,'loom'),fleetBinary:path.join(build,'fleet'),nodeBinary:path.join(build,'node'),gitBinary:path.join(build,'git'),
    pinnedOpenCodeBinary:path.join(build,'opencode'),realBinaries:{},daemon:false,fakeGitHub:false,maxBudgetUsd:'5.00',fixtureRunId:'original-host-token'};
  const plan:FixturePlan={profile:'legacy-deterministic',loomRevision:revision,fleetRevision:revision,engineRevision:revision,adapterRevision:revision,
    model:'aft/m',caseCount:1,maxCases:1,selectionSha256:'f'.repeat(64),leaseDurationMs:600000};
  let ordinal=0,port=4700,servePid=0;const launches:HostCommand[]=[],managed=new Map<string,{path:string;repo:string;source:string}>();
  const started=new Map<number,{handle:OwnedProcess;command:HostCommand}>();
  const processes:HostProcesses={
    async run(command){
      if(command.argv[0]==='init'){await fs.mkdir(path.join(command.cwd,'.git'));return '';}
      if(command.argv[0]==='rev-parse'&&command.argv[1]==='--git-common-dir')
        return path.join([...managed.values()].find(row=>row.repo===command.cwd)?.source??command.cwd,'.git');
      if(command.argv[0]==='rev-parse')return command.argv[1]==='HEAD'?revision.commit:revision.tree;
      if(command.argv[0]==='ls-files')return sourceEntries.map(entry=>entry.relativePath).join('\0')+'\0';
      return '';
    },
    start(command,_ready,generation){
      let alive=true;const handle:OwnedProcess={pid:++ordinal+100,generation:generation!,executable:command.executable,argv:command.argv,
        state:()=>alive?'running':'exited',async ready(){
          if(command.argv[0]==='serve') {
            servePid=handle.pid;const directory=path.join(command.env.LOOM_CONFIG_DIR!,'fleet-db');await fs.mkdir(directory,{recursive:true});
            await fs.writeFile(path.join(directory,'runtime.json'),JSON.stringify({pid:999,url:'http://127.0.0.1:6001',started_at:'2026-10-09T00:00:00Z'}));
          }
        },async stop(){alive=false;}};started.set(handle.pid,{handle,command});return handle;
    },
    launch(command,_stdin,generation){
      launches.push(command);let alive=true;
      return {pid:++ordinal+100,generation,executable:command.executable,argv:command.argv,state:()=>alive?'running':'exited',
        async ready(){},async stop(){alive=false;},async completion(){alive=false;return {exitCode:0,stderr:'',complete:true,
          stdout:command.argv.includes('list')?'[{"name":"task"}]':'{"name":"task","description":"actual injected CLI result"}'};}};
    },
  };
  const http:Http=async(_origin,method,route,body)=>{
    if(route==='/__requests')return {status:200,body:{requests:[],queued:0}};
    if(route==='/api/config')return {status:200,body:{}};
    if(method==='POST'&&route==='/api/workspaces') {
      const input=body as {name:string;repos:string[]},id=input.name.toUpperCase(),workspace=path.join(driver.runtimeRoot,'runtime/managed',id),repo=path.join(workspace,'repos/repo');
      await fs.mkdir(repo,{recursive:true});managed.set(id,{path:workspace,repo,source:input.repos[0]!});
      return {status:201,body:{success:true,data:{id,path:workspace,repos:[{name:'repo',path:repo}]}}};
    }
    if(method==='POST')return {status:201,body:{}};
    const id=route.split('/')[3],record=managed.get(id!);
    if(route.endsWith('/agents'))return {status:200,body:{success:true,total:0,data:[]}};
    if(route.endsWith('/issues?limit=1000'))return {status:200,body:{success:true,data:[]}};
    return {status:200,body:{success:true,data:{id,path:record!.path,repos:[{name:'repo',path:record!.repo}]}}};
  };
  const driver:HostFixtureDriver=new HostFixtureDriver(config,processes,fs,http,()=>`injected-${++ordinal}`,async()=>({port:port++,async release(){}}),undefined,
    {async capture(pid){const parent=started.get(pid);assert.ok(pid===999||parent);let alive=true;
      const identity={pid,generation:parent?`injected-parent-${pid}`:'injected-store-generation',executable:parent?config.loomBinary:config.fleetBinary,
        argvSha256:parent?hash([config.loomBinary,...parent.handle.argv].join('\0')+'\0'):'e'.repeat(64),parentPid:servePid,
        configurationRoot:driver.configurationRoot,...(parent?{fixtureRunId:parent.command.env.RUN_ID}:{}),state:'running' as const};
      return {identity,async inspect(){return {...identity,state:alive&&(!parent||parent.handle.state()==='running')?'running' as const:'exited' as const};},
        async stop(){alive=false;await parent?.handle.stop();},async abandon(){assert.fail('owned store/parent');}};}});
  const pin=await pinLoomImplementation(fileURLToPath(new URL('.',import.meta.url)),'source');
  const fixtures={...productionFixtureOptions(pin,pin.sha256,[plan],config,config),driver:()=>driver};
  // No legacyAccess override: exercise the public default factory itself.
  const registry=registerLoomAdapter(new CapabilityRegistry(),{implementation:pin,fixtures});
  const context=createCapabilityContext({file:'default-host.test.yaml',line:1},registry);
  const invoke=(id:string,input:unknown)=>registry.invoke({id,version:1,input:{}},input,context);
  const acquired=await invoke('loom.fixture.acquire',{runId:context.runId,profile:plan.profile,loomRevision:revision,fleetRevision:revision,
    model:plan.model,maxCases:1,selectionSha256:plan.selectionSha256});assert.equal(acquired.availability,'observed',JSON.stringify(acquired.error));
  const leaseId=AcquireOutput.parse(acquired.data).lease.id,fixture=await getFixture(context,leaseId);
  assert.equal(AcquireOutput.parse(acquired.data).fixtureRunId,'original-host-token');assert.notEqual(fixture.runId,'original-host-token');
  cleanup=()=>invoke('loom.fixture.release',{leaseId});
  assert.notEqual(fixture.repo,driver.workspaceRoot);assert.equal(fixture.ownedWorkspaces!.length,2);
  const input={leaseId,workspaceId:'E2E-WS',operation:'show',name:'task'};
  const key=`${evidenceKey}:${leaseId}`,store=context.resources.get(key);context.resources.delete(key);
  const missing=await invoke('loom.cli.role',input);assert.equal(missing.availability,'error');assert.equal(launches.length,0);context.resources.set(key,store);
  const runId=fixture.runId;fixture.runId='foreign';
  const foreign=await invoke('loom.cli.role',input);assert.equal(foreign.availability,'error');assert.equal(launches.length,0);fixture.runId=runId;
  const originalRepo=fixture.repo;fixture.repo=driver.workspaceRoot;
  const wrongSource=await invoke('loom.cli.role',input);assert.equal(wrongSource.availability,'error');assert.equal(launches.length,0);fixture.repo=originalRepo;
  const observed=await invoke('loom.cli.role',input);assert.equal(observed.availability,'observed',JSON.stringify(observed.error));
  assert.deepEqual((observed.data as {body:unknown}).body,{name:'task',description:'actual injected CLI result'});
  assert.equal(observed.provenance.identity.fixtureLeaseId,leaseId);assert.equal(observed.provenance.evidenceClass,'deterministic');
  assert.equal(launches.length,3);assert.deepEqual(launches.at(-1)!.argv,['--workspace','E2E-WS','role','show','task','--json']);
  assert.equal(launches.at(-1)!.cwd,driver.workspaceRoot);assert.equal(launches.at(-1)!.env.LOOM_CONFIG_DIR,driver.configurationRoot);
  assert.equal((await invoke('loom.fixture.release',{leaseId})).availability,'observed');
});

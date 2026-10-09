import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { HostFixtureDriver, legacyProfiles, type HostConfig, type Http } from './host.js';
import { FixtureLifecycle, FixtureError, type FixturePlan } from './lifecycle.js';
import { LaunchNotStarted, type HostProcesses, type HostCommand, type OwnedProcess } from './process.js';
import { type RegisteredBuild, verifyManifest } from './production.js';
import { materializeRenderer } from './renderer-fixtures.test.js';
import { createEvidenceStore } from '../evidence.js';
import { enrollOwnedLegacyAgent, requireOwnedWorkspace } from '../workspaces.js';
import type { OwnedFixture } from '../ownership.js';
import { getFixture } from '../ownership.js';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin } from '@tysonthomas9/aft/capabilities';
import { createFixtureProviders, productionFixtureOptions } from './providers.js';
import type { RegisteredIdentity } from './descendants.js';
import { HostWorkspaceRecords } from './workspace-records.js';

const hash = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
async function setup(profile: string,registeredServices=false,nativeService=false,fixtureRunId?:string,daemon=false) {
  const root = await fs.mkdtemp(path.join(path.dirname(new URL(import.meta.url).pathname), 'test-artifacts-'));
  const source = path.join(root, 'source'); const build = path.join(root, 'build');
  await fs.mkdir(source); await fs.mkdir(build); await fs.mkdir(path.join(root, 'locks')); await fs.mkdir(path.join(root, 'home'));
  await fs.writeFile(path.join(source, 'tracked.txt'), 'pinned source');
  const entries: { relativePath: string; sha256: string }[] = [];
  for (const binary of ['loom', 'fleet', 'node', 'git', 'opencode']) {
    const filename = path.join(build, binary); await fs.writeFile(filename, `pinned ${binary}`, { mode: 0o700 });
    entries.push({ relativePath: binary, sha256: hash(`pinned ${binary}`) });
  }
  const sourceEntries = [{ relativePath: 'tracked.txt', sha256: hash('pinned source') }];
  const realBinary = path.join(build, 'selected-real'); await fs.writeFile(realBinary, 'real-binary', { mode: 0o700 });
  entries.push({ relativePath: 'selected-real', sha256: hash('real-binary') });
  const manifestHash = (values: typeof entries) => hash([...values].sort((a,b) => a.relativePath < b.relativePath ? -1 : 1).map(entry => `${entry.sha256}  ${entry.relativePath}\n`).join(''));
  const revision = { repository: 'fixture-test', commit: 'a'.repeat(40), tree: 'b'.repeat(40), sourceManifestSha256: manifestHash(sourceEntries), buildManifestSha256: manifestHash(entries) };
  const registered: RegisteredBuild = { revision, source: { root: source, entries: sourceEntries }, build: { root: build, entries } };
  const backend = profile.replace('legacy-real-', '');
  const farm = path.join(source, 'e2e', profile === 'legacy-deterministic' ? 'stubs' : `stubs-real-${backend}`);
  await fs.mkdir(farm, { recursive: true });
  const selected = backend === 'cursor' ? 'cursor-agent' : backend;
  for (const tool of ['codex', 'claude', 'cursor-agent', 'opencode', 'gemini', 'gh']) if (profile === 'legacy-deterministic' || tool !== selected) await fs.writeFile(path.join(farm, tool), 'stub', { mode: 0o700 });
  const authRoot = path.join(root, 'home', 'auth'); await fs.mkdir(authRoot);
  await fs.writeFile(path.join(authRoot, 'auth.json'), 'private auth'); await fs.writeFile(path.join(authRoot, '.credentials.json'), 'private auth');
  for (const tool of ['codex', 'claude', 'cursor-agent', 'opencode', 'gemini', 'gh']) if (profile === 'legacy-deterministic' || tool !== selected) {
    sourceEntries.push({ relativePath: path.relative(source, path.join(farm, tool)), sha256: hash('stub') });
  }
  revision.sourceManifestSha256 = manifestHash(sourceEntries);
  await materializeRenderer(registered);
  const config: HostConfig = {
    loom: registered, fleet: registered, engine: registered, adapter: registered,
    tempParent: root, lockParent: path.join(root, 'locks'), hostHome: path.join(root, 'home'), toolPath: '/attested/toolchain',
    connection: 'test', connectionFingerprint: 'c'.repeat(64), minimumFreeBytes: 1, attestedImages: false,fixtureRunId,
    loomBinary: path.join(build, 'loom'), fleetBinary: path.join(build, 'fleet'), nodeBinary: path.join(build, 'node'), gitBinary: path.join(build, 'git'),
    pinnedOpenCodeBinary: path.join(build, 'opencode'), realBinaries: Object.fromEntries(['codex', 'claude', 'cursor', 'opencode'].map(name => [name,
      { executable: realBinary, sha256: hash('real-binary'), authRoot }])), daemon, fakeGitHub: false, maxBudgetUsd: '5.00',
  };
  const plan: FixturePlan = { profile, loomRevision: { ...revision }, fleetRevision: { ...revision }, engineRevision: { ...revision }, adapterRevision: { ...revision },
    model: profile === 'legacy-deterministic' ? 'aft/m' : profile === 'legacy-real-cursor' ? 'backend-default' : 'openai/real-model', maxCases: 10, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 10000 };
  const starts: { id: string; command: HostCommand; readiness: string }[] = []; const runs: HostCommand[] = []; const stopped: string[] = [];
  let failService = ''; let failStop = ''; let spawnFails = false; let port = 4100; let count = 0; let failHttp = false;
  const handles = new Map<string, OwnedProcess>();
  let cliOutput='',headOutput:string|undefined;let onCliAwait:((phase:'ready'|'completion')=>Promise<void>)|undefined;const commonDirectories=new Map<string,string>();
  let onRun:((command:HostCommand)=>Promise<void>)|undefined;
  const processes: HostProcesses = {
    async run(command) {
      runs.push(command);
      await onRun?.(command);
      if(command.argv[0]==='init'){await fs.mkdir(path.join(command.cwd,'.git'),{recursive:true});return '';}
      if(command.argv[0]==='rev-parse'&&command.argv[1]==='--git-common-dir')return commonDirectories.get(command.cwd)??'.git';
      if(command.argv[0]==='symbolic-ref')return 'actual-product-branch';
      if(command.argv[0]==='rev-parse'&&command.argv[1]==='HEAD'&&headOutput!==undefined)return headOutput;
      if (command.argv[0] === 'rev-parse') return command.argv[1] === 'HEAD' ? revision.commit : revision.tree;
      if (command.argv[0] === 'ls-files') return sourceEntries.map(entry => entry.relativePath).join('\0') + '\0';
      return '';
    },
    launch(command, stdin, generation) {
      if(spawnFails) throw new LaunchNotStarted();
      runs.push(command); let alive = true;
      const handle = { pid: ++count + 100, generation, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' as const : 'exited' as const, async ready() {await onCliAwait?.('ready');}, async stop() { alive = false; stopped.push('cli'); },
        async completion() {await onCliAwait?.('completion'); alive = false; return { exitCode: 0, stdout: stdin || cliOutput || '{"owned":true}', stderr: '', complete: true }; } };
      handles.set('cli', handle); return handle;
    },
    start(command, readiness, generation) {
      const id = command.argv[0] === 'app-server' ? 'codex-preflight' : command.argv[0] === 'serve' ? 'serve' : command.argv.includes('preview') ? 'frontend' : command.argv[0]?.endsWith('/fake-model/server.mjs') ? 'fake-model' : 'daemon';
      if(spawnFails) throw new LaunchNotStarted();
      starts.push({ id, command, readiness }); let alive = true;
      const handle: OwnedProcess = { pid: ++count + 40, generation: generation ?? `generation-${count}`, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() { if(registeredServices&&id==='serve'){const dir=path.join(command.env.LOOM_CONFIG_DIR!,'fleet-db');await fs.mkdir(dir,{recursive:true});await fs.writeFile(path.join(dir,'runtime.json'),JSON.stringify({pid:999,url:'http://127.0.0.1:6001',started_at:'2026-10-09T00:00:00Z'}));if(nativeService){const dir=path.join(command.env.LOOM_CONFIG_DIR!,'agents-opencode/state/opencode');await fs.mkdir(dir,{recursive:true});await fs.writeFile(path.join(dir,'service.json'),JSON.stringify({pid:1001,url:'http://127.0.0.1:7001',password:'private-fixture-password'}));}} if (id === failService) throw new Error('Bearer private-ready-token'); },
        async stop() { if (id === failStop) throw new Error('secret=private-stop-token'); alive = false; stopped.push(id); } };
      handles.set(id, handle); return handle;
    },
  };
  let onHttp:((method:string,relative:string)=>Promise<void>)|undefined;
  let responseOverride:((relative:string)=>unknown)|undefined;
  const requests:{method:string;relative:string}[]=[];
  const createdWorkspaces=new Map<string,{id:string;repos:{path:string;name:string;source_repo_id:string;groups:string[]}[]}>();
  const http: Http = async (_origin, method, relative,body) => {
    requests.push({method,relative});await onHttp?.(method,relative);
    if(responseOverride){const overridden=responseOverride(relative);if(overridden!==undefined)return {status:200,body:overridden};}
    if (failHttp) return { status: 503, body: { message: 'Bearer private-http-token' } };
    if(relative==='/__requests')return {status:200,body:{requests:[],queued:0}};
    if(relative==='/__reset')return {status:200,body:{ok:true}};
    if(method==='POST'&&relative==='/api/workspaces'){const input=body as {name:string;repos:string[]},data={id:input.name.toUpperCase(),repos:input.repos.map(repo=>({path:repo,name:path.basename(repo),source_repo_id:path.basename(repo),groups:[]}))};createdWorkspaces.set(data.id,data);return {status:201,body:{success:true,data}};}
    if (method === 'POST') return { status: 201, body: {} };
    const data=createdWorkspaces.get(relative.split('/').at(-1)!);
    return { status: 200, body: data?{success:true,data}:{} };
  };
  const registeredRunning=new Map<number,boolean>();let failRegisteredStop=false;const registeredStops:string[]=[],captures:number[]=[],abandoned:number[]=[];
  const registrationOverrides=new Map<number,Partial<RegisteredIdentity>>();let onCapture:((pid:number)=>Promise<void>)|undefined;
  const registeredPort={async capture(pid:number){
    const parent=[...handles.entries()].find(([name,handle])=>['serve','daemon'].includes(name)&&handle.pid===pid)?.[1];
    assert.ok([999,1001,1002].includes(pid)||parent||registrationOverrides.has(pid));captures.push(pid);registeredRunning.set(pid,true);
    const identity:RegisteredIdentity={pid,generation:pid===999?'actual-kernel-start':pid===1001?'actual-native-start':pid===1002?'actual-native-successor':`actual-parent-${pid}`,
      executable:pid===999?config.fleetBinary:parent?config.loomBinary:config.pinnedOpenCodeBinary,
      argvSha256:parent?hash(Buffer.from([config.loomBinary,...parent.argv].join('\0')+'\0')):pid===1002?hash(Buffer.from([config.pinnedOpenCodeBinary,'serve','--service'].join('\0')+'\0')):'a'.repeat(64),
      parentPid:handles.get('serve')!.pid,configurationRoot:driver.configurationRoot,
      fixtureRunId:parent?starts.find(start=>start.command.argv===parent.argv)!.command.env.RUN_ID:undefined,state:'running',...registrationOverrides.get(pid)};
    await onCapture?.(pid);
    return {identity,async inspect(){return {...identity,parentPid:handles.get('serve')!.state()==='exited'?1:identity.parentPid,
      state:registeredRunning.get(pid)&&(!parent||parent.state()==='running')?'running' as const:'exited' as const,...registrationOverrides.get(pid)};},
      async terminateGracefully(){assert.ok([1001,1002].includes(pid));registeredRunning.set(pid,false);registeredStops.push('opencode-term');},
      async stop(){if(!registeredRunning.get(pid)||parent?.state()==='exited')return;if(failRegisteredStop)throw new Error('private child cleanup');
        registeredRunning.set(pid,false);if(parent)await parent.stop();registeredStops.push(pid===999?'fleet':parent?'parent-force':'opencode-force');},
      async abandon(){abandoned.push(pid);}};
  }};
  const protocols:string[]=[];
  const driver: HostFixtureDriver = new HostFixtureDriver(config, processes, fs, http, () => `fixture-${++count}`, async () => ({ port: port++, async release() {} }),async endpoint=>{protocols.push(endpoint);if(failService==='codex-protocol')throw new Error('private probe failure');},registeredServices?registeredPort:undefined,()=>1700000000123);
  const lifecycle = new FixtureLifecycle([plan], () => driver, () => 1000, () => 'opaque-fixture');
  return { root, source, config, plan, starts, runs, stopped, handles, driver, lifecycle, protocols, requests,
    registeredStops,captures,abandoned,failRegisteredCleanup(value:boolean){failRegisteredStop=value;},
    registration(pid:number,values:Partial<RegisteredIdentity>){registrationOverrides.set(pid,values);},onCapture(callback:(pid:number)=>Promise<void>){onCapture=callback;},
    request: { runId: 'test-run', profile, loomRevision: { ...revision }, fleetRevision: { ...revision }, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 },
    onHttp(callback:(method:string,relative:string)=>Promise<void>){onHttp=callback;},
    overrideResponse(callback:(relative:string)=>unknown){responseOverride=callback;},
    cliOutput(value:unknown){cliOutput=JSON.stringify(value);},commonDir(repo:string,value:string){commonDirectories.set(repo,value);},head(value:string){headOutput=value;},
    onRun(callback:(command:HostCommand)=>Promise<void>){onRun=callback;},
    onCliAwait(callback:(phase:'ready'|'completion')=>Promise<void>){onCliAwait=callback;},
    failSpawn() { spawnFails = true; }, failService(value: string) { failService = value; }, failStop(value: string) { failStop = value; }, failHttp() { failHttp = true; },
    async cleanup() { await fs.rm(root, { recursive: true }); } };
}

test('host RUN_ID receipt reads the exact configured or clock-derived token from its owned process',async()=>{
 for(const token of [undefined,'original-launch-token']){const r=await setup('legacy-deterministic',true,false,token);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),expected=token??'1700000000';
  assert.equal(r.starts.find(start=>start.id==='serve')!.command.env.RUN_ID,expected);
  assert.notEqual(expected,r.request.runId);assert.deepEqual(await r.driver.runtimeIdentity(signal),{fixtureRunId:expected});
  const pid=r.handles.get('serve')!.pid;r.registration(pid,{fixtureRunId:'foreign-token'});
  await assert.rejects(r.driver.runtimeIdentity(signal),/identity-mismatch|ownership-mismatch/);
  r.registration(pid,{fixtureRunId:expected});assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,true);
 }finally{await r.cleanup();}}
});

test('host RUN_ID binding rejects missing readback and invalid input before launch',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  r.registration(r.handles.get('serve')!.pid,{fixtureRunId:undefined});
  await assert.rejects(r.driver.runtimeIdentity(signal),/identity-mismatch/);
  r.registration(r.handles.get('serve')!.pid,{fixtureRunId:'1700000000'});
  assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,false);
  r.registration(r.handles.get('serve')!.pid,{fixtureRunId:undefined});
  assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,true);
 }finally{await r.cleanup();}
 const invalid=await setup('legacy-real-codex',true,false,'Bearer unsafe token');try{
  await assert.rejects(invalid.lifecycle.acquire(invalid.request,new AbortController().signal),/identity-mismatch/);
  assert.equal(invalid.starts.length,0);assert.equal(invalid.runs.length,0);assert.equal(invalid.captures.length,0);
 }finally{await invalid.cleanup();}
});

test('host publishes creation receipts for both owned workspaces and enrolls only actual legacy store rows',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const owner={leaseId:a.lease.id,runId:'test-run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
  const store=await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence'));
  const roster=await r.driver.ownedWorkspaceRoster(owner,store,signal);
  assert.deepEqual(roster.map(value=>value.workspaceId),['E2E-WS-2','E2E-WS']);
  for(const record of roster){const fact=JSON.parse(await fs.readFile(await store.resolve(record.creationReceipt.id),'utf8'));
    assert.equal(fact.storeGeneration,'actual-kernel-start');assert.deepEqual(fact.agentIds,[]);assert.equal(fact.repo,record.repo);}
  const fixture:OwnedFixture={...owner,workspaceId:a.workspaceId,repo:a.repo,ownedWorkspaces:roster,secrets:[],verify:async()=>{await r.driver.prepareObserve(signal);},
   expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',roots:new Map(),agents:new Map(),
   readApi:async()=>{throw new Error('unused');},readFiles:async()=>{throw new Error('unused');},resolveAgent:async()=>{throw new Error('unused');},dispose:async()=>{},
   readWorkspaceLegacyAgent:(ws:string,name:string,abort:AbortSignal)=>r.driver.readWorkspaceLegacyAgent(owner,ws,name,abort)};
  assert.equal(requireOwnedWorkspace(fixture,'E2E-WS-2',undefined,'legacy-agent-name').workspaceId,'E2E-WS-2');
  assert.throws(()=>requireOwnedWorkspace(fixture,'foreign',undefined,'legacy-agent-name'));
  assert.throws(()=>requireOwnedWorkspace(fixture,'E2E-WS','nova','legacy-agent-name'));
  r.overrideResponse(relative=>relative==='/api/workspaces/E2E-WS/agents'?{success:true,total:1,data:[{workspace_key:'E2E-WS',name:'nova',parent:'',repos:[],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T00:01:00Z'}]}:undefined);
  await enrollOwnedLegacyAgent(fixture,'E2E-WS','nova',signal,store);
  assert.equal(requireOwnedWorkspace(fixture,'E2E-WS','nova','legacy-agent-name').workspaceId,'E2E-WS');
  const enrolled=fixture.ownedWorkspaces!.find(value=>value.workspaceId==='E2E-WS')!;
  const fact=JSON.parse(await fs.readFile(await store.resolve(enrolled.enrollmentReceipts[0]!.id),'utf8'));
  assert.equal(fact.name,'nova');assert.equal(fact.createdAt,'2026-10-09T00:00:00Z');assert.equal(fact.parentName,null);
  const before=r.requests.length;
  await assert.rejects(r.driver.readWorkspaceLegacyAgent(owner,'foreign','nova',signal));assert.equal(r.requests.length,before);
  await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});

async function setupBoundWorker(){
 const r=await setup('legacy-deterministic',true,false,undefined,true),signal=new AbortController().signal;
 const acquired=await r.lifecycle.acquire(r.request,signal);
 const owner={leaseId:acquired.lease.id,runId:'test-run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
 const store=await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence'));
 const roster=await r.driver.ownedWorkspaceRoster(owner,store,signal);
 const actor={workspace_key:'E2E-WS',name:'nova',repos:[],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T00:00:00Z'};
 r.overrideResponse(relative=>relative==='/api/workspaces/E2E-WS/agents'?{success:true,data:[actor],total:1}:undefined);
 const fixture:OwnedFixture={...owner,workspaceId:acquired.workspaceId,repo:acquired.repo,ownedWorkspaces:roster,secrets:[],
  expiresAtUtcMs:Number.MAX_SAFE_INTEGER,evidenceClass:'deterministic',roots:new Map(),agents:new Map(),
  verify:async()=>{},readApi:async()=>{throw Error('unused');},readFiles:async()=>{throw Error('unused');},
  resolveAgent:async()=>{throw Error('unused');},dispose:async()=>{},
  readWorkspaceLegacyAgent:(ws,name,abort)=>r.driver.readWorkspaceLegacyAgent(owner,ws,name,abort)};
 r.driver.bindOwnedFixture(fixture,store);
 const cwd=r.driver.workspaceRoot,worktree=path.join(r.driver.runtimeRoot,'runtime','worker-worktree');await fs.mkdir(worktree);
 r.commonDir(worktree,path.join(cwd,'.git'));
 const daemon=r.handles.get('daemon')!,directory=path.join(cwd,'actual-daemon-state');await fs.mkdir(directory);
 const configDirectory=path.join(r.driver.configurationRoot,'workspaces','E2E-WS');await fs.mkdir(configDirectory,{recursive:true});
 const row={worktree:'nova',role:'task',pid:1700,status:'running',worktree_path:worktree,current_backend:'codex',last_start:'2026-10-09T01:00:00Z'};
 const stateFile=path.join(directory,'daemon-agents.json');
 await fs.writeFile(path.join(configDirectory,'daemon.pid'),JSON.stringify({pid:daemon.pid,cwd,socket:path.join(directory,'daemon.sock'),started_at:'2026-10-09T00:30:00Z'}));
 await fs.writeFile(stateFile,JSON.stringify({pid:daemon.pid,started_at:'2026-10-09T00:30:01Z',agents:[row]}));
 r.registration(row.pid,{generation:'actual-worker-start',executable:r.config.loomBinary,parentPid:daemon.pid,
  argvSha256:hash(Buffer.from([r.config.loomBinary,'task',worktree,'--auto','--daemon-mode','--backend','codex'].join('\0')+'\0'))});
 return {...r,signal,acquired,actor,row,stateFile,daemon};
}

test('actual Host worker hook binds product metadata to canonical actor, worktree and kernel parent',async()=>{
 const r=await setupBoundWorker();try{
  const before=r.starts.length,facts=await r.driver.refreshOwnedProductProcesses(r.signal);
  assert.equal(facts.length,1);assert.equal(facts[0]?.agentId,'nova');assert.equal(facts[0]?.generation,'actual-worker-start');
  assert.equal(r.starts.length,before);assert.equal(r.captures.filter(pid=>pid===1700).length,1);
  assert.deepEqual(await r.driver.refreshOwnedProductProcesses(r.signal),facts);
  assert.equal((await r.driver.inspectOwnedProcess(facts[0]!.id,facts[0]!.generation,r.signal)).state,'running');
  assert.equal((await r.lifecycle.release(r.acquired.lease.id,r.request.runId)).released,true);
 }finally{await r.cleanup();}
});

test('Host worker discovery serializes mutation, parent stop and cleanup across deferred kernel capture',async()=>{
 const r=await setupBoundWorker();let entered!:()=>void,release!:()=>void;try{
  const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  r.onCapture(async pid=>{if(pid===1700){entered();await blocked;}});
  const refresh=r.driver.refreshOwnedProductProcesses(r.signal);await ready;
  const requests=r.requests.length,stops=r.registeredStops.length;
  await assert.rejects(r.driver.requestOwnedHttp('api','DELETE','/api/workspaces/E2E-WS',null,r.signal));
  await assert.rejects(r.driver.stopOwnedProcess('daemon',r.daemon.generation,r.signal));
  await assert.rejects(r.driver.prepareCleanup(r.signal));
  const runs=r.runs.length;await assert.rejects(r.driver.launchOwnedCli(['usage'],{},'',true,r.signal));assert.equal(r.runs.length,runs);
  assert.equal(r.requests.length,requests);assert.equal(r.registeredStops.length,stops);assert.equal(r.daemon.state(),'running');
  release();assert.equal((await refresh).length,1);
  assert.equal((await r.lifecycle.release(r.acquired.lease.id,r.request.runId)).released,true);
 }finally{release?.();await r.cleanup();}
});

test('CLI launch reserves readiness and completion before worker discovery, cleanup or mutations',async()=>{
 for(const phase of ['ready','completion'] as const){
  const r=await setupBoundWorker();let entered!:()=>void,release!:()=>void;
  let cli:ReturnType<HostFixtureDriver['launchOwnedCli']>|undefined;
  try{
   const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
   r.onCliAwait(async actual=>{if(actual===phase){entered();await blocked;}});
   cli=r.driver.launchOwnedCli(['usage'],{},'',true,r.signal);await ready;
   const requests=r.requests.length,stops=r.registeredStops.length,runs=r.runs.length;
   await assert.rejects(r.driver.refreshOwnedProductProcesses(r.signal));assert.equal(r.captures.includes(1700),false);
   await assert.rejects(r.driver.prepareCleanup(r.signal));
   await assert.rejects(r.driver.stopOwnedProcess('daemon',r.daemon.generation,r.signal));
   await assert.rejects(r.driver.restartOwnedProcess('daemon',r.daemon.generation,r.signal));
   await assert.rejects(r.driver.requestOwnedHttp('api','DELETE','/api/workspaces/E2E-WS',null,r.signal));
   await assert.rejects(r.driver.launchOwnedCli(['usage'],{},'',true,r.signal));
   assert.equal(r.requests.length,requests);assert.equal(r.registeredStops.length,stops);assert.equal(r.runs.length,runs);
   assert.equal(r.daemon.state(),'running');release();assert.equal((await cli).completion.complete,true);
   assert.equal((await r.driver.refreshOwnedProductProcesses(r.signal)).length,1);
   assert.equal((await r.lifecycle.release(r.acquired.lease.id,r.request.runId)).released,true);
  }finally{release?.();await cli?.catch(()=>{});await r.cleanup();}
 }
});

test('Host worker hook denies foreign actor and parent before granting process authority',async()=>{
 const r=await setupBoundWorker();try{
  r.actor.workspace_key='foreign';await assert.rejects(r.driver.refreshOwnedProductProcesses(r.signal));
  assert.equal(r.captures.includes(1700),false);r.actor.workspace_key='E2E-WS';
  r.registration(1700,{parentPid:999});await assert.rejects(r.driver.refreshOwnedProductProcesses(r.signal));
  assert.ok(r.abandoned.includes(1700));assert.equal(r.registeredStops.length,0);
 }finally{await r.cleanup();}
});

test('Host cleanup reserves its operation before awaited inspection so worker capture cannot overlap',async()=>{
 const r=await setupBoundWorker();let entered!:()=>void,release!:()=>void;try{
  const ready=new Promise<void>(resolve=>{entered=resolve;}),blocked=new Promise<void>(resolve=>{release=resolve;});
  const inspect=r.driver.inspect.bind(r.driver);
  r.driver.inspect=async resource=>{if(resource.kind==='ports'){entered();await blocked;}return inspect(resource);};
  const removal=r.driver.remove({id:'ports',kind:'ports',generation:r.acquired.lease.id});await ready;
  await assert.rejects(r.driver.refreshOwnedProductProcesses(r.signal));assert.equal(r.captures.includes(1700),false);
  release();await removal;r.driver.inspect=inspect;
  assert.equal((await r.lifecycle.release(r.acquired.lease.id,r.request.runId)).released,true);
 }finally{release?.();await r.cleanup();}
});
test('canonical registry production binding retains both actual host workspaces and refuses foreign store reads',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const root=path.dirname(path.dirname(new URL(import.meta.url).pathname));
  const pin=calculateImplementationPin(root,['fixture/providers.ts','fixture/host.ts','fixture/workspace-records.ts'],'fixture/providers.ts','createFixtureProviders');
  const options=productionFixtureOptions(pin,pin.sha256,[r.plan],r.config,r.config);options.driver=()=>r.driver;
  const registry=new CapabilityRegistry();for(const provider of createFixtureProviders(options))registry.register(provider);
  const context=createCapabilityContext({file:'owned-workspaces.test.yaml',line:1},registry,'00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000002');
  const acquired=await registry.invoke({id:'loom.fixture.acquire',version:1,input:{}},{...r.request,runId:context.runId},context);
  assert.equal(acquired.availability,'observed');const leaseId=(acquired.data as {lease:{id:string}}).lease.id;
  assert.equal((acquired.data as {fixtureRunId:string}).fixtureRunId,'1700000000');
  assert.notEqual((acquired.data as {fixtureRunId:string}).fixtureRunId,context.runId);
  const fixture=await getFixture(context,leaseId);assert.equal(fixture.ownedWorkspaces!.length,2);
  await r.driver.createOwnedWorkspaceFixture('legacy-e2e-repo','E2E-WS-AGENT','e2e-ws-agent',context.signal);
  assert.equal(fixture.ownedWorkspaces!.length,3);
  const created=requireOwnedWorkspace(fixture,'E2E-WS-AGENT',undefined,'legacy-agent-name');
  assert.equal(created.repoName,'agent-repo');assert.equal(created.sourceRepoId,'agent-repo');assert.equal(path.basename(created.repo),'agent-repo');
  assert.equal(requireOwnedWorkspace(fixture,'E2E-WS-2',undefined,'legacy-agent-name').workspaceId,'E2E-WS-2');
  const before=r.requests.length;await assert.rejects(fixture.readWorkspaceLegacyAgent!('foreign','nova',context.signal));assert.equal(r.requests.length,before);
  assert.equal((await registry.invoke({id:'loom.fixture.release',version:1,input:{}},{leaseId},context)).availability,'observed');
 }finally{await r.cleanup();}
});
test('finite legacy repository setup retains successful creation facts and cannot invent native setup semantics',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const owner={leaseId:a.lease.id,runId:'test-run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
  const store=await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence'));
  await r.driver.ownedWorkspaceRoster(owner,store,signal);
  const before=r.runs.length,record=await r.driver.createOwnedWorkspaceFixture('legacy-e2e-repo','E2E-WS-AGENT','e2e-ws-agent',signal);
  assert.equal(record.workspaceId,'E2E-WS-AGENT');assert.equal(record.identityKind,'legacy-agent-name');assert.deepEqual(record.agentIds,[]);
  const fact=JSON.parse(await fs.readFile(await store.resolve(record.creationReceipt.id),'utf8'));
  assert.equal(fact.storeGeneration,'actual-kernel-start');assert.equal(fact.repo,record.repo);assert.equal(fact.leaseId,a.lease.id);
  assert.deepEqual(r.runs.slice(before).filter(c=>c.argv[0]==='init'||c.argv.includes('commit')).map(c=>c.argv),
   [['init','-q'],['-c','user.email=e2e@x','-c','user.name=e2e','commit','--allow-empty','-m','init','-q']]);
  assert.deepEqual(await fs.readdir(record.repo),['.git']);
  const effects=r.runs.length,requests=r.requests.length;
  for(const args of [['legacy-e2e-repo','E2E-WS-AGENT','e2e-ws-agent'],['legacy-e2e-repo','FOREIGN','e2e-ws-other'],
    ['agent-api-source-repo','E2E-AGV1-UI','e2e-agv1-ui']] as const){await assert.rejects(r.driver.createOwnedWorkspaceFixture(args[0],args[1],args[2],signal));}
  assert.equal(r.runs.length,effects);assert.equal(r.requests.length,requests);
  assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
 }finally{await r.cleanup();}
});

test('production factory rejects missing store capture before host preflight, auth or allocation',async()=>{
 const r=await setup('legacy-real-codex');try{
  const root=path.dirname(path.dirname(new URL(import.meta.url).pathname));
  const pin=calculateImplementationPin(root,['fixture/providers.ts'],'fixture/providers.ts','createFixtureProviders');
  const options=productionFixtureOptions(pin,pin.sha256,[r.plan],r.config,r.config);
  assert.throws(()=>options.driver('legacy-real-codex'));
  assert.equal(r.runs.length,0);assert.equal(r.starts.length,0);assert.equal(r.driver.runtimeRoot,'');
 }finally{await r.cleanup();}
});
test('host workspace ownership rejects replaced store before actor discovery',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const owner={leaseId:a.lease.id,runId:'test-run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
  await r.driver.ownedWorkspaceRoster(owner,await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence')),signal);
  const filename=path.join(r.driver.configurationRoot,'fleet-db/runtime.json'),original=await fs.readFile(filename,'utf8');
  await fs.writeFile(filename,original.replace('999','998'));const before=r.requests.length;
  await assert.rejects(r.driver.readWorkspaceLegacyAgent(owner,'E2E-WS','nova',signal));assert.equal(r.requests.length,before);
  await fs.writeFile(filename,original);await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('legacy worktree reads use actual product resolution and exact source Git identity',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),owner={leaseId:a.lease.id,runId:'test-run',suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
  await r.driver.ownedWorkspaceRoster(owner,await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence')),signal);
  r.overrideResponse(relative=>relative==='/api/workspaces/E2E-WS/agents'?{success:true,total:1,data:[{workspace_key:'E2E-WS',name:'nova',repos:[],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T00:01:00Z'}]}:undefined);
  const worktree=path.join(r.driver.runtimeRoot,'runtime','actual-product-worktree');await fs.mkdir(worktree);
  r.commonDir(worktree,path.join(r.driver.workspaceRoot,'.git'));
  const status={ok:true,workspace:{key:'E2E-WS'},agents:[{name:'nova',worktree_path:worktree,worktree_ready:true}]};r.cliOutput(status);
  const resolved=await r.driver.resolveLegacyWorktree('E2E-WS','nova',signal);
  assert.equal(resolved.root.path,worktree);assert.equal(resolved.branch,'actual-product-branch');assert.equal(resolved.commonDir,path.join(r.driver.workspaceRoot,'.git'));
  assert.deepEqual(r.runs.filter(value=>value.argv[0]==='workspace').at(-1)!.argv,['workspace','ops','diagnose','E2E-WS','--json']);
  assert.equal(await r.driver.readLegacyWorktreeHead('E2E-WS','nova',signal),'a'.repeat(40));
  r.head('partial');await assert.rejects(r.driver.readLegacyWorktreeHead('E2E-WS','nova',signal));r.head('a'.repeat(40));
  for(const bad of [{...status,workspace:{key:'foreign'}},{...status,agents:[]},{...status,agents:[{...status.agents[0],worktree_ready:false}]},
   {...status,agents:[status.agents[0],status.agents[0]]},{...status,agents:[{...status.agents[0],worktree_path:r.config.hostHome}]}]){
   r.cliOutput(bad);await assert.rejects(r.driver.resolveLegacyWorktree('E2E-WS','nova',signal));
  }
  r.cliOutput(status);r.commonDir(worktree,path.join(r.driver.runtimeRoot,'runtime','e2e-workspace-2','.git'));
  await assert.rejects(r.driver.resolveLegacyWorktree('E2E-WS','nova',signal));
  r.commonDir(worktree,path.join(r.driver.workspaceRoot,'.git'));let replace=true;
  r.onRun(async command=>{if(replace&&command.argv[0]==='symbolic-ref'){replace=false;await fs.rename(worktree,worktree+'-prior');await fs.mkdir(worktree);}});
  await assert.rejects(r.driver.resolveLegacyWorktree('E2E-WS','nova',signal));
  const before=r.runs.length;await assert.rejects(r.driver.resolveLegacyWorktree('foreign','nova',signal));assert.equal(r.runs.length,before);
  await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});

test('multi-assigned beta diagnostic selects its actual physical source under the canonical fixture roster',async()=>{
 const r=await setup('legacy-deterministic',true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  const alpha=path.join(r.driver.runtimeRoot,'runtime','alpha'),beta=path.join(r.driver.runtimeRoot,'runtime','beta');
  for(const repo of [alpha,beta])await fs.mkdir(path.join(repo,'.git'),{recursive:true});
  // Exercise the production private creation recorder with successful fixed
  // HTTP transport facts; native source factory provisioning is a separate gate.
  const records=Reflect.get(r.driver,'workspaceRecords') as HostWorkspaceRecords;
  const directory=path.join(r.driver.configurationRoot,'fleet-db'),stamp=await fs.lstat(directory);
  const before={storeId:`${directory}#${stamp.dev}:${stamp.ino}`,storeGeneration:'actual-kernel-start'};
  const created=await r.driver.requestOwnedHttp('api','POST','/api/workspaces',{name:'owned-multi',type:'empty',repos:[alpha,beta]},signal);
  await records.captureCreated('OWNED-MULTI',[alpha,beta],created,before,signal);
  const actual=(created.body as {data:{id:string;repos:{path:string;name:string;source_repo_id:string;groups:string[]}[]}}).data;
  const actor={workspace_key:'OWNED-MULTI',name:'nova',repos:['alpha','beta'],repo_groups:[],created_at:'2026-10-09T00:00:00Z',updated_at:'2026-10-09T01:00:00Z'};
  r.overrideResponse(relative=>relative==='/api/workspaces/OWNED-MULTI/agents'?{success:true,total:1,data:[actor]}:
   relative==='/api/workspaces/OWNED-MULTI'?{success:true,data:actual}:undefined);
  const owner={leaseId:a.lease.id,runId:r.request.runId,suiteId:'suite',scope:'case' as const,caseId:'case',profile:r.plan.profile};
  const evidence=await createEvidenceStore(path.join(r.driver.runtimeRoot,'evidence'));
  const fixture:OwnedFixture={...owner,workspaceId:a.workspaceId,repo:a.repo,
   ownedWorkspaces:await r.driver.ownedWorkspaceRoster(owner,evidence,signal),roots:new Map(),agents:new Map(),secrets:[],
   evidenceClass:'deterministic',expiresAtUtcMs:Number.MAX_SAFE_INTEGER,verify:abort=>r.driver.prepareObserve(abort),dispose:async()=>{},
   readApi:async()=>{throw new Error('unused');},readFiles:async()=>{throw new Error('unused');},resolveAgent:async()=>{throw new Error('unused');},
   readWorkspaceLegacyAgent:(ws,name,abort)=>r.driver.readWorkspaceLegacyAgent(owner,ws,name,abort)};
  r.driver.bindOwnedFixture(fixture,evidence);
  const worktree=path.join(r.driver.runtimeRoot,'runtime','actual-beta-worktree');await fs.mkdir(worktree);r.commonDir(worktree,path.join(beta,'.git'));
  r.cliOutput({ok:true,workspace:{key:'OWNED-MULTI'},agents:[{name:'nova',worktree_path:worktree,worktree_ready:true}]});
  const fact=await r.driver.readWorkspaceLegacyAgent(owner,'OWNED-MULTI','nova',signal);assert.equal(fact.repo,null);assert.equal(fact.commonDir,null);
  const selected=await r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal,'beta');
  assert.equal(selected.root.path,worktree);assert.equal(selected.commonDir,path.join(beta,'.git'));
  assert.equal(requireOwnedWorkspace(fixture,'OWNED-MULTI','nova','legacy-agent-name','beta').repo,beta);
  assert.throws(()=>requireOwnedWorkspace(fixture,'OWNED-MULTI','nova','legacy-agent-name'));
  actual.repos.reverse();assert.equal((await r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal)).commonDir,path.join(beta,'.git'));
  assert.equal(await r.driver.readLegacyWorktreeHead('OWNED-MULTI','nova',signal,'beta'),'a'.repeat(40));
  await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal,'alpha'));
  actor.repos=['alpha'];await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal));actor.repos=['alpha','beta'];
  r.commonDir(worktree,path.join(r.driver.workspaceRoot,'.git'));await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal));
  r.commonDir(worktree,path.join(beta,'.git'));let change=true;
  r.onRun(async command=>{if(change&&command.argv[0]==='symbolic-ref'){change=false;actor.repos=['alpha'];}});
  await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal));actor.repos=['alpha','beta'];
  const launches=()=>r.runs.filter(command=>command.executable===r.config.loomBinary).length,beforeLaunches=launches();
  actual.repos[0]!.groups=['reassigned'];await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal));actual.repos[0]!.groups=[];assert.equal(launches(),beforeLaunches);
  r.registration(999,{generation:'foreign-generation'});await assert.rejects(r.driver.resolveLegacyWorktree('OWNED-MULTI','nova',signal));r.registration(999,{});assert.equal(launches(),beforeLaunches);
  await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});

for (const profile of legacyProfiles) test(`${profile}: production host driver uses owned paths and fixed profile startup`, async () => {
  const r = await setup(profile);
  try {
    const acquired = await r.lifecycle.acquire(r.request, new AbortController().signal);
    assert.equal(acquired.workspaceId, 'E2E-WS'); assert.equal(acquired.repo, r.driver.workspaceRoot);
    assert.equal(acquired.repo.startsWith(r.root + '/loom-aft-host-'), true);
    assert.deepEqual(r.starts.map(start => start.id), profile === 'legacy-deterministic' ? ['fake-model', 'serve', 'frontend'] : profile === 'legacy-real-codex' ? ['codex-preflight','serve','frontend'] : ['serve', 'frontend']);
    const serve = r.starts.find(start => start.id === 'serve')!;
    assert.ok(serve.command.env.LOOM_CONFIG_DIR!.startsWith(acquired.repo));
    assert.equal(serve.command.env.LOOM_LEAD_CONTROLLED, '1');
    assert.equal(serve.command.env.GITHUB_TOKEN, undefined); assert.equal(serve.command.env.SSH_AUTH_SOCK, undefined);
    assert.equal(serve.command.env.OPENAI_API_KEY, profile === 'legacy-deterministic' ? 'stub-e2e' : undefined);
    assert.equal(serve.command.env.ANTHROPIC_API_KEY, undefined); assert.equal(serve.command.env.CURSOR_API_KEY, undefined);
    assert.equal(serve.command.env.GIT_CONFIG_VALUE_0, ''); assert.equal(serve.command.env.GIT_CONFIG_VALUE_2, 'always');
    assert.ok(serve.command.env.PATH!.split(':')[1] === path.join(r.source, 'e2e', profile === 'legacy-deterministic' ? 'stubs' : `stubs-real-${profile.replace('legacy-real-', '')}`));
    if (profile === 'legacy-real-cursor') assert.ok(r.runs.some(command => command.argv[0] === 'status'));
    assert.ok(!r.runs.some(command => command.argv.some(arg => arg.includes('start-e2e-server') || arg.includes('run-aft'))));
    assert.ok(!r.runs.some(command => command.argv[0] === 'config'));
    assert.equal((await r.lifecycle.observe(acquired.lease.id, 'test-run')).services.length, r.starts.length);
    assert.equal((await r.lifecycle.release(acquired.lease.id, 'test-run')).released, true);
    assert.deepEqual(r.stopped, [...(profile === 'legacy-real-codex' ? ['codex-preflight'] : []), ...r.starts.map(start => start.id).reverse()]);
    assert.equal((await fs.readdir(path.join(r.driver.runtimeRoot, 'evidence'))).length, 3);
    await assert.rejects(fs.lstat(path.join(r.driver.runtimeRoot, 'runtime')), { code: 'ENOENT' });
  } finally { await r.cleanup(); }
});
test('Cursor backend-default omits ineffective model overrides and named-model selection fails before auth/startup',async()=>{
 const r=await setup('legacy-real-cursor');try{
 const acquired=await r.lifecycle.acquire(r.request,new AbortController().signal);
 assert.deepEqual(r.driver.executionRouting.modelSelection,{kind:'backend-default'});
 assert.equal('LOOM_AGENT_MODEL' in r.driver.cliRegistration.env,false);assert.equal('LOOM_OPENCODE_MODEL' in r.driver.cliRegistration.env,false);
 await r.lifecycle.release(acquired.lease.id,r.request.runId);
 const before=r.runs.length,starts=r.starts.length;
 await assert.rejects(r.driver.preflight({...r.plan,model:'named-cursor-model'},new AbortController().signal),e=>e instanceof FixtureError&&e.code==='unsupported-capability');
 assert.equal(r.runs.length,before);assert.equal(r.starts.length,starts);
 }finally{await r.cleanup();}
});
test('host captures a retained startup baseline and refuses stale or stopped service restoration',async()=>{
 const r=await setup('legacy-deterministic');try{
 const acquired=await r.lifecycle.acquire(r.request,new AbortController().signal);const signal=new AbortController().signal;
 const fact=await r.driver.freshFixtureBaseline('fake-model',signal);assert.equal(fact.generation,r.driver.processesById.get('fake-model')!.generation);
 assert.deepEqual(await r.driver.resetFixtureBaseline('fake-model',fact.generation,signal),{status:200,body:{ok:true}});
 await assert.rejects(r.driver.resetFixtureBaseline('fake-model','foreign-generation',signal));
 await assert.rejects(r.driver.requestOwnedHttp('fake-model','POST','/__reset',null,signal,'foreign-generation'));
 await r.driver.stopOwnedProcess('fake-model',fact.generation,signal);await assert.rejects(r.driver.freshFixtureBaseline('fake-model',signal));
 await r.lifecycle.release(acquired.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('HTTP baseline mutation rejects a replacement during awaited process inspection before calling transport',async()=>{
 const r=await setup('legacy-deterministic');try{
 const acquired=await r.lifecycle.acquire(r.request,new AbortController().signal),signal=new AbortController().signal;
 const fact=await r.driver.freshFixtureBaseline('fake-model',signal);const inspect=r.driver.inspectOwnedProcess.bind(r.driver);let armed=true;
 r.driver.inspectOwnedProcess=async(id,generation,abort)=>{const result=await inspect(id,generation,abort);if(armed&&id==='fake-model'){armed=false;await r.driver.restartOwnedProcess(id,generation,abort);}return result;};
 await assert.rejects(r.driver.requestOwnedHttp('fake-model','POST','/__reset',null,signal,fact.generation));
 assert.equal(r.requests.filter(request=>request.method==='POST'&&request.relative==='/__reset').length,0);
 assert.equal((await r.driver.freshFixtureBaseline('fake-model',signal)).generation,fact.generation);await r.lifecycle.release(acquired.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('in-flight HTTP holds exact service authority and rejects concurrent stop and restart',async()=>{
 const r=await setup('legacy-deterministic');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),fact=await r.driver.freshFixtureBaseline('fake-model',signal);
 let enter!:()=>void,leave!:()=>void;const entered=new Promise<void>(resolve=>{enter=resolve;}),gate=new Promise<void>(resolve=>{leave=resolve;});
 r.onHttp(async(method,relative)=>{if(method==='POST'&&relative==='/__reset'){enter();await gate;}});
 const pending=r.driver.requestOwnedHttp('fake-model','POST','/__reset',null,signal,fact.generation);await entered;
 const starts=r.starts.length,stops=r.stopped.length;
 await assert.rejects(r.driver.stopOwnedProcess('fake-model',fact.generation,signal));
 await assert.rejects(r.driver.restartOwnedProcess('fake-model',fact.generation,signal));
 assert.equal(r.starts.length,starts);assert.equal(r.stopped.length,stops);
 leave();assert.deepEqual(await pending,{status:200,body:{ok:true}});
 assert.deepEqual(await r.driver.resetFixtureBaseline('fake-model',fact.generation,signal),{status:200,body:{ok:true}});
 await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('replacement during deferred inspection never stops or restarts the replacement',async()=>{
 for(const action of ['stopOwnedProcess','restartOwnedProcess'] as const){
 const r=await setup('legacy-deterministic');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.driver.processesById.get('serve')!;
 let enter!:()=>void,leave!:()=>void;const entered=new Promise<void>(resolve=>{enter=resolve;}),gate=new Promise<void>(resolve=>{leave=resolve;});
 const inspect=r.driver.inspectOwnedProcess.bind(r.driver);r.driver.inspectOwnedProcess=async(id,generation,abort)=>{const fact=await inspect(id,generation,abort);if(id==='serve'){enter();await gate;}return fact;};
 let foreignStops=0;const foreign:OwnedProcess={...before,generation:'foreign',stop:async()=>{foreignStops++;}};
 const pending=r.driver[action]('serve',before.generation,signal);await entered;
 const starts=r.starts.length;(r.driver.processesById as Map<string,OwnedProcess>).set('serve',foreign);leave();
 await assert.rejects(pending);assert.equal(foreignStops,0);assert.equal(r.starts.length,starts);
 (r.driver.processesById as Map<string,OwnedProcess>).set('serve',before);r.driver.inspectOwnedProcess=inspect;
 await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}}
});
test('deferred HTTP inspection rejects a replaced handle before any mutation dispatch',async()=>{
 const r=await setup('legacy-deterministic');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.driver.processesById.get('fake-model')!;
 let enter!:()=>void,leave!:()=>void;const entered=new Promise<void>(resolve=>{enter=resolve;}),gate=new Promise<void>(resolve=>{leave=resolve;});
 const inspect=r.driver.inspectOwnedProcess.bind(r.driver);r.driver.inspectOwnedProcess=async(id,generation,abort)=>{const fact=await inspect(id,generation,abort);if(id==='fake-model'){enter();await gate;}return fact;};
 const pending=r.driver.requestOwnedHttp('fake-model','POST','/__reset',null,signal,before.generation);await entered;
 (r.driver.processesById as Map<string,OwnedProcess>).set('fake-model',{...before,generation:'foreign'});leave();
 await assert.rejects(pending);assert.equal(r.requests.filter(x=>x.method==='POST'&&x.relative==='/__reset').length,0);
 (r.driver.processesById as Map<string,OwnedProcess>).set('fake-model',before);r.driver.inspectOwnedProcess=inspect;
 await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('HTTP response from a changed service is uncertain and cannot report success or retry',async()=>{
 const r=await setup('legacy-deterministic');try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.driver.processesById.get('fake-model')!;
 r.onHttp(async(method,relative)=>{if(method==='POST'&&relative==='/__reset')(r.driver.processesById as Map<string,OwnedProcess>).set('fake-model',{...before,generation:'replacement'});});
 await assert.rejects(r.driver.requestOwnedHttp('fake-model','POST','/__reset',null,signal,before.generation));
 assert.equal(r.requests.filter(x=>x.method==='POST'&&x.relative==='/__reset').length,1);
 (r.driver.processesById as Map<string,OwnedProcess>).set('fake-model',before);r.onHttp(async()=>{});await r.lifecycle.release(a.lease.id,r.request.runId);
 }finally{await r.cleanup();}
});
test('partial host startup stops exact launched handle and preserves safe failure evidence', async () => {
  const r = await setup('legacy-deterministic'); r.failService('serve');
  try {
    await assert.rejects(r.lifecycle.acquire(r.request, new AbortController().signal), FixtureError);
    assert.deepEqual(r.stopped, ['serve', 'fake-model']);
    const diagnostics = await fs.readdir(path.join(r.driver.runtimeRoot, 'evidence'));
    const content = await fs.readFile(path.join(r.driver.runtimeRoot, 'evidence', diagnostics[0]!), 'utf8');
    assert.equal(content.includes('private-ready-token'), false);
  } finally { await r.cleanup(); }
});
test('failed host cleanup retains runtime, account lock and exact process generation for retry', async () => {
  const r = await setup('legacy-real-codex');
  try {
    const acquired = await r.lifecycle.acquire(r.request, new AbortController().signal); r.failStop('serve');
    const failed = await r.lifecycle.release(acquired.lease.id, 'test-run');
    assert.equal(failed.released, false); assert.ok(failed.remainingOwnedResources.includes('serve'));
    assert.ok((await fs.lstat(path.join(r.config.lockParent, 'aft-live.codex.lock'))).isFile());
    assert.ok((await fs.lstat(path.join(r.driver.runtimeRoot, 'runtime'))).isDirectory());
    r.failStop(''); assert.equal((await r.lifecycle.release(acquired.lease.id, 'test-run')).released, true);
  } finally { await r.cleanup(); }
});
test('foreign host account lock fails without removing it or launching any process', async () => {
  const r = await setup('legacy-real-claude'); const lock = path.join(r.config.lockParent, 'aft-live.claude.lock');
  try {
    await fs.writeFile(lock, 'foreign lock');
    await assert.rejects(r.lifecycle.acquire(r.request, new AbortController().signal));
    assert.equal(await fs.readFile(lock, 'utf8'), 'foreign lock'); assert.deepEqual(r.starts, []);
  } finally { await r.cleanup(); }
});
test('changed source bytes fail before backend auth status or process allocation', async () => {
  const r = await setup('legacy-real-cursor');
  try {
    await fs.writeFile(path.join(r.source, 'tracked.txt'), 'changed');
    await assert.rejects(r.lifecycle.acquire(r.request, new AbortController().signal));
    assert.equal(r.runs.some(command => command.executable === r.config.realBinaries.cursor!.executable && command.argv[0] === 'status'), false); assert.deepEqual(r.starts, []);
  } finally { await r.cleanup(); }
});
test('manifest refuses a symlink or missing entry rather than proving source identity', async () => {
  const r = await setup('legacy-deterministic');
  try {
    await fs.unlink(path.join(r.source, 'tracked.txt')); await fs.symlink(path.join(r.config.loom.build.root, 'loom'), path.join(r.source, 'tracked.txt'));
    await assert.rejects(verifyManifest(r.config.loom.source, r.plan.loomRevision.sourceManifestSha256));
    await fs.unlink(path.join(r.source, 'tracked.txt'));
    await assert.rejects(verifyManifest(r.config.loom.source, r.plan.loomRevision.sourceManifestSha256));
  } finally { await r.cleanup(); }
});
test('failed readiness response remains unverified and triggers cleanup', async () => {
  const r = await setup('legacy-deterministic'); r.failHttp();
  try {
    await assert.rejects(r.lifecycle.acquire(r.request, new AbortController().signal));
    assert.deepEqual(r.stopped, ['fake-model']);
    assert.deepEqual(r.starts.map(start=>start.id),['fake-model']);
  } finally { await r.cleanup(); }
});

test('private CLI hooks enroll owned commands and reject executable or auth override inputs', async () => {
  const r = await setup('legacy-deterministic');
  try {
    const acquired = await r.lifecycle.acquire(r.request, new AbortController().signal);
    const result = await r.driver.launchOwnedCli(['usage','--format','json','--agent','owned'], { LOOM_WORKSPACE_ID:'E2E-WS' }, '', true, new AbortController().signal);
    assert.equal(result.completion.exitCode, 0); assert.equal(result.completion.complete, true);
    const services = (await r.lifecycle.observe(acquired.lease.id, 'test-run')).services;
    assert.ok(services.some(s => s.id === result.id && s.generation === result.generation));
    await assert.rejects(r.driver.launchOwnedCli(['bash','-c','bad'],{},'',true,new AbortController().signal));
    await assert.rejects(r.driver.launchOwnedCli(['usage'],{ OPENAI_API_KEY:'private' },'',true,new AbortController().signal));
    assert.equal(r.driver.cliRegistration.binary, r.config.loomBinary);
    assert.equal((await r.lifecycle.release(acquired.lease.id, 'test-run')).released, true);
  } finally { await r.cleanup(); }
});
test('owned restart replaces the recorded generation; stale and foreign actions never stop a process', async () => {
  const r = await setup('legacy-deterministic');
  try {
    const acquired = await r.lifecycle.acquire(r.request, new AbortController().signal);
    const before = r.driver.processesById.get('serve')!.generation;
    await assert.rejects(r.driver.stopOwnedProcess('serve','foreign',new AbortController().signal)); assert.deepEqual(r.stopped, []);
    const restarted = await r.driver.restartOwnedProcess('serve',before,new AbortController().signal);
    assert.notEqual(restarted.afterGeneration,before);
    const observed = await r.lifecycle.observe(acquired.lease.id,'test-run');
    assert.equal(observed.services.find(s => s.id === 'serve')!.generation,restarted.afterGeneration);
    await assert.rejects(r.driver.restartOwnedProcess('serve',before,new AbortController().signal));
    assert.equal((await r.lifecycle.release(acquired.lease.id,'test-run')).released,true);
  } finally { await r.cleanup(); }
});
test('restoration failure blocks process/path cleanup and retries after cancellation', async () => {
  const r = await setup('legacy-deterministic'); let fails = true; let attempts = 0;
  try {
    const acquired = await r.lifecycle.acquire(r.request,new AbortController().signal);
    r.driver.enrollCleanup(async () => { attempts++; if(fails) throw new Error('Bearer private-restore-token'); });
    const first = await r.lifecycle.release(acquired.lease.id,'test-run'); assert.equal(first.released,false); assert.deepEqual(r.stopped,[]);
    assert.ok(await fs.lstat(r.driver.workspaceRoot)); assert.equal((await fs.readFile(first.receipt.id,'utf8')).includes('private-restore-token'),false);
    fails=false; assert.equal((await r.lifecycle.release(acquired.lease.id,'test-run')).released,true); assert.equal(attempts,2);
  } finally { await r.cleanup(); }
});

test('proven synchronous launch failure retires its pre-spawn intent and allows exact cleanup',async()=>{
 const r=await setup('legacy-deterministic');r.failSpawn();try{
  await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal),error=>{assert.ok(error instanceof FixtureError);assert.equal(error.remainingOwnedResources.length,0);return true;});assert.deepEqual(r.starts,[]);
 }finally{await r.cleanup();}
});
test('proven synchronous CLI failure retires only its intent; completed fixture still releases',async()=>{
 const r=await setup('legacy-deterministic');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);r.failSpawn();await assert.rejects(r.driver.launchOwnedCli(['usage'],{},'',true,new AbortController().signal));assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);}finally{await r.cleanup();}
});
test('only deterministic bounded seed-worktree receives test support, unrelated daemon or env denied',async()=>{
 const r=await setup('legacy-deterministic');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);
 const result=await r.driver.launchOwnedCli(['daemon','seed-worktree','--workspace','E2E-WS','--agent','owned','--file','seed.txt','--content','-','--message','seed'],{LOOM_TESTSUPPORT:'1'},'seed bytes',true,new AbortController().signal);assert.equal(result.completion.stdout,'seed bytes');
 await assert.rejects(r.driver.launchOwnedCli(['daemon','rm'],{LOOM_TESTSUPPORT:'1'},'',true,new AbortController().signal));await assert.rejects(r.driver.launchOwnedCli(['usage'],{LOOM_TESTSUPPORT:'1'},'',true,new AbortController().signal));assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
 }finally{await r.cleanup();}
});
test('HTTP mutation refuses a stopped owned service before using its saved port',async()=>{
 const r=await setup('legacy-deterministic');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);const h=r.driver.processesById.get('serve')!;await r.driver.stopOwnedProcess('serve',h.generation,new AbortController().signal);await assert.rejects(r.driver.requestOwnedHttp('api','POST','/api/workspaces',{},new AbortController().signal));assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);}finally{await r.cleanup();}
});
test('fake-model origin is resolved from its current owned service and refused after stop',async()=>{
 const r=await setup('legacy-deterministic');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);
 assert.match(await r.driver.fakeModelOrigin(new AbortController().signal),/^http:\/\/127\.0\.0\.1:\d+$/);
 const h=r.driver.processesById.get('fake-model')!;await r.driver.stopOwnedProcess('fake-model',h.generation,new AbortController().signal);
 await assert.rejects(r.driver.fakeModelOrigin(new AbortController().signal));assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
 }finally{await r.cleanup();}
});
test('controlled Codex preflight cleans its exact app-server before stack startup',async()=>{
 const r=await setup('legacy-real-codex');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);
 const probe=r.starts[0]!;assert.equal(probe.id,'codex-preflight');assert.deepEqual(probe.command.argv,['app-server','--listen',r.protocols[0]]);assert.equal(probe.command.executable,r.config.realBinaries.codex!.executable);assert.equal(r.handles.get('codex-preflight')!.state(),'exited');assert.equal(r.stopped[0],'codex-preflight');assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
 }finally{await r.cleanup();}
});
test('failed Codex initialization launches no stack and still cleans the enrolled probe',async()=>{
 const r=await setup('legacy-real-codex');r.failService('codex-protocol');try{await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal));assert.deepEqual(r.starts.map(s=>s.id),['codex-preflight']);assert.equal(r.handles.get('codex-preflight')!.state(),'exited');}finally{await r.cleanup();}
});
test('Codex probe teardown failure retains exact probe and account lock for retry',async()=>{
 const r=await setup('legacy-real-codex');r.failStop('codex-preflight');try{let lease='';await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal),error=>{assert.ok(error instanceof FixtureError);lease=error.leaseId!;assert.ok(error.remainingOwnedResources.includes('codex-preflight'));return true;});assert.equal(r.starts.length,1);r.failStop('');assert.equal((await r.lifecycle.release(lease,'test-run')).released,true);}finally{await r.cleanup();}
});
test('paid task without exact reviewed authority and backend swap fail before CLI launch',async()=>{
 const r=await setup('legacy-real-codex');try{const a=await r.lifecycle.acquire(r.request,new AbortController().signal);const count=r.runs.length;
 for(const backend of ['codex','claude'])await assert.rejects(r.driver.launchOwnedCli(['--workspace','E2E-WS','--backend',backend,'task','owned'],{},'',false,new AbortController().signal));
 assert.equal(r.runs.length,count);assert.equal(r.driver.cliRegistration.env.LOOM_AGENT_MODEL,r.plan.model);assert.equal(r.driver.cliRegistration.env.LOOM_OPENCODE_MODEL,r.plan.model);
 assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
 }finally{await r.cleanup();}
});

test('host product registration binds detached generation after parent exit and cleanup failure retains authority',async()=>{
 const r=await setup('legacy-deterministic',true);try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),parent=r.driver.processesById.get('serve')!;
 await parent.stop();const before=await r.lifecycle.observe(a.lease.id,r.request.runId);
 assert.ok(before.services.some(row=>row.id==='registered-fleet-db'&&row.generation==='actual-kernel-start'&&row.state==='running'));
 r.failRegisteredCleanup(true);const failed=await r.lifecycle.release(a.lease.id,r.request.runId);assert.equal(failed.released,false);
 assert.ok(failed.remainingOwnedResources.includes('registered-fleet-db'));assert.ok((await fs.lstat(path.join(r.driver.runtimeRoot,'runtime'))).isDirectory());
 r.failRegisteredCleanup(false);assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,true);assert.deepEqual(r.registeredStops,['fleet']);
 }finally{await r.cleanup();}
});
test('changed product registration blocks all cleanup without adopting the new PID',async()=>{
 const r=await setup('legacy-deterministic',true);try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),before=r.stopped.length;
 const file=path.join(r.driver.configurationRoot,'fleet-db/runtime.json'),original=await fs.readFile(file);
 await fs.writeFile(file,JSON.stringify({pid:1000,url:'http://127.0.0.1:6002'}));
 const failed=await r.lifecycle.release(a.lease.id,r.request.runId);assert.equal(failed.released,false);assert.equal(r.stopped.length,before);assert.deepEqual(r.registeredStops,[]);
 await fs.writeFile(file,original);assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,true);
 }finally{await r.cleanup();}
});
test('partial host acquisition enrolls registered child before cleanup snapshot and retains it after failure',async()=>{
 const r=await setup('legacy-deterministic',true);try{
 r.failService('frontend');r.failRegisteredCleanup(true);let lease='';
 await assert.rejects(r.lifecycle.acquire(r.request,new AbortController().signal),error=>{
  assert.ok(error instanceof FixtureError);lease=error.leaseId!;assert.ok(error.remainingOwnedResources.includes('registered-fleet-db'));return true;
 });
 assert.equal(r.handles.get('serve')!.state(),'running');r.failRegisteredCleanup(false);
 assert.equal((await r.lifecycle.release(lease,r.request.runId)).released,true);assert.deepEqual(r.registeredStops,['fleet']);
 }finally{await r.cleanup();}
});

test('host graceful native actor preserves exact registration and never force-kills or respawns',async()=>{
 const r=await setup('legacy-deterministic',true,true);try{
 const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
 const before=r.starts.length;
 await assert.rejects(r.driver.terminateRegisteredNativeService('registered-fleet-db','actual-kernel-start',signal));
 await assert.rejects(r.driver.terminateRegisteredNativeService('registered-opencode-service','foreign',signal));assert.equal(r.registeredStops.length,0);
 assert.deepEqual(await r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal),
  {beforeGeneration:'actual-native-start',afterGeneration:null,affectedIds:['registered-opencode-service'],complete:true});
 assert.equal(r.starts.length,before);assert.deepEqual(r.registeredStops,['opencode-term']);
 await assert.rejects(r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal));
 assert.equal((await r.lifecycle.release(a.lease.id,r.request.runId)).released,true);assert.equal(r.registeredStops.includes('opencode-force'),false);
 }finally{await r.cleanup();}
});

test('product successor keeps the exited predecessor and a separately captured owned parent generation',async()=>{
 const r=await setup('legacy-deterministic',true,true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),starts=r.starts.length;
  await r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal);
  await fs.writeFile(path.join(r.driver.configurationRoot,'agents-opencode/state/opencode/service.json'),
    JSON.stringify({id:'actual-successor-registration',pid:1002,url:'http://127.0.0.1:7002',password:'private-successor-password'}));
  const observed=await r.lifecycle.observe(a.lease.id,'test-run');
  const predecessor=observed.services.find(row=>row.id==='registered-opencode-service')!,successor=observed.services.find(row=>row.generation==='actual-native-successor')!;
  assert.equal(predecessor.state,'exited');assert.ok(successor.id.startsWith('registered-opencode-service:successor-'));assert.equal(successor.state,'running');
  assert.ok(observed.services.some(row=>row.generation===`actual-parent-${r.handles.get('serve')!.pid}`));
  assert.equal(r.starts.length,starts);assert.deepEqual(r.registeredStops,['opencode-term']);
  await assert.rejects(r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal));
  r.failRegisteredCleanup(true);const failed=await r.lifecycle.release(a.lease.id,'test-run');assert.equal(failed.released,false);
  assert.ok(failed.remainingOwnedResources.includes(successor.id));assert.ok(failed.remainingOwnedResources.includes(predecessor.id));
  r.failRegisteredCleanup(false);assert.equal((await r.lifecycle.release(a.lease.id,'test-run')).released,true);
  assert.equal(r.registeredStops.filter(value=>value==='opencode-force').length,1);
  for(const filename of await fs.readdir(path.join(r.driver.runtimeRoot,'evidence'))){const text=await fs.readFile(path.join(r.driver.runtimeRoot,'evidence',filename),'utf8');assert.equal(text.includes('private-successor-password'),false);}
 }finally{await r.cleanup();}
});

test('unrequested, foreign-command, foreign-parent and reparented service replacements cannot grant cleanup authority',async()=>{
 for(const change of ['unrequested','argv','parent','reparented'] as const){const r=await setup('legacy-deterministic',true,true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal),stopped=r.stopped.length;
  if(change!=='unrequested')await r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal);
  if(change==='argv')r.registration(1002,{argvSha256:'f'.repeat(64)});
  if(change==='parent')r.registration(1002,{parentPid:9876});
  if(change==='reparented')r.onCapture(async pid=>{if(pid===1002)await r.handles.get('serve')!.stop();});
  const file=path.join(r.driver.configurationRoot,'agents-opencode/state/opencode/service.json');
  await fs.writeFile(file,JSON.stringify({pid:1002,url:'http://127.0.0.1:7002',password:'never-captured'}));
  await assert.rejects(r.lifecycle.observe(a.lease.id,'test-run'));
  if(change==='unrequested')assert.equal(r.captures.includes(1002),false);
  if(change==='argv'||change==='parent')assert.deepEqual(r.abandoned,[1002]);
  assert.equal(r.registeredStops.includes('opencode-force'),false);
  const failed=await r.lifecycle.release(a.lease.id,'test-run');assert.equal(failed.released,false);
  assert.equal(r.registeredStops.includes('opencode-force'),false);
  assert.equal(r.stopped.length,stopped+(change==='reparented'?1:0));
 }finally{await r.cleanup();}}
});

test('successor capture rejects changed parent kernel generation and serializes cleanup and parent restart',async()=>{
 const r=await setup('legacy-deterministic',true,true);try{
  const signal=new AbortController().signal,a=await r.lifecycle.acquire(r.request,signal);
  await r.driver.terminateRegisteredNativeService('registered-opencode-service','actual-native-start',signal);
  const parent=r.handles.get('serve')!;let resumed!:()=>void,entered!:()=>void;
  const waiting=new Promise<void>(resolve=>resumed=resolve),capturing=new Promise<void>(resolve=>entered=resolve);
  r.onCapture(async pid=>{if(pid===1002){entered();await waiting;}});
  await fs.writeFile(path.join(r.driver.configurationRoot,'agents-opencode/state/opencode/service.json'),JSON.stringify({pid:1002,url:'http://127.0.0.1:7002'}));
  const observing=r.lifecycle.observe(a.lease.id,'test-run');await capturing;
  await assert.rejects(r.driver.restartOwnedProcess('serve',parent.generation,signal));
  await assert.rejects(r.driver.stopOwnedProcess('serve',parent.generation,signal));
  await assert.rejects(r.lifecycle.release(a.lease.id,'test-run'));await assert.rejects(r.driver.prepareCleanup(signal));assert.equal(parent.state(),'running');
  r.registration(parent.pid,{generation:'reused-parent-kernel-generation'});resumed();await assert.rejects(observing);
  assert.equal(r.registeredStops.includes('opencode-force'),false);
 }finally{await r.cleanup();}
});

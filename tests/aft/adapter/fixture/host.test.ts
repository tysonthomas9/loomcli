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

const hash = (value: string | Uint8Array) => createHash('sha256').update(value).digest('hex');
async function setup(profile: string) {
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
    connection: 'test', connectionFingerprint: 'c'.repeat(64), minimumFreeBytes: 1, attestedImages: false,
    loomBinary: path.join(build, 'loom'), fleetBinary: path.join(build, 'fleet'), nodeBinary: path.join(build, 'node'), gitBinary: path.join(build, 'git'),
    pinnedOpenCodeBinary: path.join(build, 'opencode'), realBinaries: Object.fromEntries(['codex', 'claude', 'cursor', 'opencode'].map(name => [name,
      { executable: realBinary, sha256: hash('real-binary'), authRoot }])), daemon: false, fakeGitHub: false, maxBudgetUsd: '5.00',
  };
  const plan: FixturePlan = { profile, loomRevision: revision, fleetRevision: revision, engineRevision: revision, adapterRevision: revision,
    model: profile === 'legacy-deterministic' ? 'aft/m' : profile === 'legacy-real-cursor' ? 'backend-default' : 'openai/real-model', maxCases: 10, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 10000 };
  const starts: { id: string; command: HostCommand; readiness: string }[] = []; const runs: HostCommand[] = []; const stopped: string[] = [];
  let failService = ''; let failStop = ''; let spawnFails = false; let port = 4100; let count = 0; let failHttp = false;
  const handles = new Map<string, OwnedProcess>();
  const processes: HostProcesses = {
    async run(command) {
      runs.push(command);
      if (command.argv[0] === 'rev-parse') return command.argv[1] === 'HEAD' ? revision.commit : revision.tree;
      if (command.argv[0] === 'ls-files') return sourceEntries.map(entry => entry.relativePath).join('\0') + '\0';
      return '';
    },
    launch(command, stdin, generation) {
      if(spawnFails) throw new LaunchNotStarted();
      runs.push(command); let alive = true;
      const handle = { pid: ++count + 100, generation, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' as const : 'exited' as const, async ready() {}, async stop() { alive = false; stopped.push('cli'); },
        async completion() { alive = false; return { exitCode: 0, stdout: stdin || '{"owned":true}', stderr: '', complete: true }; } };
      handles.set('cli', handle); return handle;
    },
    start(command, readiness, generation) {
      const id = command.argv[0] === 'app-server' ? 'codex-preflight' : command.argv[0] === 'serve' ? 'serve' : command.argv.includes('preview') ? 'frontend' : command.argv[0]?.endsWith('/fake-model/server.mjs') ? 'fake-model' : 'daemon';
      if(spawnFails) throw new LaunchNotStarted();
      starts.push({ id, command, readiness }); let alive = true;
      const handle: OwnedProcess = { pid: ++count + 40, generation: generation ?? `generation-${count}`, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() { if (id === failService) throw new Error('Bearer private-ready-token'); },
        async stop() { if (id === failStop) throw new Error('secret=private-stop-token'); alive = false; stopped.push(id); } };
      handles.set(id, handle); return handle;
    },
  };
  let onHttp:((method:string,relative:string)=>Promise<void>)|undefined;
  const requests:{method:string;relative:string}[]=[];
  const http: Http = async (_origin, method, relative) => {
    requests.push({method,relative});await onHttp?.(method,relative);
    if (failHttp) return { status: 503, body: { message: 'Bearer private-http-token' } };
    if(relative==='/__requests')return {status:200,body:{requests:[],queued:0}};
    if(relative==='/__reset')return {status:200,body:{ok:true}};
    if (method === 'POST') return { status: 201, body: {} };
    return { status: 200, body: relative.endsWith('/E2E-WS') ? { data: { id: 'E2E-WS', repos: [{ path: driver.workspaceRoot }] } } : {} };
  };
  const protocols:string[]=[];
  const driver: HostFixtureDriver = new HostFixtureDriver(config, processes, fs, http, () => `fixture-${++count}`, async () => ({ port: port++, async release() {} }),async endpoint=>{protocols.push(endpoint);if(failService==='codex-protocol')throw new Error('private probe failure');});
  const lifecycle = new FixtureLifecycle([plan], () => driver, () => 1000, () => 'opaque-fixture');
  return { root, source, config, plan, starts, runs, stopped, handles, driver, lifecycle, protocols, requests,
    request: { runId: 'test-run', profile, loomRevision: revision, fleetRevision: revision, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 },
    onHttp(callback:(method:string,relative:string)=>Promise<void>){onHttp=callback;},
    failSpawn() { spawnFails = true; }, failService(value: string) { failService = value; }, failStop(value: string) { failStop = value; }, failHttp() { failHttp = true; },
    async cleanup() { await fs.rm(root, { recursive: true }); } };
}

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

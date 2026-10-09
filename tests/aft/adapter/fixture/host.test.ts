import assert from 'node:assert/strict';
import { test } from 'node:test';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import path from 'node:path';
import { HostFixtureDriver, legacyProfiles, type HostConfig, type Http } from './host.js';
import { FixtureLifecycle, FixtureError, type FixturePlan } from './lifecycle.js';
import { type HostProcesses, type HostCommand, type OwnedProcess } from './process.js';
import { type RegisteredBuild, verifyManifest } from './production.js';

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
  const config: HostConfig = {
    loom: registered, fleet: registered, engine: registered, adapter: registered,
    tempParent: root, lockParent: path.join(root, 'locks'), hostHome: path.join(root, 'home'), toolPath: '/attested/toolchain',
    connection: 'test', connectionFingerprint: 'c'.repeat(64), minimumFreeBytes: 1, attestedImages: false,
    loomBinary: path.join(build, 'loom'), fleetBinary: path.join(build, 'fleet'), nodeBinary: path.join(build, 'node'), gitBinary: path.join(build, 'git'),
    pinnedOpenCodeBinary: path.join(build, 'opencode'), realBinaries: Object.fromEntries(['codex', 'claude', 'cursor', 'opencode'].map(name => [name,
      { executable: realBinary, sha256: hash('real-binary'), authRoot }])), daemon: false, fakeGitHub: false, maxBudgetUsd: '5.00',
  };
  const plan: FixturePlan = { profile, loomRevision: revision, fleetRevision: revision, engineRevision: revision, adapterRevision: revision,
    model: profile === 'legacy-deterministic' ? 'aft/m' : 'openai/real-model', maxCases: 10, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 10000 };
  const starts: { id: string; command: HostCommand; readiness: string }[] = []; const runs: HostCommand[] = []; const stopped: string[] = [];
  let failService = ''; let failStop = ''; let port = 4100; let count = 0; let failHttp = false;
  const handles = new Map<string, OwnedProcess>();
  const processes: HostProcesses = {
    async run(command) {
      runs.push(command);
      if (command.argv[0] === 'rev-parse') return command.argv[1] === 'HEAD' ? revision.commit : revision.tree;
      if (command.argv[0] === 'ls-files') return sourceEntries.map(entry => entry.relativePath).join('\0') + '\0';
      return '';
    },
    launch(command, stdin, generation) {
      runs.push(command); let alive = true;
      const handle = { pid: ++count + 100, generation, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' as const : 'exited' as const, async ready() {}, async stop() { alive = false; stopped.push('cli'); },
        async completion() { alive = false; return { exitCode: 0, stdout: stdin || '{"owned":true}', stderr: '', complete: true }; } };
      handles.set('cli', handle); return handle;
    },
    start(command, readiness, generation) {
      const id = command.argv[0] === 'serve' ? 'serve' : command.argv.includes('preview') ? 'frontend' : command.argv[0]?.endsWith('/fake-model/server.mjs') ? 'fake-model' : 'daemon';
      starts.push({ id, command, readiness }); let alive = true;
      const handle: OwnedProcess = { pid: ++count + 40, generation: generation ?? `generation-${count}`, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() { if (id === failService) throw new Error('Bearer private-ready-token'); },
        async stop() { if (id === failStop) throw new Error('secret=private-stop-token'); alive = false; stopped.push(id); } };
      handles.set(id, handle); return handle;
    },
  };
  const http: Http = async (_origin, method, relative) => {
    if (failHttp) return { status: 503, body: { message: 'Bearer private-http-token' } };
    if (method === 'POST') return { status: 201, body: {} };
    return { status: 200, body: relative.endsWith('/E2E-WS') ? { data: { id: 'E2E-WS', repos: [{ path: driver.workspaceRoot }] } } : {} };
  };
  const driver: HostFixtureDriver = new HostFixtureDriver(config, processes, fs, http, () => `fixture-${++count}`, async () => ({ port: port++, async release() {} }));
  const lifecycle = new FixtureLifecycle([plan], () => driver, () => 1000, () => 'opaque-fixture');
  return { root, source, config, plan, starts, runs, stopped, handles, driver, lifecycle,
    request: { runId: 'test-run', profile, loomRevision: revision, fleetRevision: revision, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 },
    failService(value: string) { failService = value; }, failStop(value: string) { failStop = value; }, failHttp() { failHttp = true; },
    async cleanup() { await fs.rm(root, { recursive: true }); } };
}

for (const profile of legacyProfiles) test(`${profile}: production host driver uses owned paths and fixed profile startup`, async () => {
  const r = await setup(profile);
  try {
    const acquired = await r.lifecycle.acquire(r.request, new AbortController().signal);
    assert.equal(acquired.workspaceId, 'E2E-WS'); assert.equal(acquired.repo, r.driver.workspaceRoot);
    assert.equal(acquired.repo.startsWith(r.root + '/loom-aft-host-'), true);
    assert.deepEqual(r.starts.map(start => start.id), profile === 'legacy-deterministic' ? ['fake-model', 'serve', 'frontend'] : ['serve', 'frontend']);
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
    assert.deepEqual(r.stopped, [...r.starts.map(start => start.id)].reverse());
    assert.equal((await fs.readdir(path.join(r.driver.runtimeRoot, 'evidence'))).length, 3);
    await assert.rejects(fs.lstat(path.join(r.driver.runtimeRoot, 'runtime')), { code: 'ENOENT' });
  } finally { await r.cleanup(); }
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
    assert.deepEqual(r.stopped, ['serve', 'fake-model']);
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

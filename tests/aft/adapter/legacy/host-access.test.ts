import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin, getRegisteredResource } from '@tysonthomas9/aft/capabilities';
import { createFixtureProviders } from '../fixture/providers.js';
import { HostFixtureDriver, type HostConfig, type Http } from '../fixture/host.js';
import type { HostProcesses, HostCommand, OwnedProcess } from '../fixture/process.js';
import { LaunchNotStarted } from '../fixture/process.js';
import type { FixturePlan } from '../fixture/lifecycle.js';
import { createEvidenceStore } from '../evidence.js';
import { fixturesKey, type OwnedFixture } from '../ownership.js';
import { createLegacyProviders } from './providers.js';
import { productionLegacyAccess } from './host-access.js';
import { LegacyError } from './operations.js';
import { createFixtureOperationAuthority } from '../authority.js';
import { LegacyOperationEffects } from './providers.js';

const hash = (bytes: string) => createHash('sha256').update(bytes).digest('hex');
async function setup() {
  const root = await fs.realpath(await fs.mkdtemp(fileURLToPath(new URL('.seed-test-host-', import.meta.url))));
  const source = path.join(root, 'source'), build = path.join(root, 'build');
  await fs.mkdir(source); await fs.mkdir(build); await fs.mkdir(path.join(root, 'locks')); await fs.mkdir(path.join(root, 'home'));
  const sourceEntries: { relativePath: string; sha256: string }[] = [], buildEntries: typeof sourceEntries = [];
  for (const binary of ['loom', 'fleet', 'node', 'git', 'opencode']) {
    await fs.writeFile(path.join(build, binary), binary, { mode: 0o700 }); buildEntries.push({ relativePath: binary, sha256: hash(binary) });
  }
  const farm = path.join(source, 'e2e', 'stubs'); await fs.mkdir(farm, { recursive: true });
  for (const binary of ['codex', 'claude', 'cursor-agent', 'opencode', 'gemini', 'gh']) {
    await fs.writeFile(path.join(farm, binary), binary, { mode: 0o700 });
    sourceEntries.push({ relativePath: `e2e/stubs/${binary}`, sha256: hash(binary) });
  }
  const manifest = (entries: typeof sourceEntries) => hash([...entries].sort((a,b) => a.relativePath < b.relativePath ? -1 : 1).map(entry => `${entry.sha256}  ${entry.relativePath}\n`).join(''));
  const revision = { repository: 'injected-build', commit: 'a'.repeat(40), tree: 'b'.repeat(40),
    sourceManifestSha256: manifest(sourceEntries), buildManifestSha256: manifest(buildEntries) };
  const registered = { revision, source: { root: source, entries: sourceEntries }, build: { root: build, entries: buildEntries } };
  const config: HostConfig = { loom: registered, fleet: registered, engine: registered, adapter: registered,
    tempParent: root, lockParent: path.join(root, 'locks'), hostHome: path.join(root, 'home'), toolPath: '/injected/toolchain',
    connection: 'injected', connectionFingerprint: 'c'.repeat(64), minimumFreeBytes: 1, attestedImages: false,
    loomBinary: path.join(build, 'loom'), fleetBinary: path.join(build, 'fleet'), nodeBinary: path.join(build, 'node'), gitBinary: path.join(build, 'git'),
    pinnedOpenCodeBinary: path.join(build, 'opencode'), realBinaries: {}, daemon: false, fakeGitHub: false, maxBudgetUsd: '5.00' };
  const plan: FixturePlan = { profile: 'legacy-deterministic', loomRevision: revision, fleetRevision: revision, engineRevision: revision,
    adapterRevision: revision, model: 'aft/m', maxCases: 1, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 600000 };
  let count = 0, port = 4300; const launches: HostCommand[] = []; const stops: string[] = [];
  let badAgents = false, failLaunch: '' | 'proven' | 'uncertain' = '', badRoles = false, exitCode = 0, stderr = '';
  let restoredBytes: string | undefined;
  const processes: HostProcesses = {
    async run(command) {
      if (command.argv[0] === 'rev-parse') return command.argv[1] === 'HEAD' ? revision.commit : revision.tree;
      if (command.argv[0] === 'ls-files') return sourceEntries.map(entry => entry.relativePath).join('\0') + '\0';
      return '';
    },
    start(command, _readiness, generation) {
      let alive = true; const handle: OwnedProcess = { pid: ++count + 100, generation: generation!, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() {}, async stop() { alive = false; stops.push(command.argv[0]!); } };
      return handle;
    },
    launch(command, _stdin, generation) {
      launches.push(command); if (failLaunch === 'proven') throw new LaunchNotStarted();
      if (failLaunch === 'uncertain') throw new Error('Injected uncertain launch failure');
      let alive = true;
      return { pid: ++count + 100, generation, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() {}, async stop() { alive = false; stops.push('cli'); },
        async completion() { alive = false; return { exitCode, stderr, complete: true, stdout:
          command.argv[2] === 'role' ? badRoles ? 'not JSON' : '[{"name":"task"}]' : '{"agent":"worker","cost":0}' }; } };
    },
  };
  const http: Http = async (_origin, method, route) => {
    if (method === 'POST') return { status: 201, body: {} };
    if (route === '/api/workspaces/E2E-WS') return { status: 200, body: { success: true,
      data: { id: 'E2E-WS', path: driver.workspaceRoot, repos: [{ name: 'repo', path: driver.workspaceRoot }] } } };
    if (route.endsWith('/agents')) return { status: 200, body: { success: true, total: badAgents ? 2 : 1,
      data: [{ name: 'worker', workspace_key: 'E2E-WS', role_name: 'task', updated_at: '2026-10-09T00:00:00Z' }] } };
    if (route.endsWith('/issues?limit=1000')) return { status: 200, body: { success: true, data: [{ id: 'issue' }] } };
    return { status: 200, body: {} };
  };
  const files: typeof fs = { ...fs, async rm(filename, options) {
    if (filename === path.join(driver.runtimeRoot, 'runtime')) restoredBytes = await fs.readFile(path.join(driver.configurationRoot, 'agents-opencode/config/opencode/opencode.json'), 'utf8');
    await fs.rm(filename, options);
  } };
  const driver = new HostFixtureDriver(config, processes, files, http, () => `injected-${++count}`, async () => ({ port: port++, async release() {} }));
  const adapterRoot = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(adapterRoot, ['fixture/providers.ts'], 'fixture/providers.ts', 'createFixtureProviders');
  const legacyPin = calculateImplementationPin(adapterRoot, ['legacy/providers.ts','legacy/host-access.ts','legacy/cli-plan.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry();
  for (const provider of createFixtureProviders({ implementation: pin, implementationSha256: pin.sha256, plans: [plan], driver: () => driver,
    evidenceAfterFailure: () => createEvidenceStore(path.join(driver.runtimeRoot, 'evidence')),
    async bind(_driver, acquired, input, context) {
      const stat = await fs.lstat(driver.runtimeRoot);
      return { evidenceClass: 'deterministic', operationAuthority: createFixtureOperationAuthority({ leaseId: acquired.lease.id, runId: context.runId,
        suiteId: context.suiteId, scope: context.scope, caseId: context.caseId, profile: input.profile },
        Object.fromEntries(Object.entries(LegacyOperationEffects).map(([operation, effects]) => [operation, { evidenceClass: 'deterministic' as const,
          effects: operation === 'loom.runtime.stimulate' ? ['stop-owned-process','restart-owned-service'] : [...effects] }]))),
        roots: new Map([['runtime', { path: driver.runtimeRoot, device: stat.dev, inode: stat.ino }]]), secrets: [],
        evidenceStore: await createEvidenceStore(path.join(driver.runtimeRoot, 'evidence')), readApi: async () => { throw new Error('Unused'); },
        readFiles: async () => { throw new Error('Unused'); }, resolveAgent: async () => { throw new Error('No native-v1 registration'); } };
    } })) registry.register(provider);
  for (const provider of createLegacyProviders(legacyPin, legacyPin.sha256, (_context, fixture) => productionLegacyAccess(fixture))) registry.register(provider);
  const context = createCapabilityContext({ file: 'injected-host.yaml', line: 1 }, registry); Object.assign(context, { runId: 'binding-run' });
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  const acquired = await invoke('loom.fixture.acquire', { runId: 'binding-run', profile: plan.profile, loomRevision: revision,
    fleetRevision: revision, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 });
  assert.equal(acquired.availability, 'observed');
  const leaseId = (acquired.data as { lease: { id: string } }).lease.id;
  const fixture = getRegisteredResource(context, `${fixturesKey}:${leaseId}`, leaseId) as OwnedFixture;
  return { root, driver, fixture, leaseId, invoke, launches, stops,
    badAgents: () => { badAgents = true; }, badRoles: () => { badRoles = true; }, failLaunch: (kind: 'proven' | 'uncertain' = 'proven') => { failLaunch = kind; },
    processFailure: () => { exitCode = 17; stderr = 'exact process diagnostic'; }, restored: () => restoredBytes,
    async cleanup() { await fs.rm(root, { recursive: true, force: true }); } };
}

test('canonical acquisition binds actual host driver to role, usage, all backend argv and serve generations', async t => {
  const r = await setup(); t.after(r.cleanup);
  assert.equal((await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'show', name: 'task' })).availability, 'observed');
  assert.equal((await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS', agentId: 'worker' } })).availability, 'observed');
  for (const backend of ['codex','claude','cursor','opencode']) for (const mode of ['once','auto','daemon']) {
    const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend,
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: 'repo' });
    assert.equal(result.availability, 'observed');
    assert.equal((result.data as { complete: boolean }).complete, false);
    const command = r.launches.at(-1)!;
    assert.deepEqual(command.argv, ['--workspace','E2E-WS','--backend',backend,'task','worker',...(mode === 'once' ? [] : ['--auto']),...(mode === 'daemon' ? ['--daemon-mode'] : [])]);
    assert.equal(command.env.LOOM_ASSIGNED_TASK_ID, mode === 'daemon' ? 'issue' : '');
  }
  const before = r.driver.processesById.get('serve')!.generation;
  const restarted = await r.invoke('loom.runtime.stimulate', { leaseId: r.leaseId, targetId: 'serve', operation: 'serve-restart', expectedGeneration: before });
  assert.equal(restarted.availability, 'observed'); assert.notEqual(r.driver.processesById.get('serve')!.generation, before);
  const stale = await r.invoke('loom.runtime.stimulate', { leaseId: r.leaseId, targetId: 'serve', operation: 'serve-restart', expectedGeneration: before });
  assert.equal(stale.availability, 'error'); assert.equal(r.stops.filter(value => value === 'serve').length, 1);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('actual binding rejects incomplete product identities and unsafe plans before operation launch', async t => {
  const r = await setup(); t.after(r.cleanup); r.badAgents();
  const bad = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null });
  assert.equal(bad.availability, 'error'); assert.equal(r.launches.length, 0);
  const access = productionLegacyAccess(r.fixture);
  await assert.rejects(access.lease('foreign', new AbortController().signal), LegacyError);
  await assert.rejects(access.execute(r.leaseId, { binary: '/foreign', cwd: r.driver.workspaceRoot, argv: ['bash','-c','bad'], env: {}, stdin: '' }, new AbortController().signal), LegacyError);
  assert.equal(r.launches.length, 0);
});

test('configuration restores exact private bytes through lease cleanup after expiry', async t => {
  const r = await setup(); t.after(r.cleanup);
  const filename = path.join(r.driver.configurationRoot, 'agents-opencode/config/opencode/opencode.json');
  const original = await fs.readFile(filename, 'utf8');
  // Whitespace is an independent exact-byte oracle for restoration.
  const bytes = `  ${original}\n`; await fs.writeFile(filename, bytes);
  const configured = await r.invoke('loom.fixture.configure', { leaseId: r.leaseId, setting: 'provider-default', model: 'aft/m', harness: 'opencode' });
  assert.equal(configured.availability, 'observed'); assert.notEqual(await fs.readFile(filename, 'utf8'), bytes);
  assert.deepEqual((configured.data as { previous: unknown }).previous, { model: 'aft/m', harness: 'opencode' });
  r.fixture.expiresAtUtcMs = 0;
  const backup = `${filename}.owned-backup`; await fs.rename(filename, backup); await fs.symlink(backup, filename);
  await assert.rejects(r.fixture.dispose());
  assert.equal(await fs.readFile(backup, 'utf8') === bytes, false);
  await fs.unlink(filename); await fs.rename(backup, filename);
  await r.fixture.dispose();
  assert.equal(r.restored(), bytes);
  // Runtime removal follows exact restoration. No private bytes are exported.
  assert.equal(await fs.stat(r.driver.runtimeRoot).then(() => true), true);
  assert.equal(JSON.stringify(configured.data).includes('baseURL'), false);
});

test('missing process registrations and unobservable scripted state fail before mutation', async t => {
  const r = await setup(); t.after(r.cleanup);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  for (const target of ['fake-model','fake-github','scripted-backend','workspace:E2E-WS']) await assert.rejects(access.snapshot(r.leaseId, target, signal), LegacyError);
  await assert.rejects(access.validateSeedPath(r.leaseId, 'E2E-WS', 'worker', 'file', signal), LegacyError);
  await assert.rejects(access.stimulate(r.leaseId, { id: 'guessed', kind: 'harness', generation: 'guessed', workspaceId: null, agentName: null, sessionName: null }, 'harness-restart', null, signal), LegacyError);
  assert.equal(r.launches.length, 0); assert.equal(r.stops.length, 0);
});

test('concrete configuration rejects foreign origins and stopped fixture generations before writes', async t => {
  const r = await setup(); t.after(r.cleanup);
  const filename = path.join(r.driver.configurationRoot, 'agents-opencode/config/opencode/opencode.json');
  const original = await fs.readFile(filename, 'utf8'); const changed = JSON.parse(original);
  changed.provider.aft.options.baseURL = 'http://127.0.0.1:65534/v1';
  const foreign = JSON.stringify(changed); await fs.writeFile(filename, foreign);
  const input = { leaseId: r.leaseId, setting: 'provider-default', model: 'aft/m', harness: 'opencode' };
  assert.equal((await r.invoke('loom.fixture.configure', input)).availability, 'error');
  assert.equal(await fs.readFile(filename, 'utf8'), foreign);
  await fs.writeFile(filename, original);
  const handle = r.driver.processesById.get('fake-model')!;
  await r.driver.stopOwnedProcess('fake-model', handle.generation, new AbortController().signal);
  assert.equal((await r.invoke('loom.fixture.configure', input)).availability, 'error');
  assert.equal(await fs.readFile(filename, 'utf8'), original);
});

test('actual driver enrollment retains stderr and retires only proven no-start failures', async t => {
  const r = await setup(); t.after(r.cleanup);
  const signal = new AbortController().signal;
  r.processFailure();
  const result = await r.driver.launchOwnedCli(['usage','--format','json','--agent','worker'], { LOOM_WORKSPACE_ID: 'E2E-WS' }, '', true, signal);
  assert.equal(result.completion.exitCode, 17); assert.equal(result.completion.stderr, 'exact process diagnostic');
  r.failLaunch();
  await assert.rejects(r.driver.launchOwnedCli(['usage','--format','json','--agent','worker'], { LOOM_WORKSPACE_ID: 'E2E-WS' }, '', true, signal));
  const released = await r.invoke('loom.fixture.release', { leaseId: r.leaseId });
  assert.equal(released.availability, 'observed'); assert.equal((released.data as { released: boolean }).released, true);
});

test('generation is compared again at the actual restart effect', async t => {
  const r = await setup(); t.after(r.cleanup);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  const lease = await access.lease(r.leaseId, signal, 'loom.runtime.stimulate'); const target = lease.processes[0]!;
  await r.driver.restartOwnedProcess('serve', target.generation, signal);
  await assert.rejects(access.stimulate(r.leaseId, target, 'serve-restart', null, signal));
  assert.equal(r.stops.filter(value => value === 'serve').length, 1);
});

test('uncertain no-handle launch failure keeps the exact resource ledger for retry', async t => {
  const r = await setup(); t.after(r.cleanup); r.failLaunch('uncertain');
  await assert.rejects(r.driver.launchOwnedCli(['usage','--format','json','--agent','worker'], { LOOM_WORKSPACE_ID: 'E2E-WS' }, '', true, new AbortController().signal));
  const released = await r.invoke('loom.fixture.release', { leaseId: r.leaseId });
  assert.equal(released.availability, 'observed');
  const data = released.data as { released: boolean; remainingOwnedResources: string[] };
  assert.equal(data.released, false); assert.ok(data.remainingOwnedResources.some(id => id.startsWith('cli-')));
});

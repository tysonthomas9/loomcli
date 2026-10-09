import { test, type TestContext } from 'node:test';
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
import { testLegacyRoster } from './test-roster.js';
import { materializeRenderer } from '../fixture/renderer-fixtures.test.js';

const hash = (bytes: string) => createHash('sha256').update(bytes).digest('hex');
async function setup(t: TestContext, options: { liveClaude?: boolean; invalidStartup?: boolean; fakeGitHub?: boolean; secondWorkspace?: boolean; onRoot?: (root: string) => void } = {}) {
  const created = await fs.mkdtemp(fileURLToPath(new URL('.seed-test-host-', import.meta.url)));
  t.after(async () => { await fs.rm(created, { recursive: true, force: true }); });
  const root = await fs.realpath(created); options.onRoot?.(root);
  const source = path.join(root, 'source'), build = path.join(root, 'build');
  await fs.mkdir(source); await fs.mkdir(build); await fs.mkdir(path.join(root, 'locks')); await fs.mkdir(path.join(root, 'home'));
  const sourceEntries: { relativePath: string; sha256: string }[] = [], buildEntries: typeof sourceEntries = [];
  for (const binary of ['loom', 'fleet', 'node', 'git', 'opencode', ...(options.liveClaude ? ['claude'] : [])]) {
    await fs.writeFile(path.join(build, binary), binary, { mode: 0o700 }); buildEntries.push({ relativePath: binary, sha256: hash(binary) });
  }
  const farm = path.join(source, 'e2e', options.liveClaude ? 'stubs-real-claude' : 'stubs'); await fs.mkdir(farm, { recursive: true });
  for (const binary of ['codex', 'claude', 'cursor-agent', 'opencode', 'gemini', 'gh']) {
    await fs.writeFile(path.join(farm, binary), binary, { mode: 0o700 });
    sourceEntries.push({ relativePath: `e2e/${path.basename(farm)}/${binary}`, sha256: hash(binary) });
  }
  const manifest = (entries: typeof sourceEntries) => hash([...entries].sort((a,b) => a.relativePath < b.relativePath ? -1 : 1).map(entry => `${entry.sha256}  ${entry.relativePath}\n`).join(''));
  const revision = { repository: 'injected-build', commit: 'a'.repeat(40), tree: 'b'.repeat(40),
    sourceManifestSha256: manifest(sourceEntries), buildManifestSha256: manifest(buildEntries) };
  const registered = { revision, source: { root: source, entries: sourceEntries }, build: { root: build, entries: buildEntries } };
  // The shared injected receipt producer exercises source/build preflight; it
  // does not stand in for a compiled product renderer or runtime acceptance.
  await materializeRenderer(registered);
  // This file is an injected preflight placeholder, never provider auth. No
  // credentials are read/copied, and all process execution remains a double.
  if (options.liveClaude) await fs.writeFile(path.join(root, 'home', '.credentials.json'), '{}');
  const config: HostConfig = { loom: registered, fleet: registered, engine: registered, adapter: registered,
    tempParent: root, lockParent: path.join(root, 'locks'), hostHome: path.join(root, 'home'), toolPath: '/injected/toolchain',
    connection: 'injected', connectionFingerprint: 'c'.repeat(64), minimumFreeBytes: 1, attestedImages: false,
    loomBinary: path.join(build, 'loom'), fleetBinary: path.join(build, 'fleet'), nodeBinary: path.join(build, 'node'), gitBinary: path.join(build, 'git'),
    pinnedOpenCodeBinary: path.join(build, 'opencode'), realBinaries: options.liveClaude ? {
      claude: { executable: path.join(build, 'claude'), sha256: hash('claude'), authRoot: path.join(root, 'home') } } : {},
    daemon: false, fakeGitHub: options.fakeGitHub ?? false, maxBudgetUsd: '5.00' };
  const plan: FixturePlan = { profile: options.liveClaude ? 'legacy-real-claude' : 'legacy-deterministic', loomRevision: revision, fleetRevision: revision, engineRevision: revision,
    adapterRevision: revision, model: options.liveClaude ? 'configured-model' : 'aft/m', maxCases: 1, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 600000,
    ...(options.liveClaude ? { liveProvider: { backend: 'claude', model: 'configured-model' } as const } : {}) };
  let count = 0, port = 4300; const launches: HostCommand[] = []; const stops: string[] = [];
  let badAgents = false, failLaunch: '' | 'proven' | 'uncertain' = '', badRoles = false, exitCode = 0, stderr = '';
  let restoredBytes: string | undefined;
  let queued = 0, malformedReset = false;
  const fixtureRequests: { method: string; route: string; body: unknown }[] = [];
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
          command.argv[2] === 'role' ? badRoles ? 'not JSON' : '[{"name":"task","model":"unused-role-model"}]' : '{"agent":"worker","cost":0}' }; } };
    },
  };
  const http: Http = async (_origin, method, route, body) => {
    if (route.startsWith('/__')) {
      fixtureRequests.push({ method, route, body });
      const github = _origin.endsWith(':4304');
      if (route === '/__requests') return { status: 200, body: github ? [] : options.invalidStartup ? { requests: [] } : { requests: [], queued } };
      if (route === '/__reset') { queued = 0; return { status: 200, body: malformedReset ? {} : { ok: true } }; }
      if (route === '/__script') { queued += (body as { steps: unknown[] }).steps.length; return { status: 200, body: { queued } }; }
      if (route === '/__fixture') { const value = body as { pr: unknown; files: unknown[] }; return { status: 200, body: { ok: true, pr: value.pr, files: value.files.length } }; }
    }
    if (method === 'POST') return { status: 201, body: {} };
    const workspaceId = route.split('/')[3];
    const repo = workspaceId === 'E2E-WS' ? driver.workspaceRoot : path.join(driver.runtimeRoot, 'runtime/e2e-workspace-2');
    if (route === `/api/workspaces/${workspaceId}`) return { status: 200, body: { success: true,
      data: { id: workspaceId, path: repo, repos: [{ name: 'repo', path: repo }] } } };
    if (route.endsWith('/agents')) return { status: 200, body: { success: true, total: badAgents ? 2 : 1,
      data: [{ name: 'worker', workspace_key: workspaceId, role_name: 'task', updated_at: '2026-10-09T00:00:00Z' }] } };
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
  const registry = new CapabilityRegistry(); let factories = 0;
  for (const provider of createFixtureProviders({ implementation: pin, implementationSha256: pin.sha256, plans: [plan], driver: () => driver,
    evidenceAfterFailure: () => createEvidenceStore(path.join(driver.runtimeRoot, 'evidence')),
    async bind(_driver, acquired, input, context) {
      const stat = await fs.lstat(driver.runtimeRoot);
      const evidenceClass = options.liveClaude ? 'real-native' as const : 'deterministic' as const;
      return { evidenceClass, operationAuthority: createFixtureOperationAuthority({ leaseId: acquired.lease.id, runId: context.runId,
        suiteId: context.suiteId, scope: context.scope, caseId: context.caseId, profile: input.profile },
        Object.fromEntries(Object.entries(LegacyOperationEffects).map(([operation, effects]) => [operation, {
          evidenceClass: options.liveClaude && operation === 'loom.cli.task' ? 'live-provider' as const : evidenceClass,
          effects: operation === 'loom.runtime.stimulate' ? [...effects, 'restart-owned-service'] :
            options.liveClaude && operation === 'loom.cli.task' ? [...effects, 'external-provider'] : [...effects] }]))),
        roots: new Map([['runtime', { path: driver.runtimeRoot, device: stat.dev, inode: stat.ino }]]), secrets: [],
        evidenceStore: await createEvidenceStore(path.join(driver.runtimeRoot, 'evidence')), readApi: async () => { throw new Error('Unused'); },
        readFiles: async () => { throw new Error('Unused'); }, resolveAgent: async () => { throw new Error('No native-v1 registration'); } };
    } })) registry.register(provider);
  for (const provider of createLegacyProviders(legacyPin, legacyPin.sha256, (_context, fixture) => { factories++; return productionLegacyAccess(fixture); },
    { taskExecution: options.liveClaude ? 'live-provider' : 'deterministic' })) registry.register(provider);
  const context = createCapabilityContext({ file: 'injected-host.yaml', line: 1 }, registry); Object.assign(context, { runId: 'binding-run' });
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  const acquired = await invoke('loom.fixture.acquire', { runId: 'binding-run', profile: plan.profile, loomRevision: revision,
    fleetRevision: revision, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 });
  if (acquired.availability !== 'observed') {
    const directory = fileURLToPath(new URL('.verification/', import.meta.url)); await fs.mkdir(directory, { recursive: true });
    await fs.writeFile(path.join(directory, `acquire-failure-${path.basename(root)}.json`),
      JSON.stringify({ availability: acquired.availability, error: acquired.error, evidenceClass: 'deterministic injected setup' }) + '\n', { flag: 'wx', mode: 0o600 });
  }
  assert.equal(acquired.availability, 'observed', JSON.stringify(acquired.error));
  const leaseId = (acquired.data as { lease: { id: string } }).lease.id;
  const fixture = getRegisteredResource(context, `${fixturesKey}:${leaseId}`, leaseId) as OwnedFixture;
  fixture.readWorkspaceLegacyAgent = async () => { throw new Error('Injected member needs no enrollment lookup'); };
  fixture.ownedWorkspaces = await testLegacyRoster(fixture, await createEvidenceStore(path.join(driver.runtimeRoot, 'evidence')),
    [{ workspaceId: fixture.workspaceId, repo: fixture.repo, agentIds: ['worker'] }, ...(options.secondWorkspace ?
      [{ workspaceId: 'E2E-WS-2', repo: path.join(driver.runtimeRoot, 'runtime/e2e-workspace-2'), agentIds: ['worker'] }] : [])]);
  return { root, driver, fixture, leaseId, invoke, launches, stops, factories: () => factories,
    fixtureRequests, queued: () => queued, malformedReset: (value: boolean) => { malformedReset = value; },
    badAgents: () => { badAgents = true; }, badRoles: () => { badRoles = true; }, failLaunch: (kind: 'proven' | 'uncertain' = 'proven') => { failLaunch = kind; },
    processFailure: () => { exitCode = 17; stderr = 'exact process diagnostic'; }, restored: () => restoredBytes,
    async cleanup() { await fs.rm(root, { recursive: true, force: true }); } };
}

test('canonical acquisition binds actual host driver to role, usage, all backend argv and serve generations', async t => {
  const r = await setup(t);
  const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'show', name: 'task' });
  assert.equal(role.availability, 'observed', JSON.stringify(role.error));
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
  const r = await setup(t); r.badAgents();
  const bad = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null });
  assert.equal(bad.availability, 'error'); assert.equal(r.launches.length, 0);
  const access = productionLegacyAccess(r.fixture);
  await assert.rejects(access.lease('foreign', new AbortController().signal), LegacyError);
  await assert.rejects(access.execute(r.leaseId, { binary: '/foreign', cwd: r.driver.workspaceRoot, argv: ['bash','-c','bad'], env: {}, stdin: '' }, new AbortController().signal), LegacyError);
  assert.equal(r.launches.length, 0);
});

test('configuration restores exact private bytes through lease cleanup after expiry', async t => {
  const r = await setup(t);
  const filename = path.join(r.driver.configurationRoot, 'agents-opencode/config/opencode/opencode.json');
  const original = await fs.readFile(filename, 'utf8');
  // Whitespace is an independent exact-byte oracle for restoration.
  const bytes = `  ${original}\n`; await fs.writeFile(filename, bytes);
  const configured = await r.invoke('loom.fixture.configure', { leaseId: r.leaseId, setting: 'provider-default', model: 'aft/m', harness: 'opencode' });
  assert.equal(configured.availability, 'observed'); assert.notEqual(await fs.readFile(filename, 'utf8'), bytes);
  assert.equal(r.launches.length, 0, 'Configuration has no role discovery CLI effect');
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
  const r = await setup(t);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  for (const target of ['scripted-backend','workspace:E2E-WS']) await assert.rejects(access.snapshot(r.leaseId, target, signal), LegacyError);
  await assert.rejects(access.snapshot(r.leaseId, 'fake-github', signal));
  await assert.rejects(access.validateSeedPath(r.leaseId, 'E2E-WS', 'worker', 'file', signal), LegacyError);
  await assert.rejects(access.stimulate(r.leaseId, { id: 'guessed', kind: 'harness', generation: 'guessed', workspaceId: null, agentName: null, sessionName: null }, 'harness-restart', null, signal), LegacyError);
  assert.equal(r.launches.length, 0); assert.equal(r.stops.length, 0);
});

test('concrete configuration rejects foreign origins and stopped fixture generations before writes', async t => {
  const r = await setup(t);
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
  const r = await setup(t);
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
  const r = await setup(t);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  const lease = await access.lease(r.leaseId, signal, 'loom.runtime.stimulate'); const target = lease.processes[0]!;
  await r.driver.restartOwnedProcess('serve', target.generation, signal);
  await assert.rejects(access.stimulate(r.leaseId, target, 'serve-restart', null, signal));
  assert.equal(r.stops.filter(value => value === 'serve').length, 1);
});

test('uncertain no-handle launch failure keeps the exact resource ledger for retry', async t => {
  const r = await setup(t); r.failLaunch('uncertain');
  await assert.rejects(r.driver.launchOwnedCli(['usage','--format','json','--agent','worker'], { LOOM_WORKSPACE_ID: 'E2E-WS' }, '', true, new AbortController().signal));
  const released = await r.invoke('loom.fixture.release', { leaseId: r.leaseId });
  assert.equal(released.availability, 'observed');
  const data = released.data as { released: boolean; remainingOwnedResources: string[] };
  assert.equal(data.released, false); assert.ok(data.remainingOwnedResources.some(id => id.startsWith('cli-')));
});

test('canonical registry denies missing process effect before actual host factory and discovery', async t => {
  const r = await setup(t);
  r.fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: r.fixture.leaseId, runId: r.fixture.runId,
    suiteId: r.fixture.suiteId, scope: r.fixture.scope, caseId: r.fixture.caseId, profile: r.fixture.profile },
    { 'loom.cli.role': { evidenceClass: 'deterministic', effects: ['read-api','read-filesystem'] } });
  const result = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'list', name: null });
  assert.equal(result.availability, 'unsupported'); assert.equal(r.factories(), 0); assert.equal(r.launches.length, 0);
});

test('real execution grant cannot reuse the deterministic task descriptor or start host discovery', async t => {
  const r = await setup(t);
  r.fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: r.fixture.leaseId, runId: r.fixture.runId,
    suiteId: r.fixture.suiteId, scope: r.fixture.scope, caseId: r.fixture.caseId, profile: r.fixture.profile },
    { 'loom.cli.task': { evidenceClass: 'live-provider', effects: [...LegacyOperationEffects['loom.cli.task'],'external-provider'] } });
  const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null });
  assert.equal(result.availability, 'error'); assert.equal(r.factories(), 0); assert.equal(r.launches.length, 0);
});

test('every bound action rejects a missing required effect before its host factory or transport', async t => {
  const cases = [
    { id: 'loom.cli.role', remove: 'start-owned-process', input: { workspaceId: 'E2E-WS', operation: 'list', name: null } },
    { id: 'loom.cli.usage', remove: 'start-owned-process', input: { agent: { workspaceId: 'E2E-WS', agentId: 'worker' } } },
    { id: 'loom.cli.task', remove: 'start-owned-process', input: { workspaceId: 'E2E-WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null } },
    { id: 'loom.runtime.stimulate', remove: 'stop-owned-process', input: { targetId: 'serve', operation: 'serve-restart', expectedGeneration: 'unused' } },
    { id: 'loom.fixture.seedWorktree', remove: 'write-fixture', input: { workspaceId: 'E2E-WS', agentName: 'worker', relativePath: 'proof.txt', content: 'fixture', commitMessage: 'fixture only' } },
    { id: 'loom.fixture.configure', remove: 'write-fixture', input: { setting: 'provider-default', model: 'aft/m', harness: 'opencode' } },
  ] as const;
  for (const row of cases) {
    const r = await setup(t);
    r.fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: r.fixture.leaseId, runId: r.fixture.runId,
      suiteId: r.fixture.suiteId, scope: r.fixture.scope, caseId: r.fixture.caseId, profile: r.fixture.profile },
      { [row.id]: { evidenceClass: 'deterministic', effects: LegacyOperationEffects[row.id].filter(effect => effect !== row.remove) } });
    const input = row.id === 'loom.cli.usage' ? { agent: { ...row.input.agent, fixtureLeaseId: r.leaseId } } : { ...row.input, leaseId: r.leaseId };
    const result = await r.invoke(row.id, input);
    assert.equal(result.availability, 'unsupported', row.id); assert.equal(r.factories(), 0, row.id); assert.equal(r.launches.length, 0, row.id);
  }
});

test('actual host binding refuses backend or model environment drift before task launch', async t => {
  for (const variant of ['backend','model'] as const) {
    const r = await setup(t);
    const route = r.driver.executionRouting;
    Object.defineProperty(r.driver, 'executionRouting', { get: () => ({ ...route,
      ...(variant === 'backend' ? { allowedTaskBackends: ['opencode'] } : { model: 'foreign-model' }) }) });
    const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null });
    assert.equal(result.availability, 'error'); assert.equal(r.launches.length, 0, variant);
  }
});

test('direct once, auto and daemon CLI actors keep matching effective env despite unused role model', async t => {
  const r = await setup(t, { liveClaude: true });
  for (const mode of ['once', 'auto', 'daemon']) {
    const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'claude',
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: 'repo' });
    assert.equal(result.availability, 'observed', JSON.stringify(result));
    const command = r.launches.at(-1)!;
    assert.equal(command.env.LOOM_AGENT_MODEL, 'configured-model');
    assert.deepEqual(command.argv, ['--workspace','E2E-WS','--backend','claude','task','worker',
      ...(mode === 'once' ? [] : ['--auto']), ...(mode === 'daemon' ? ['--daemon-mode'] : [])]);
  }
});

test('an override actually applied to the host environment rejects before the task process launch', async t => {
  const r = await setup(t, { liveClaude: true });
  const inherited = r.driver.cliRegistration.env;
  // Inject the *effective* environment at the same HostFixtureDriver seam
  // used by both cliRegistration and launchOwnedCli. This is distinct from
  // unused role metadata; no supervisor actor or provider is executed here.
  Object.defineProperty(r.driver, 'env', { value: () => ({ ...inherited, LOOM_AGENT_MODEL: 'applied-override' }) });
  for (const mode of ['once', 'auto', 'daemon']) {
    const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend: 'claude',
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: 'repo' });
    assert.equal(result.availability, 'error');
  }
  assert.equal(r.launches.filter(command => command.argv[4] === 'task').length, 0);
});

test('failed canonical acquisition removes the exact test root after retaining diagnostics', async t => {
  let root = '';
  await t.test('missing queued count fails startup', async child => {
    await assert.rejects(setup(child, { invalidStartup: true, onRoot: value => { root = value; } }));
  });
  await assert.rejects(fs.stat(root), { code: 'ENOENT' });
  const receipt = JSON.parse(await fs.readFile(fileURLToPath(new URL(`.verification/acquire-failure-${path.basename(root)}.json`, import.meta.url)), 'utf8'));
  assert.equal(receipt.availability, 'error');
});

test('canonical model script binding retains startup restoration and cleans up after expiry', async t => {
  const r = await setup(t);
  const input = { leaseId: r.leaseId, setting: 'fake-model-scenario', fixtureId: 'fake-model',
    scenarioId: 'fake-model-e31296792340e32b', parameters: {}, agentId: null };
  const configured = await r.invoke('loom.fixture.configure', input);
  assert.equal(configured.availability, 'observed', JSON.stringify(configured));
  assert.equal(r.queued(), 1); assert.equal(r.launches.length, 0);
  const generation = r.driver.processesById.get('fake-model')!.generation;
  assert.deepEqual((configured.data as { previous: unknown }).previous,
    { restorationPoint: { kind: 'startup-empty' }, generation });
  assert.deepEqual(r.fixtureRequests.find(row => row.route === '/__script')?.body,
    { steps: [{ text: 'AGV1-KIDS-A2' }] });
  r.fixture.expiresAtUtcMs = 0;
  await r.fixture.dispose(); assert.equal(r.queued(), 0);
  assert.equal(r.fixtureRequests.filter(row => row.route === '/__reset').length, 1);
});

test('baseline binding rejects foreign state, unknown targets, and stopped generations before POST', async t => {
  const r = await setup(t);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  await access.lease(r.leaseId, signal, 'loom.fixture.configure');
  const snapshot = await access.snapshot(r.leaseId, 'fake-model', signal);
  await assert.rejects(access.restore(r.leaseId, 'fake-model', { target: 'fake-model', kind: 'startup-empty', generation: 'foreign' }, signal));
  await assert.rejects(access.request(r.leaseId, 'fake-model', 'POST', '/__arbitrary', {}, signal));
  await assert.rejects(access.request(r.leaseId, 'fake-github', 'POST', '/__fixture', {}, signal));
  const handle = r.driver.processesById.get('fake-model')!;
  await r.driver.stopOwnedProcess('fake-model', handle.generation, signal);
  await assert.rejects(access.request(r.leaseId, 'fake-model', 'POST', '/__script', { steps: [] }, signal));
  await assert.rejects(access.restore(r.leaseId, 'fake-model', snapshot.restoreState, signal));
  assert.equal(r.fixtureRequests.filter(row => row.method === 'POST').length, 0);
});

test('failed baseline restoration retains lease cleanup for a verified retry', async t => {
  const r = await setup(t);
  const configured = await r.invoke('loom.fixture.configure', { leaseId: r.leaseId, setting: 'fake-model-scenario', fixtureId: 'fake-model',
    scenarioId: 'fake-model-e31296792340e32b', parameters: {}, agentId: null });
  assert.equal(configured.availability, 'observed'); r.malformedReset(true);
  await assert.rejects(r.fixture.dispose());
  assert.equal(r.driver.processesById.get('fake-model')!.state(), 'running');
  r.malformedReset(false); await r.fixture.dispose();
  assert.equal(r.fixtureRequests.filter(row => row.route === '/__reset').length, 2);
});

test('canonical GitHub script uses exact refs and source patch through the owned fixture service', async t => {
  const r = await setup(t, { fakeGitHub: true });
  const headSha = '1'.repeat(40), baseSha = '2'.repeat(40);
  const configured = await r.invoke('loom.fixture.configure', { leaseId: r.leaseId, setting: 'fake-github-scenario', fixtureId: 'fake-github',
    scenarioId: 'fake-github-review-widget', parameters: { headSha, baseSha }, agentId: null });
  assert.equal(configured.availability, 'observed', JSON.stringify(configured));
  const request = r.fixtureRequests.find(row => row.route === '/__fixture')!.body as { pr: { head: { sha: string }; base: { sha: string } }; files: { patch: string }[] };
  assert.equal(request.pr.head.sha, headSha); assert.equal(request.pr.base.sha, baseSha);
  assert.equal(request.files[0]!.patch, '@@ -1,5 +1,5 @@\n package widget\n \n func ParseName(s string) string {\n-\treturn s\n+\treturn s[1:]\n }');
  assert.equal(r.launches.length, 0);
  await r.fixture.dispose(); assert.equal(r.fixtureRequests.filter(row => row.route === '/__reset').length, 1);
});

test('actual registry discovers two owned legacy workspaces serially through the locked host API', async t => {
  const r = await setup(t, { secondWorkspace: true });
  const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS-2', operation: 'show', name: 'task' });
  assert.equal(role.availability, 'observed', JSON.stringify(role));
  assert.deepEqual(r.launches.map(command => command.argv), [
    ['--workspace','E2E-WS','role','list','--json'],
    ['--workspace','E2E-WS-2','role','list','--json'],
    ['--workspace','E2E-WS-2','role','show','task','--json'],
  ]);
  const usage = await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS-2', agentId: 'worker' } });
  assert.equal(usage.availability, 'observed'); assert.equal(r.launches.at(-1)!.env.LOOM_WORKSPACE_ID, 'E2E-WS-2');
});

test('retained baseline generation reaches the host dispatch and rejects a replacement before POST', async t => {
  const r = await setup(t);
  const access = productionLegacyAccess(r.fixture), signal = new AbortController().signal;
  await access.lease(r.leaseId, signal, 'loom.fixture.configure');
  await access.snapshot(r.leaseId, 'fake-model', signal);
  const before = r.driver.processesById.get('fake-model')!.generation;
  const original = r.driver.requestOwnedHttp.bind(r.driver); let retained: string | undefined;
  r.driver.requestOwnedHttp = async (target, method, relative, body, callSignal, expectedGeneration) => {
    if (target === 'fake-model' && method === 'POST') {
      retained = expectedGeneration;
      await r.driver.restartOwnedProcess('fake-model', before, callSignal);
    }
    return original(target, method, relative, body, callSignal, expectedGeneration);
  };
  await assert.rejects(access.request(r.leaseId, 'fake-model', 'POST', '/__script', { steps: [{ text: 'unused' }] }, signal));
  assert.equal(retained, before);
  assert.notEqual(r.driver.processesById.get('fake-model')!.generation, before);
  assert.equal(r.fixtureRequests.filter(row => row.method === 'POST').length, 0);
});

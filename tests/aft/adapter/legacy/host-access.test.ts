import { test, type TestContext } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createHash } from 'node:crypto';
import * as fs from 'node:fs/promises';
import { CapabilityRegistry, createCapabilityContext, calculateImplementationPin, getRegisteredResource } from '@tysonthomas9/aft/capabilities';
import { createFixtureProviders, productionFixtureOptions } from '../fixture/providers.js';
import { HostFixtureDriver, type HostConfig, type Http } from '../fixture/host.js';
import type { HostProcesses, HostCommand, OwnedProcess } from '../fixture/process.js';
import { LaunchNotStarted } from '../fixture/process.js';
import type { FixturePlan } from '../fixture/lifecycle.js';
import { getFixtureEvidenceStore } from '../evidence.js';
import { fixturesKey, type OwnedFixture } from '../ownership.js';
import { createLegacyProviders } from './providers.js';
import { productionLegacyAccess } from './host-access.js';
import { LegacyError } from './operations.js';
import { createFixtureOperationAuthority } from '../authority.js';
import { LegacyOperationEffects } from './providers.js';
import { testLegacyRoster } from './test-roster.js';
import { materializeRenderer } from '../fixture/renderer-fixtures.test.js';
import type { RegisteredProcessPort } from '../fixture/descendants.js';
import { ObservationError } from '../protocol.js';
import { HostWorkspaceRecords } from '../fixture/workspace-records.js';
import { appendCreatedWorkspaces, requireOwnedWorkspace, enrollOwnedLegacyAgent } from '../workspaces.js';
import { fixtureOwnerIdentity } from '../authority.js';
import { registerLoomAdapter } from '../composition.js';
import { createTerminalDetachProviders, productionTerminalMetadataAccess } from './terminal-providers.js';
import { TerminalDetachEffects } from './effects.js';
import type { Json } from '../protocol.js';

const hash = (bytes: string) => createHash('sha256').update(bytes).digest('hex');
async function setup(t: TestContext, options: { seedWorktree?: boolean; liveClaude?: boolean; invalidStartup?: boolean; fakeGitHub?: boolean; onRoot?: (root: string) => void; terminal?: 'default' | 'factory-probe' } = {}) {
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
  const plan: FixturePlan = { profile: options.liveClaude ? 'legacy-real-claude' : 'legacy-deterministic', loomRevision: { ...revision }, fleetRevision: { ...revision }, engineRevision: { ...revision },
    adapterRevision: { ...revision }, model: options.liveClaude ? 'configured-model' : 'aft/m', maxCases: 1, caseCount: 1, selectionSha256: 'd'.repeat(64), leaseDurationMs: 600000,
    ...(options.liveClaude ? { liveProvider: { backend: 'claude', model: 'configured-model' } as const } : {}) };
  let count = 0, port = 4300; const launches: HostCommand[] = []; const stops: string[] = [];
  let badAgents = false, failLaunch: '' | 'proven' | 'uncertain' = '', badRoles = false, exitCode = 0, stderr = '';
  let restoredBytes: string | undefined;
  let queued = 0, malformedReset = false;
  const fixtureRequests: { method: string; route: string; body: unknown }[] = [];
  type Repo = { name: string; path: string; source_repo_id: string; groups: string[]; source: string };
  const managed = new Map<string, { source: string; path: string; repo: string; repositories: Repo[] }>();
  const actors = new Map<string, { name: string; repos: string[]; repo_groups: string[]; createdAt?: string; updatedAt?: string; parent?: string }[]>();
  let taskLaunch: (() => void) | undefined;
  let tabBodies: Json[] = [{ data: [] }], tabReads = 0, deleteStatus = 204;
  const tabRequests: { method: string; route: string }[] = [];
  const worktrees = new Map<string, string>();
  let seedHead = revision.commit, seedExit = 0;
  const handles: OwnedProcess[] = [];
  const startedCommands = new Map<number, HostCommand>();
  const parentTokenOverrides = new Map<number, string | undefined>();
  let storeGeneration = 'injected-captured-store';
  const processes: HostProcesses = {
    async run(command) {
      if (command.argv[0] === 'init') { await fs.mkdir(path.join(command.cwd, '.git')); return ''; }
      if (command.argv[0] === 'rev-parse' && command.argv[1] === '--git-common-dir') {
        const repo = [...managed.values()].flatMap(row => row.repositories).find(row => row.path === command.cwd);
        const worktree = [...managed.entries()].find(([ws]) => worktrees.get(ws) === command.cwd)?.[1];
        return path.join(repo?.source ?? worktree?.source ?? command.cwd, '.git');
      }
      if (command.argv[0] === 'symbolic-ref') return 'agents/worker';
      if (command.argv[0] === 'rev-parse' && command.argv[1] === 'HEAD' && [...worktrees.values()].includes(command.cwd)) return seedHead;
      if (command.argv[0] === 'rev-parse') return command.argv[1] === 'HEAD' ? revision.commit : revision.tree;
      if (command.argv[0] === 'ls-files') return sourceEntries.map(entry => entry.relativePath).join('\0') + '\0';
      return '';
    },
    start(command, _readiness, generation) {
      let alive = true; const handle: OwnedProcess = { pid: ++count + 100, generation: generation!, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() {
          if (command.argv[0] === 'serve') {
            const directory = path.join(command.env.LOOM_CONFIG_DIR!, 'fleet-db'); await fs.mkdir(directory, { recursive: true });
            await fs.writeFile(path.join(directory, 'runtime.json'), JSON.stringify({ pid: 999, url: 'http://127.0.0.1:6001', started_at: '2026-10-09T00:00:00Z' }));
          }
        }, async stop() { alive = false; stops.push(command.argv[0]!); } };
      handles.push(handle);
      startedCommands.set(handle.pid, structuredClone(command));
      return handle;
    },
    launch(command, stdin, generation) {
      launches.push(command); if (command.argv[4] === 'task') taskLaunch?.(); if (failLaunch === 'proven') throw new LaunchNotStarted();
      if (failLaunch === 'uncertain') throw new Error('Injected uncertain launch failure');
      let alive = true;
      return { pid: ++count + 100, generation, executable: command.executable, argv: command.argv,
        state: () => alive ? 'running' : 'exited', async ready() {}, async stop() { alive = false; stops.push('cli'); },
        async completion() {
          alive = false;
          if (command.argv[0] === 'workspace') {
            const workspaceId = command.argv[3]!, worktree = worktrees.get(workspaceId);
            return { exitCode, stderr, complete: true, stdout: JSON.stringify({ ok: true, workspace: { key: workspaceId },
              agents: [{ name: 'worker', worktree_ready: Boolean(worktree), ...(worktree ? { worktree_path: worktree } : {}) }] }) };
          }
          if (command.argv[0] === 'daemon' && command.argv[1] === 'seed-worktree') {
            const workspaceId = command.argv[3]!, agentName = command.argv[5]!;
            if (seedExit === 0) {
              // Fixture-only bytes written by this injected CLI double. This
              // does not prove any product worktree creation or agent activity.
              await fs.writeFile(path.join(worktrees.get(workspaceId)!, command.argv[7]!), stdin);
              seedHead = 'f'.repeat(40);
            }
            return { exitCode: seedExit, stderr: seedExit ? 'injected seed failure' : '', complete: true,
              stdout: `seeded worktree: ws=${workspaceId} agent=${agentName} repos=${managed.get(workspaceId)!.repositories.length}\n` };
          }
          return { exitCode, stderr, complete: true, stdout:
          command.argv[2] === 'role' ? badRoles ? 'not JSON' : '[{"name":"task","model":"unused-role-model"}]' : '{"agent":"worker","cost":0}' }; } };
    },
  };
  const http: Http = async (_origin, method, route, body) => {
    if (route.includes('/terminal/tabs')) {
      tabRequests.push({ method, route });
      if (method === 'DELETE') return { status: deleteStatus, body: null };
      return { status: 200, body: structuredClone(tabBodies[Math.min(tabReads++, tabBodies.length - 1)]!) };
    }
    if (route.startsWith('/__')) {
      fixtureRequests.push({ method, route, body });
      const github = _origin.endsWith(':4304');
      if (route === '/__requests') return { status: 200, body: github ? [] : options.invalidStartup ? { requests: [] } : { requests: [], queued } };
      if (route === '/__reset') { queued = 0; return { status: 200, body: malformedReset ? {} : { ok: true } }; }
      if (route === '/__script') { queued += (body as { steps: unknown[] }).steps.length; return { status: 200, body: { queued } }; }
      if (route === '/__fixture') { const value = body as { pr: unknown; files: unknown[] }; return { status: 200, body: { ok: true, pr: value.pr, files: value.files.length } }; }
    }
    if (method === 'POST' && route === '/api/workspaces') {
      const input = body as { name: string; repos: string[] }, workspaceId = input.name.toUpperCase();
      const workspace = path.join(driver.runtimeRoot, 'runtime', 'managed', workspaceId);
      const repositories = input.repos.map(source => ({ name: path.basename(source), path: path.join(workspace, path.basename(source)),
        source_repo_id: path.basename(source), groups: [] as string[], source }));
      for (const repo of repositories) await fs.mkdir(repo.path, { recursive: true });
      managed.set(workspaceId, { source: input.repos[0]!, path: workspace, repo: repositories[0]!.path, repositories });
      actors.set(workspaceId, [{ name: 'worker', repos: [], repo_groups: [] }]);
      return { status: 201, body: { success: true, data: { id: workspaceId, path: workspace,
        repos: repositories.map(repo => ({ name: repo.name, path: repo.path, source_repo_id: repo.source_repo_id, groups: repo.groups })) } } };
    }
    if (method === 'POST') return { status: 201, body: {} };
    const workspaceId = route.split('/')[3];
    const repo = managed.get(workspaceId!)?.repo ?? (workspaceId === 'E2E-WS' ? driver.workspaceRoot : path.join(driver.runtimeRoot, 'runtime/e2e-workspace-2'));
    if (route === `/api/workspaces/${workspaceId}`) return { status: 200, body: { success: true,
      data: { id: workspaceId, path: managed.get(workspaceId!)?.path ?? repo,
        repos: managed.get(workspaceId!)!.repositories.map(repo => ({ name: repo.name, path: repo.path, source_repo_id: repo.source_repo_id, groups: repo.groups })) } } };
    if (route.endsWith('/agents')) return { status: 200, body: { success: true, total: actors.get(workspaceId!)!.length + (badAgents ? 1 : 0),
      data: actors.get(workspaceId!)!.map(actor => ({ ...actor, workspace_key: workspaceId, role_name: 'task', parent: actor.parent ?? '',
        created_at: actor.createdAt ?? '2026-10-09T00:00:00Z', updated_at: actor.updatedAt ?? '2026-10-09T00:00:00Z' })) } };
    if (route.endsWith('/issues?limit=1000')) return { status: 200, body: { success: true, data: [{ id: 'issue' }] } };
    return { status: 200, body: {} };
  };
  const files: typeof fs = { ...fs, async rm(filename, options) {
    if (filename === path.join(driver.runtimeRoot, 'runtime')) restoredBytes = await fs.readFile(path.join(driver.configurationRoot, 'agents-opencode/config/opencode/opencode.json'), 'utf8');
    await fs.rm(filename, options);
  } };
  const registeredPort: RegisteredProcessPort = { async capture(pid: number) {
    const parent = handles.find(handle => handle.pid === pid), command = startedCommands.get(pid);
    assert.ok(pid === 999 || parent && command); let alive = true;
    const identity = { pid, generation: pid === 999 ? 'injected-captured-store' : `injected-kernel-${parent!.generation}`,
      executable: pid === 999 ? config.fleetBinary : command!.executable,
      argvSha256: pid === 999 ? 'e'.repeat(64) : hash([command!.executable, ...command!.argv].join('\0') + '\0'),
      parentPid: handles.find(handle => handle.argv[0] === 'serve')!.pid,
      configurationRoot: pid === 999 ? driver.configurationRoot : command!.env.LOOM_CONFIG_DIR!,
      ...(pid === 999 ? {} : { fixtureRunId: command!.env.RUN_ID }), state: 'running' as const };
    return { identity, async inspect() { return { ...identity,
      ...(pid === 999 ? { generation: storeGeneration } : {}),
      ...(parentTokenOverrides.has(pid) ? { fixtureRunId: parentTokenOverrides.get(pid) } : {}),
      state: alive && (!parent || parent.state() === 'running') ? 'running' as const : 'exited' as const }; },
      async stop() { alive = false; if (parent) await parent.stop(); }, async abandon() { assert.fail('Owned captured process must stay enrolled'); } };
  } };
  const driver: HostFixtureDriver = new HostFixtureDriver(config, processes, files, http, () => `injected-${++count}`, async () => ({ port: port++, async release() {} }), undefined,
    registeredPort, () => 1700000000123);
  const adapterRoot = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(adapterRoot, ['fixture/providers.ts'], 'fixture/providers.ts', 'createFixtureProviders');
  const legacyPin = calculateImplementationPin(adapterRoot, ['legacy/providers.ts','legacy/host-access.ts','legacy/cli-plan.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); let factories = 0;
  const fixtureOptions = { ...productionFixtureOptions(pin, pin.sha256, [plan], config, config), driver: () => driver };
  if (options.terminal === 'default') {
    // Exercise the actual public composition and its production default factory.
    // This unit pin is not the final emitted implementation closure receipt.
    const compositionPin = calculateImplementationPin(adapterRoot,
      ['composition.ts', 'legacy/terminal-providers.ts', 'legacy/terminal-metadata.ts', 'legacy/effects.ts', 'fixture/providers.ts'],
      'composition.ts', 'createLoomProviders');
    registerLoomAdapter(registry, { implementation: compositionPin, fixtures: fixtureOptions });
  } else {
    for (const provider of createFixtureProviders(fixtureOptions)) registry.register(provider);
    for (const provider of createLegacyProviders(legacyPin, legacyPin.sha256, (context, fixture) => {
      factories++; return productionLegacyAccess(fixture, getFixtureEvidenceStore(context, fixture.leaseId));
    },
      { taskExecution: options.liveClaude ? 'live-provider' : 'deterministic' })) registry.register(provider);
    if (options.terminal === 'factory-probe') {
      const terminalPin = calculateImplementationPin(adapterRoot,
        ['legacy/terminal-providers.ts', 'legacy/terminal-metadata.ts', 'legacy/effects.ts'],
        'legacy/terminal-providers.ts', 'createTerminalDetachProviders');
      for (const provider of createTerminalDetachProviders(terminalPin, terminalPin.sha256,
        (context, fixture) => { factories++; return productionTerminalMetadataAccess(context, fixture); })) registry.register(provider);
    }
  }
  const context = createCapabilityContext({ file: 'injected-host.yaml', line: 1 }, registry); Object.assign(context, { runId: 'binding-run' });
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  // YAML/JSON wire values have distinct revision objects, not shared JS references.
  const acquired = await invoke('loom.fixture.acquire', { runId: 'binding-run', profile: plan.profile, loomRevision: { ...revision },
    fleetRevision: { ...revision }, model: plan.model, maxCases: 1, selectionSha256: plan.selectionSha256 });
  if (acquired.availability !== 'observed') {
    const directory = fileURLToPath(new URL('.verification/', import.meta.url)); await fs.mkdir(directory, { recursive: true });
    await fs.writeFile(path.join(directory, `acquire-failure-${path.basename(root)}.json`),
      JSON.stringify({ availability: acquired.availability, error: acquired.error, evidenceClass: 'deterministic injected setup' }) + '\n', { flag: 'wx', mode: 0o600 });
  }
  assert.equal(acquired.availability, 'observed', JSON.stringify(acquired.error));
  assert.equal((acquired.data as { fixtureRunId: string }).fixtureRunId, '1700000000');
  assert.notEqual((acquired.data as { fixtureRunId: string }).fixtureRunId, context.runId);
  const leaseId = (acquired.data as { lease: { id: string } }).lease.id;
  const fixture = getRegisteredResource(context, `${fixturesKey}:${leaseId}`, leaseId) as OwnedFixture;
  if (options.seedWorktree) for (const workspaceId of ['E2E-WS', 'E2E-WS-2']) {
    const worktree = path.join(driver.runtimeRoot, 'runtime', 'agent-worktrees', workspaceId, 'worker');
    await fs.mkdir(worktree, { recursive: true }); worktrees.set(workspaceId, worktree);
  }
  return { root, driver, fixture, evidenceStore: getFixtureEvidenceStore(context, leaseId), leaseId, invoke, launches, stops, factories: () => factories,
    tabRequests,
    tabResponses(bodies: Json[], status = 204) { tabBodies = bodies; tabReads = 0; deleteStatus = status; },
    repoName: (workspaceId = 'E2E-WS') => managed.get(workspaceId)!.repositories[0]!.name,
    onTaskLaunch(callback: () => void) { taskLaunch = callback; },
    changeStoreGeneration(value: string) { storeGeneration = value; },
    async addMultiWorkspace() {
      const signal = new AbortController().signal, sourceRepos = ['alpha', 'beta'].map(name => path.join(driver.runtimeRoot, 'runtime', 'source-repos', name));
      for (const source of sourceRepos) await fs.mkdir(path.join(source, '.git'), { recursive: true });
      // Retain the actual successful injected HTTP creation in the production
      // private recorder. Finite native factory provisioning remains separate.
      const response = await driver.requestOwnedHttp('api', 'POST', '/api/workspaces', { name: 'owned-multi', type: 'empty', repos: sourceRepos }, signal);
      const directory = path.join(driver.configurationRoot, 'fleet-db'), stat = await fs.lstat(directory);
      const records = Reflect.get(driver, 'workspaceRecords') as HostWorkspaceRecords;
      await records.captureCreated('OWNED-MULTI', sourceRepos, response,
        { storeId: `${directory}#${stat.dev}:${stat.ino}`, storeGeneration: 'injected-captured-store' }, signal);
      const store = getFixtureEvidenceStore(context, leaseId);
      const record = await records.creationRecord(fixtureOwnerIdentity(fixture), 'OWNED-MULTI', store, signal);
      await appendCreatedWorkspaces(fixture, [record], signal, store);
      actors.get('OWNED-MULTI')!.push({ name: 'beta-worker', repos: ['beta'], repo_groups: [] });
      return { repos: managed.get('OWNED-MULTI')!.repositories, actors: actors.get('OWNED-MULTI')! };
    },
    changeParentToken(value: string | undefined) {
      parentTokenOverrides.set(driver.processesById.get('serve')!.pid, value);
    },
    worktree: (workspaceId = 'E2E-WS') => worktrees.get(workspaceId)!, seedFailure: () => { seedExit = 17; },
    fixtureRequests, queued: () => queued, malformedReset: (value: boolean) => { malformedReset = value; },
    badAgents: () => { badAgents = true; }, badRoles: () => { badRoles = true; }, failLaunch: (kind: 'proven' | 'uncertain' = 'proven') => { failLaunch = kind; },
    processFailure: () => { exitCode = 17; stderr = 'exact process diagnostic'; }, restored: () => restoredBytes,
    async cleanup() { await fs.rm(root, { recursive: true, force: true }); } };
}

test('actual Host and public legacy registry preserve unselected multi-repository membership and exact task selectors', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const task = (agentName: string, repoName: string | null) => r.invoke('loom.cli.task', {
    leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName, backend: 'codex', mode: 'once', issueId: null, repoName });
  for (const request of [
    () => r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'FOREIGN', operation: 'list', name: null }),
    () => r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentId: 'foreign' } }),
    () => task('beta-worker', 'alpha'), () => task('worker', 'missing'),
  ]) {
    assert.equal((await request()).availability, 'error'); assert.equal(r.factories(), 0); assert.equal(r.launches.length, 0);
  }
  const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', operation: 'list', name: null });
  assert.equal(role.availability, 'observed', JSON.stringify(role));
  assert.deepEqual(r.launches.at(-1)!.argv, ['--workspace', 'OWNED-MULTI', 'role', 'list', '--json']);
  for (const workspaceId of ['E2E-WS', 'OWNED-MULTI']) {
    const usage = await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId, agentId: 'worker' } });
    assert.equal(usage.availability, 'observed', JSON.stringify(usage));
    assert.deepEqual(r.launches.at(-1)!.argv, ['usage', '--format', 'json', '--agent', 'worker']);
    assert.equal(r.launches.at(-1)!.env.LOOM_WORKSPACE_ID, workspaceId);
  }
  const fact = await r.driver.readWorkspaceLegacyAgent(fixtureOwnerIdentity(r.fixture), 'OWNED-MULTI', 'worker', new AbortController().signal);
  assert.equal(fact.repo, null); assert.equal(fact.commonDir, null);
  assert.throws(() => requireOwnedWorkspace(r.fixture, 'OWNED-MULTI', 'worker', 'legacy-agent-name'), /selection/);
  const beta = topology.repos.find(repo => repo.name === 'beta')!;
  for (const [agentName, repoName, sourcePath] of [['worker', null, ''], ['worker', 'beta', beta.path], ['beta-worker', 'beta', beta.path]] as const) {
    const result = await task(agentName, repoName); assert.equal(result.availability, 'observed', JSON.stringify(result));
    assert.deepEqual(r.launches.at(-1)!.argv, ['--workspace', 'OWNED-MULTI', '--backend', 'codex', 'task', agentName]);
    assert.equal(r.launches.at(-1)!.env.LOOM_WORKSPACE_ID, 'OWNED-MULTI');
    assert.equal(r.launches.at(-1)!.env.LOOM_SOURCE_REPOS, sourcePath);
    assert.equal(r.launches.at(-1)!.env.LOOM_ASSIGNED_TASK_ID, '');
  }
  topology.repos.reverse(); assert.equal((await task('worker', 'beta')).availability, 'observed');
  assert.equal(r.launches.at(-1)!.env.LOOM_SOURCE_REPOS, beta.path);
  const before = r.launches.length;
  topology.actors.find(actor => actor.name === 'beta-worker')!.repos = ['alpha'];
  assert.equal((await task('beta-worker', 'beta')).availability, 'error'); assert.equal(r.launches.length, before);
  topology.actors.find(actor => actor.name === 'beta-worker')!.repos = ['beta'];
  beta.groups = ['reassigned']; assert.equal((await task('worker', 'beta')).availability, 'error');
  assert.equal(r.launches.length, before); beta.groups = [];
  const sourceId = beta.source_repo_id; beta.source_repo_id = 'foreign-source';
  assert.equal((await task('worker', 'beta')).availability, 'error'); assert.equal(r.launches.length, before); beta.source_repo_id = sourceId;
  r.changeStoreGeneration('foreign-kernel-generation');
  assert.equal((await task('worker', 'beta')).availability, 'error'); assert.equal(r.launches.length, before);
  r.changeStoreGeneration('injected-captured-store');
});

test('canonical host binding refuses changed or missing captured serve RUN_ID before CLI effects', async t => {
  const r = await setup(t);
  for (const token of ['foreign-token', undefined]) {
    r.changeParentToken(token);
    await assert.rejects(r.driver.runtimeIdentity(new AbortController().signal), /ownership-mismatch|identity-mismatch/);
    const result = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'list', name: null });
    assert.equal(result.availability, 'error'); assert.equal(r.factories(), 0); assert.equal(r.launches.length, 0);
  }
  r.changeParentToken('1700000000');
  assert.deepEqual(await r.driver.runtimeIdentity(new AbortController().signal), { fixtureRunId: '1700000000' });
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('production host binding preserves managed workspace and source/config identities for original CLI actors', async t => {
  const r = await setup(t);
  assert.notEqual(r.fixture.repo, r.driver.workspaceRoot);
  assert.equal(r.fixture.ownedWorkspaces!.length, 2);
  const primary = r.fixture.ownedWorkspaces!.find(row => row.workspaceId === 'E2E-WS')!;
  assert.equal(primary.commonDir, path.join(r.driver.workspaceRoot, '.git'));
  assert.equal(primary.storeGeneration, 'injected-captured-store');
  for (const workspaceId of ['E2E-WS', 'E2E-WS-2']) {
    const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId, operation: 'show', name: 'task' });
    assert.equal(role.availability, 'observed', JSON.stringify(role.error));
    assert.equal((await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId, agentId: 'worker' } })).availability, 'observed');
    for (const backend of ['codex', 'claude', 'cursor', 'opencode']) for (const mode of ['once', 'auto', 'daemon']) {
      const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId, agentName: 'worker', backend, mode,
        issueId: mode === 'daemon' ? 'issue' : null, repoName: r.repoName(workspaceId) });
      assert.equal(result.availability, 'observed', JSON.stringify(result.error));
      const command = r.launches.at(-1)!;
      assert.equal(command.cwd, r.driver.workspaceRoot);
      assert.equal(command.env.LOOM_CONFIG_DIR, r.driver.configurationRoot);
      assert.equal(command.env.LOOM_SOURCE_REPOS, r.fixture.ownedWorkspaces!.find(row => row.workspaceId === workspaceId)!.repo);
      assert.deepEqual(command.argv, ['--workspace', workspaceId, '--backend', backend, 'task', 'worker',
        ...(mode === 'once' ? [] : ['--auto']), ...(mode === 'daemon' ? ['--daemon-mode'] : [])]);
    }
  }
  // Source association alone cannot authorize writes when product diagnostics
  // do not attest an existing ready worktree.
  const before = r.launches.length;
  const seed = await r.invoke('loom.fixture.seedWorktree', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    relativePath: 'marker.txt', content: 'fixture bytes\n', commitMessage: 'fixture seed' });
  assert.equal(seed.availability, 'error');
  assert.equal(r.launches.slice(before).filter(command => command.argv[0] === 'daemon').length, 0);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('managed binding refuses unrelated primary repo and forged commonDir or store associations before CLI effects', async t => {
  const r = await setup(t);
  const originalRepo = r.fixture.repo, originalRoster = r.fixture.ownedWorkspaces;
  const other = path.join(r.driver.runtimeRoot, 'runtime', 'unrelated'); await fs.mkdir(other);
  r.fixture.repo = other;
  const input = { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'show', name: 'task' };
  assert.equal((await r.invoke('loom.cli.role', input)).availability, 'error'); assert.equal(r.launches.length, 0);
  r.fixture.repo = originalRepo;
  // An authenticated injected receipt with the wrong Git/store association
  // cannot replace the driver's actual successful provisioning facts.
  r.fixture.ownedWorkspaces = await testLegacyRoster(r.fixture, r.evidenceStore,
    originalRoster!.map(row => ({ workspaceId: row.workspaceId, repo: row.repo, agentIds: [] })));
  assert.equal((await r.invoke('loom.cli.role', input)).availability, 'error'); assert.equal(r.launches.length, 0);
  // Equality with the launcher's source path is not an alternate ownership
  // authority; the private creation still names the managed checkout.
  r.fixture.repo = r.driver.workspaceRoot;
  r.fixture.ownedWorkspaces = await testLegacyRoster(r.fixture, r.evidenceStore,
    originalRoster!.map(row => ({ workspaceId: row.workspaceId,
      repo: row.workspaceId === 'E2E-WS' ? r.driver.workspaceRoot : row.repo, agentIds: [] })));
  assert.equal((await r.invoke('loom.cli.role', input)).availability, 'error'); assert.equal(r.launches.length, 0);
  r.fixture.repo = originalRepo;
  r.fixture.ownedWorkspaces = originalRoster;
  assert.equal((await r.invoke('loom.cli.role', input)).availability, 'observed');
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('managed production access requires the existing canonical evidence store before any CLI effect', async t => {
  const r = await setup(t);
  assert.throws(() => productionLegacyAccess(r.fixture), error => error instanceof LegacyError && error.code === 'unsupported-capability');
  assert.equal(r.launches.length, 0);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('production managed binding rejects replaced captured store before legacy agent read or CLI launch', async t => {
  const r = await setup(t);
  const filename = path.join(r.driver.configurationRoot, 'fleet-db', 'runtime.json'), original = await fs.readFile(filename, 'utf8');
  await fs.writeFile(filename, original.replace('999', '998'));
  const result = await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS', agentId: 'worker' } });
  assert.equal(result.availability, 'error'); assert.equal(r.launches.length, 0);
  await fs.writeFile(filename, original);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

const seededCommand = (r: Awaited<ReturnType<typeof setup>>) => r.launches.filter(command => command.argv[1] === 'seed-worktree');
const seedInput = { workspaceId: 'E2E-WS', agentName: 'worker', relativePath: 'marker.txt', content: 'fixture marker\n', commitMessage: 'fixture diff' };

test('source-resolved existing worktree seed preserves exact CLI actor, stdin and fixture-only receipt', async t => {
  const r = await setup(t, { seedWorktree: true });
  for (const workspaceId of ['E2E-WS', 'E2E-WS-2']) {
    const result = await r.invoke('loom.fixture.seedWorktree', { ...seedInput, workspaceId, leaseId: r.leaseId });
    assert.equal(result.availability, 'observed', JSON.stringify(result.error));
    assert.equal((result.data as { commit: string }).commit, 'f'.repeat(40));
    assert.deepEqual((result.data as { receipt: { facts: unknown } }).receipt.facts, { commit: 'f'.repeat(40), actorActivity: false });
    assert.equal(await fs.readFile(path.join(r.worktree(workspaceId), 'marker.txt'), 'utf8'), seedInput.content);
    const command = seededCommand(r).at(-1)!;
    assert.deepEqual(command.argv, ['daemon', 'seed-worktree', '--workspace', workspaceId, '--agent', 'worker',
      '--file', 'marker.txt', '--content', '-', '--message', 'fixture diff']);
    assert.equal(command.env.LOOM_TESTSUPPORT, '1'); assert.equal(command.cwd, r.driver.workspaceRoot);
  }
  assert.equal(seededCommand(r).length, 2);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('actual owned seed binding rejects path escapes, symlinks and hard links before the seed actor', async t => {
  const r = await setup(t, { seedWorktree: true });
  const outside = path.join(r.root, 'outside.txt'); await fs.writeFile(outside, 'unchanged');
  await fs.symlink(outside, path.join(r.worktree(), 'linked.txt'));
  await fs.link(outside, path.join(r.worktree(), 'hard.txt'));
  await assert.rejects(r.invoke('loom.fixture.seedWorktree', { ...seedInput, leaseId: r.leaseId, relativePath: '../outside.txt' }));
  for (const relativePath of ['linked.txt', 'hard.txt']) {
    const result = await r.invoke('loom.fixture.seedWorktree', { ...seedInput, leaseId: r.leaseId, relativePath });
    assert.equal(result.availability, 'error');
  }
  assert.equal(seededCommand(r).length, 0); assert.equal(await fs.readFile(outside, 'utf8'), 'unchanged');
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('seed rechecks the exact physical worktree after validation and before CLI mutation', async t => {
  const r = await setup(t, { seedWorktree: true });
  await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS', agentId: 'worker' } });
  const access = productionLegacyAccess(r.fixture, r.evidenceStore), signal = new AbortController().signal;
  const lease = await access.lease(r.leaseId, signal, 'loom.fixture.seedWorktree');
  await access.validateSeedPath(r.leaseId, 'E2E-WS', 'worker', 'marker.txt', signal);
  await fs.rename(r.worktree(), r.worktree() + '-retired'); await fs.mkdir(r.worktree());
  await assert.rejects(access.execute(r.leaseId, { binary: lease.binary, cwd: lease.cwd,
    argv: ['daemon', 'seed-worktree', '--workspace', 'E2E-WS', '--agent', 'worker', '--file', 'marker.txt', '--content', '-', '--message', 'fixture diff'],
    env: { ...lease.env, LOOM_TESTSUPPORT: '1' }, stdin: seedInput.content }, signal));
  assert.equal(seededCommand(r).length, 0); await assert.rejects(fs.stat(path.join(r.worktree(), 'marker.txt')), { code: 'ENOENT' });
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('failed seed process keeps its uncertain target and never retries the mutating actor', async t => {
  const r = await setup(t, { seedWorktree: true }); r.seedFailure();
  const input = { ...seedInput, leaseId: r.leaseId };
  assert.equal((await r.invoke('loom.fixture.seedWorktree', input)).availability, 'error');
  assert.equal((await r.invoke('loom.fixture.seedWorktree', input)).availability, 'error');
  assert.equal(seededCommand(r).length, 1); await assert.rejects(fs.stat(path.join(r.worktree(), 'marker.txt')), { code: 'ENOENT' });
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('post-seed worktree replacement cannot produce a successful commit fact or replay the actor', async t => {
  const r = await setup(t, { seedWorktree: true });
  const read = r.driver.readLegacyWorktreeHead.bind(r.driver);
  r.driver.readLegacyWorktreeHead = async (workspaceId, agentName, signal) => {
    const head = await read(workspaceId, agentName, signal);
    await fs.rename(r.worktree(), r.worktree() + '-retired'); await fs.mkdir(r.worktree()); return head;
  };
  const input = { ...seedInput, leaseId: r.leaseId };
  const result = await r.invoke('loom.fixture.seedWorktree', input);
  assert.equal(result.availability, 'error'); assert.equal(result.data, undefined);
  assert.equal((await r.invoke('loom.fixture.seedWorktree', input)).availability, 'error');
  assert.equal(seededCommand(r).length, 1);
  assert.equal((await r.invoke('loom.fixture.release', { leaseId: r.leaseId })).availability, 'observed');
});

test('canonical acquisition binds actual host driver to role, usage, all backend argv and serve generations', async t => {
  const r = await setup(t);
  const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS', operation: 'show', name: 'task' });
  assert.equal(role.availability, 'observed', JSON.stringify(role.error));
  assert.equal((await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS', agentId: 'worker' } })).availability, 'observed');
  for (const backend of ['codex','claude','cursor','opencode']) for (const mode of ['once','auto','daemon']) {
    const result = await r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', backend,
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: r.repoName() });
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
  const access = productionLegacyAccess(r.fixture, r.evidenceStore);
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
  const access = productionLegacyAccess(r.fixture, r.evidenceStore), signal = new AbortController().signal;
  for (const target of ['scripted-backend','workspace:E2E-WS']) await assert.rejects(access.snapshot(r.leaseId, target, signal), LegacyError);
  await assert.rejects(access.snapshot(r.leaseId, 'fake-github', signal));
  await assert.rejects(access.validateSeedPath(r.leaseId, 'E2E-WS', 'worker', 'file', signal),
    error => error instanceof ObservationError && error.code === 'ownership-mismatch');
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
  const access = productionLegacyAccess(r.fixture, r.evidenceStore), signal = new AbortController().signal;
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

function terminalTestGrant(fixture: OwnedFixture, effects: readonly string[] = TerminalDetachEffects) {
  // An explicit injected owner grant exercises canonical admission. It is not
  // evidence that the production fixture's grant builder supplies this route.
  fixture.operationAuthority = createFixtureOperationAuthority(fixtureOwnerIdentity(fixture), {
    'loom.runtime.detachTerminal': { evidenceClass: 'deterministic', effects: [...effects] as (typeof TerminalDetachEffects)[number][] },
  });
}
async function observedServeGeneration(r: Awaited<ReturnType<typeof setup>>) {
  const observed = await r.invoke('loom.fixture.observe', { leaseId: r.leaseId });
  assert.equal(observed.availability, 'observed');
  const services = (observed.data as { services: { id: string; generation: string; state: string }[] }).services;
  const serve = services.filter(service => service.id === 'serve');
  assert.equal(serve.length, 1); assert.equal(serve[0]!.state, 'running');
  return serve[0]!.generation;
}

test('default composition detaches every owned terminal metadata target with an observed serve generation', async t => {
  const r = await setup(t, { terminal: 'default' }); terminalTestGrant(r.fixture);
  const generation = await observedServeGeneration(r);
  const initial = { data: [{ session_name: 'first', agent_id: 'worker' }, { session_name: 'foreign', agent_id: 'other' },
    { session_name: 'second', agent_id: 'worker' }] };
  r.tabResponses([initial, initial, { data: [] }]);
  const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    expectedServeGeneration: generation });
  assert.equal(result.availability, 'observed', JSON.stringify(result.error));
  const data = result.data as { capturedSessions: string[]; remainingSessions: string[]; deletions: { sessionName: string; status: number }[]; serve: { generation: string } };
  assert.deepEqual(data.capturedSessions, ['first', 'second']); assert.deepEqual(data.remainingSessions, []);
  assert.deepEqual(data.deletions, [{ sessionName: 'first', status: 204, outcome: 'http-success' },
    { sessionName: 'second', status: 204, outcome: 'http-success' }]);
  assert.equal(data.serve.generation, generation);
  assert.deepEqual(r.tabRequests, [
    { method: 'GET', route: '/api/workspaces/E2E-WS/terminal/tabs' }, { method: 'GET', route: '/api/workspaces/E2E-WS/terminal/tabs' },
    { method: 'DELETE', route: '/api/workspaces/E2E-WS/terminal/tabs/first' }, { method: 'DELETE', route: '/api/workspaces/E2E-WS/terminal/tabs/second' },
    { method: 'GET', route: '/api/workspaces/E2E-WS/terminal/tabs' }]);
  assert.equal('afterGeneration' in data, false); assert.equal(r.launches.length, 0);
});

test('terminal stale generation is rejected before the production access factory or tab transport', async t => {
  const r = await setup(t, { terminal: 'factory-probe' }); terminalTestGrant(r.fixture);
  const generation = await observedServeGeneration(r);
  const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    expectedServeGeneration: generation + '-stale' });
  assert.equal(result.availability, 'error'); assert.equal(r.factories(), 0);
  assert.deepEqual(r.tabRequests, []); assert.equal(r.launches.length, 0);
});

test('default terminal provider requires a final reachable read even with no matching tabs', async t => {
  const r = await setup(t, { terminal: 'default' }); terminalTestGrant(r.fixture);
  const generation = await observedServeGeneration(r); r.tabResponses([[], { data: [] }, []]);
  const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    expectedServeGeneration: generation });
  assert.equal(result.availability, 'observed', JSON.stringify(result.error));
  assert.deepEqual((result.data as { remainingSessions: string[] }).remainingSessions, []);
  assert.equal(r.tabRequests.length, 3); assert.ok(r.tabRequests.every(request => request.method === 'GET'));
});

test('terminal admission denies missing grant, missing effects and foreign identities before the production factory', async t => {
  const r = await setup(t, { terminal: 'factory-probe' });
  const generation = await observedServeGeneration(r);
  const input = { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker', expectedServeGeneration: generation };
  assert.equal((await r.invoke('loom.runtime.detachTerminal', input)).availability, 'unsupported');
  for (const effect of TerminalDetachEffects) {
    terminalTestGrant(r.fixture, TerminalDetachEffects.filter(value => value !== effect));
    assert.equal((await r.invoke('loom.runtime.detachTerminal', input)).availability, 'unsupported', effect);
  }
  terminalTestGrant(r.fixture);
  for (const patch of [{ leaseId: 'foreign' }, { workspaceId: 'foreign' }, { agentName: 'foreign' }])
    assert.equal((await r.invoke('loom.runtime.detachTerminal', { ...input, ...patch })).availability, 'error');
  r.fixture.operationAuthority = createFixtureOperationAuthority(fixtureOwnerIdentity(r.fixture), {
    'loom.runtime.detachTerminal': { evidenceClass: 'live-provider', effects: [...TerminalDetachEffects] },
  });
  assert.equal((await r.invoke('loom.runtime.detachTerminal', input)).availability, 'error');
  assert.equal(r.factories(), 0); assert.deepEqual(r.tabRequests, []); assert.equal(r.launches.length, 0);
});

test('terminal enrollment rejects a recreated or removed same-name actor before access creation', async t => {
  for (const mutation of ['recreated', 'removed'] as const) {
    const r = await setup(t, { terminal: 'factory-probe' }); terminalTestGrant(r.fixture);
    const topology = await r.addMultiWorkspace(), generation = await observedServeGeneration(r);
    // Retain the initial actual private observation before testing replacement;
    // workspace membership alone does not attest an earlier actor incarnation.
    await enrollOwnedLegacyAgent(r.fixture, 'OWNED-MULTI', 'worker', new AbortController().signal, r.evidenceStore);
    if (mutation === 'recreated') topology.actors[0]!.createdAt = '2026-10-09T01:00:00Z';
    else topology.actors.splice(0, 1);
    const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName: 'worker',
      expectedServeGeneration: generation });
    assert.equal(result.availability, 'error', mutation); assert.equal(r.factories(), 0, mutation);
    assert.deepEqual(r.tabRequests, []); assert.equal(r.launches.length, 0);
  }
});

test('default terminal transport preserves failed DELETE and remaining metadata without an exit claim', async t => {
  const r = await setup(t, { terminal: 'default' }); terminalTestGrant(r.fixture);
  const generation = await observedServeGeneration(r), row = { session_name: 'still-there', agent_id: 'worker' };
  r.tabResponses([{ data: [row] }], 503);
  const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    expectedServeGeneration: generation });
  assert.equal(result.availability, 'observed', JSON.stringify(result.error));
  const data = result.data as { deletions: unknown[]; remainingSessions: string[] };
  assert.deepEqual(data.deletions, [{ sessionName: 'still-there', status: 503, outcome: 'http-failure' }]);
  assert.deepEqual(data.remainingSessions, ['still-there']); assert.equal('afterGeneration' in data, false);
  assert.equal(r.tabRequests.filter(request => request.method === 'DELETE').length, 1);
});

test('malformed final terminal metadata cannot satisfy an absence observation after a DELETE', async t => {
  const r = await setup(t, { terminal: 'default' }); terminalTestGrant(r.fixture);
  const generation = await observedServeGeneration(r), initial = { data: [{ session_name: 'one', agent_id: 'worker' }] };
  r.tabResponses([initial, initial, { unexpected: true }]);
  const result = await r.invoke('loom.runtime.detachTerminal', { leaseId: r.leaseId, workspaceId: 'E2E-WS', agentName: 'worker',
    expectedServeGeneration: generation });
  assert.equal(result.availability, 'error');
  assert.equal(r.tabRequests.filter(request => request.method === 'DELETE').length, 1);
  assert.equal(result.data, undefined);
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
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: r.repoName() });
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
      mode, issueId: mode === 'daemon' ? 'issue' : null, repoName: r.repoName() });
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
  const access = productionLegacyAccess(r.fixture, r.evidenceStore), signal = new AbortController().signal;
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
  const r = await setup(t);
  const role = await r.invoke('loom.cli.role', { leaseId: r.leaseId, workspaceId: 'E2E-WS-2', operation: 'show', name: 'task' });
  assert.equal(role.availability, 'observed', JSON.stringify(role));
  assert.deepEqual(r.launches.map(command => command.argv), [
    ['--workspace','E2E-WS-2','role','list','--json'],
    ['--workspace','E2E-WS','role','list','--json'],
    ['--workspace','E2E-WS-2','role','show','task','--json'],
  ]);
  const usage = await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'E2E-WS-2', agentId: 'worker' } });
  assert.equal(usage.availability, 'observed'); assert.equal(r.launches.at(-1)!.env.LOOM_WORKSPACE_ID, 'E2E-WS-2');
});

test('retained baseline generation reaches the host dispatch and rejects a replacement before POST', async t => {
  const r = await setup(t);
  const access = productionLegacyAccess(r.fixture, r.evidenceStore), signal = new AbortController().signal;
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


test('current-enrollment actual Host rejects a recreated name before discovery or task launch', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const task = () => r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null, repoName: 'beta' });
  assert.equal((await task()).availability, 'observed');
  const before = r.launches.length;
  topology.actors.find(actor => actor.name === 'worker')!.createdAt = '2026-10-09T00:01:00Z';
  assert.equal((await task()).availability, 'error'); assert.equal(r.launches.length, before);
  assert.equal((await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentId: 'worker' } })).availability, 'error');
  assert.equal(r.launches.length, before);
});

test('current-enrollment actual Host rejects changed current parent lineage before CLI effects', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const task = () => r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null, repoName: 'beta' });
  assert.equal((await task()).availability, 'observed');
  const before = r.launches.length;
  topology.actors.find(actor => actor.name === 'worker')!.parent = 'foreign-parent';
  assert.equal((await task()).availability, 'error'); assert.equal(r.launches.length, before);
});

test('current-enrollment actual Host allows updatedAt during a task and current assignment narrowing', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const actor = topology.actors.find(actor => actor.name === 'worker')!;
  const task = () => r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null, repoName: 'beta' });
  assert.equal((await task()).availability, 'observed');
  actor.repos = ['beta']; actor.updatedAt = '2026-10-09T00:01:00Z';
  assert.equal((await task()).availability, 'observed');
  let applied = 0; r.onTaskLaunch(() => { applied++; actor.updatedAt = '2026-10-09T00:02:00Z'; });
  const result = await task(); assert.equal(applied, 1); assert.equal(result.availability, 'observed', JSON.stringify(result));
  assert.deepEqual(r.launches.at(-1)!.argv, ['--workspace', 'OWNED-MULTI', '--backend', 'codex', 'task', 'worker']);
});

test('current-enrollment actual Host rejects a removed actor before discovery and usage', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const task = () => r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null, repoName: 'beta' });
  assert.equal((await task()).availability, 'observed');
  const before = r.launches.length;
  topology.actors.splice(topology.actors.findIndex(actor => actor.name === 'worker'), 1);
  assert.equal((await task()).availability, 'error'); assert.equal(r.launches.length, before);
  assert.equal((await r.invoke('loom.cli.usage', { agent: { fixtureLeaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentId: 'worker' } })).availability, 'error');
  assert.equal(r.launches.length, before);
});

test('current-enrollment actual Host rejects a recreated ancestor before child task effects', async t => {
  const r = await setup(t), topology = await r.addMultiWorkspace();
  const task = (agentName: string) => r.invoke('loom.cli.task', { leaseId: r.leaseId, workspaceId: 'OWNED-MULTI', agentName, backend: 'codex', mode: 'once', issueId: null, repoName: 'beta' });
  assert.equal((await task('worker')).availability, 'observed');
  topology.actors.push({ name: 'child', repos: ['beta'], repo_groups: [], parent: 'worker' });
  assert.equal((await task('child')).availability, 'observed');
  const before = r.launches.length;
  topology.actors.find(actor => actor.name === 'worker')!.createdAt = '2026-10-09T00:01:00Z';
  assert.equal((await task('child')).availability, 'error'); assert.equal(r.launches.length, before);
});

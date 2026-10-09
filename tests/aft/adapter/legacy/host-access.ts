import path from 'node:path';
import { isDeepStrictEqual } from 'node:util';
import { constants } from 'node:fs';
import { lstat, realpath, open } from 'node:fs/promises';
import { z } from 'zod';
import type { OwnedFixture } from '../ownership.js';
import { privateFixtureDriver } from '../fixture/providers.js';
import { HostFixtureDriver } from '../fixture/host.js';
import { HttpResponse, Json } from '../protocol.js';
import { checkedCliPlan } from './cli-plan.js';
import { LegacyError, LegacyEvidenceClasses, type LegacyAccess, type LegacyLease, type ConfigurationSnapshot, type LegacyAuthorizedOperation } from './operations.js';
import { fixtureOwnerIdentity, getFixtureOperationAuthority } from '../authority.js';
import type { EvidenceStore } from '../evidence.js';
import { LegacyOperationEffects } from './effects.js';
import { enrollOwnedLegacyAgent, requireOwnedWorkspace, requireOwnedWorkspaceRecord, resolveLegacyRepositoryAssignments } from '../workspaces.js';
import { checkConfiguredModel } from './model-selection.js';
import type { BaselineTarget } from '../fixture/baseline.js';
import { validateLocalSeedPath } from './seed-path.js';

const Name = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/);
// These projections follow ops.WorkspaceData and domain.Agent, not AgentRow.
const Workspace = z.object({ success: z.literal(true), data: z.object({ id: Name, path: z.string(),
  repos: z.array(z.object({ name: Name, path: z.string(), source_repo_id: z.string().min(1),
    groups: z.array(z.string().min(1).max(512)).max(32) }).passthrough()).max(32) }).passthrough() }).passthrough();
const Agents = z.object({ success: z.literal(true), total: z.number().int().nonnegative(),
  data: z.array(z.object({ workspace_key: Name, name: Name, role_name: Name, updated_at: z.string().min(1) }).passthrough()) }).passthrough();
const Issues = z.object({ success: z.literal(true), data: z.array(z.object({ id: Name }).passthrough()).max(999) }).passthrough();
const Roles = z.array(z.object({ name: Name }).passthrough()).max(1000);
const unsupported = (): never => { throw new LegacyError('unsupported-capability', 'Fixture has no concrete owned hook for this legacy target'); };
const requireOwned = (ok: unknown): void => { if (!ok) throw new LegacyError('ownership-mismatch', 'Legacy fixture identity changed'); };

/** Root composition uses the canonical private driver; no second lease store. */
export function productionLegacyAccess(fixture: OwnedFixture, evidenceStore?: EvidenceStore): LegacyAccess {
  const driver = privateFixtureDriver(fixture);
  if (!(driver instanceof HostFixtureDriver)) return unsupported();
  return createHostLegacyAccess(fixture, driver, evidenceStore);
}

/** Tests inject the actual HostFixtureDriver's process/HTTP seams. */
export function createHostLegacyAccess(fixture: OwnedFixture, driver: HostFixtureDriver, evidenceStore?: EvidenceStore): LegacyAccess {
  if (!evidenceStore) return unsupported();
  requireOwned(z.enum(LegacyEvidenceClasses).safeParse(fixture.evidenceClass).success && fixture.profile.startsWith('legacy-'));
  requireOwned(fixture.profile === 'legacy-deterministic' ? fixture.evidenceClass === 'deterministic' :
    fixture.profile.startsWith('legacy-real-') && fixture.evidenceClass === 'real-native');
  requireOwned(driver.workspaceRoot.startsWith(driver.runtimeRoot + path.sep) &&
    driver.configurationRoot === path.join(driver.workspaceRoot, '.loom-config'));
  let cached: LegacyLease | undefined;
  const baselines = new Map<BaselineTarget, string>();
  type SeedResolution = Awaited<ReturnType<HostFixtureDriver['resolveLegacyWorktree']>>;
  const seeds = new Map<string, { resolved: SeedResolution; relativePath: string; state: 'prepared' | 'executing' | 'completed' | 'uncertain' }>();
  const seedKey = (workspaceId: string, agentName: string) => JSON.stringify([workspaceId, agentName]);
  const seedResolution = async (workspaceId: string, agentName: string, signal: AbortSignal) => {
    const workspace = requireOwnedWorkspace(fixture, workspaceId, agentName, 'legacy-agent-name');
    const resolved = await driver.resolveLegacyWorktree(workspaceId, agentName, signal);
    requireOwned(resolved.complete && resolved.workspaceId === workspaceId && resolved.agentName === agentName &&
      resolved.commonDir === workspace.commonDir);
    return resolved;
  };
  const sameSeedRoot = (before: SeedResolution, after: SeedResolution) => requireOwned(before.root.path === after.root.path &&
    before.root.device === after.root.device && before.root.inode === after.root.inode &&
    before.commonDir === after.commonDir && before.branch === after.branch);
  const identity = (id: string) => requireOwned(id === fixture.leaseId);
  const evidenceFor = (operation: LegacyAuthorizedOperation) => {
    const route = driver.executionRouting;
    requireOwned(route.profile === fixture.profile && route.evidenceClass === fixture.evidenceClass);
    const expected = operation === 'loom.cli.task' && route.externalProvider ? 'live-provider' : route.evidenceClass;
    const grant = getFixtureOperationAuthority(fixture, operation, operation === 'loom.cli.task' && expected === 'live-provider'
      ? ['start-owned-process', 'external-provider'] : LegacyOperationEffects[operation]);
    if (grant.evidenceClass !== expected) throw new LegacyError('source-mismatch', 'Operation class differs from owned execution routing');
    return expected;
  };
  const verify = async (id: string, signal: AbortSignal) => { identity(id); signal.throwIfAborted(); await fixture.verify(signal); };
  const api = async (route: string, signal: AbortSignal) => {
    const response = HttpResponse.parse(await driver.requestOwnedHttp('api', 'GET', route, null, signal));
    if (response.status !== 200) throw new LegacyError('response-invalid', 'Owned legacy API read failed');
    return response.body;
  };
  const configFile = path.join(driver.configurationRoot, 'agents-opencode', 'config', 'opencode', 'opencode.json');
  let configIdentity: { dev: number; ino: number } | undefined;
  const configuration = async (signal: AbortSignal) => {
    signal.throwIfAborted();
    const root = fixture.roots.get('runtime'); requireOwned(root && root.path === driver.runtimeRoot);
    const stat = await lstat(driver.runtimeRoot); requireOwned(stat.dev === root!.device && stat.ino === root!.inode);
    requireOwned(await realpath(path.dirname(configFile)) === path.dirname(configFile));
    const file = await open(configFile, constants.O_NOFOLLOW | constants.O_RDWR);
    try {
      const stat = await file.stat(); requireOwned(stat.isFile() && stat.nlink === 1 && stat.size <= 65536 && (stat.mode & 0o077) === 0);
      requireOwned(!configIdentity || configIdentity.dev === stat.dev && configIdentity.ino === stat.ino);
      configIdentity ??= { dev: stat.dev, ino: stat.ino };
    }
    catch (error) { await file.close(); throw error; }
    return file;
  };
  const access: LegacyAccess = {
    async lease(id, signal, operation) {
      identity(id); signal.throwIfAborted();
      // Restoration uses the last attested facts, without re-opening authority
      // or depending on an expired capability context's signal.
      if (cached && (!operation || Date.now() >= fixture.expiresAtUtcMs))
        return { ...structuredClone(cached), active: Date.now() < fixture.expiresAtUtcMs };
      if (!operation) return unsupported();
      const evidence = evidenceFor(operation);
      await verify(id, signal);
      const records = fixture.ownedWorkspaces?.filter(row => row.identityKind === 'legacy-agent-name');
      requireOwned(records?.length);
      const primary = requireOwnedWorkspaceRecord(fixture, fixture.workspaceId, 'legacy-agent-name');
      requireOwned(primary?.repo === fixture.repo);
      // A managed checkout is distinct from the launcher's source checkout.
      // Re-read the fixture's successful creation facts and captured store,
      // rather than granting ownership from the API topology below.
      const actual = await driver.ownedWorkspaceRoster(fixtureOwnerIdentity(fixture), evidenceStore, signal);
      for (const record of records!) {
        const matches = actual.filter(row => row.identityKind === 'legacy-agent-name' && row.workspaceId === record.workspaceId);
        requireOwned(matches.length === 1);
        const created = matches[0]!;
        requireOwned(created.repo === record.repo && created.commonDir === record.commonDir &&
          created.storeId === record.storeId && created.storeGeneration === record.storeGeneration &&
          isDeepStrictEqual(created.repositories, record.repositories));
      }
      const cli = driver.cliRegistration;
      // The owned service port rejects overlapping operations. Discovery is
      // bounded by the authenticated roster and serializes without retries.
      const metadata = [];
      for (const record of records!) {
        const owned = requireOwnedWorkspaceRecord(fixture, record.workspaceId, 'legacy-agent-name')!;
        requireOwned(owned.repo.startsWith(driver.runtimeRoot + path.sep) && await realpath(owned.repo) === owned.repo);
        const prefix = `/api/workspaces/${encodeURIComponent(record.workspaceId)}`;
        const workspace = Workspace.parse(await api(prefix, signal)).data;
        requireOwned(workspace.id === record.workspaceId && workspace.path.startsWith(driver.runtimeRoot + path.sep) &&
          await realpath(workspace.path) === workspace.path &&
          (owned.repo === workspace.path || owned.repo.startsWith(workspace.path + path.sep)) &&
          workspace.repos.some(row => row.path === owned.repo));
        const agents = Agents.parse(await api(`${prefix}/agents`, signal));
        requireOwned(agents.total === agents.data.length && agents.data.every(row => row.workspace_key === record.workspaceId) &&
          new Set(agents.data.map(row => row.name)).size === agents.data.length);
        const repos = await Promise.all(workspace.repos.map(async row => {
          if (owned.repositories) {
            const retained = owned.repositories.filter(repo => repo.repoName === row.name && repo.repo === row.path &&
              repo.sourceRepoId === row.source_repo_id && isDeepStrictEqual(repo.groups, [...row.groups].sort()));
            requireOwned(retained.length === 1 && workspace.repos.length === owned.repositories.length);
          } else requireOwned(row.path.startsWith(owned.repo + path.sep) || row.path === owned.repo);
          requireOwned(row.path.startsWith(driver.runtimeRoot + path.sep));
          requireOwned(await realpath(row.path) === row.path);
          return { workspaceId: record.workspaceId, name: row.name, sourcePath: row.path };
        }));
        requireOwned(new Set(repos.map(row => row.name)).size === repos.length);
        // This fixed read CLI is enrolled before launch and declared in the
        // operation effect union. API discovery never grants agent ownership.
        let roles: z.infer<typeof Roles> = [];
        if (operation === 'loom.cli.role' || operation === 'loom.cli.task' && evidence === 'live-provider') {
          const roleResult = await driver.launchOwnedCli(['--workspace', record.workspaceId, 'role', 'list', '--json'], {}, '', true, signal);
          if (!roleResult.completion.complete || roleResult.completion.exitCode !== 0)
            throw new LegacyError('process-failed', 'Owned role discovery CLI failed');
          roles = Roles.parse(JSON.parse(roleResult.completion.stdout));
          requireOwned(new Set(roles.map(row => row.name)).size === roles.length);
        }
        const issues = Issues.parse(await api(`${prefix}/issues?limit=1000`, signal)).data;
        requireOwned(new Set(issues.map(row => row.id)).size === issues.length);
        metadata.push({ agents: agents.data.filter(row => record.agentIds.includes(row.name)).map(row => ({ workspaceId: row.workspace_key,
          id: row.name, name: row.name, generation: row.updated_at })), repos,
          roles: roles.map(row => ({ workspaceId: record.workspaceId, name: row.name })),
          issues: issues.map(row => ({ workspaceId: record.workspaceId, id: row.id })) });
      }
      const serve = driver.processesById.get('serve'); requireOwned(serve);
      // Only the runtime operation admits the fixed Git/kernel discovery
      // effects. These are registered builtin targets, not a complete process
      // inventory or evidence that omitted target kinds are absent.
      const workers = operation === 'loom.runtime.stimulate' && driver.processesById.has('daemon')
        ? await driver.refreshOwnedProductProcesses(signal) : [];
      requireOwned(workers.length <= 1000 && new Set(workers.map(row => row.id)).size === workers.length);
      for (const worker of workers) requireOwned(worker.identityKind === 'legacy-agent-name' &&
        records!.some(record => record.workspaceId === worker.workspaceId) &&
        metadata.some(record => record.agents.some(agent => agent.workspaceId === worker.workspaceId && agent.name === worker.agentId)));
      await driver.inspectOwnedProcess('serve', serve!.generation, signal);
      const fixtures: string[] = fixture.profile === 'legacy-deterministic' ? ['provider-default'] : [];
      for (const target of ['fake-model', 'fake-github'] as const) {
        if (driver.processesById.has(target)) {
          await driver.freshFixtureBaseline(target, signal); fixtures.push(target);
        }
      }
      cached = { id, runId: fixture.runId, active: Date.now() < fixture.expiresAtUtcMs, evidence: fixture.evidenceClass,
        secrets: fixture.secrets, binary: cli.binary, cwd: cli.cwd, env: cli.env, workspaces: records!.map(row => row.workspaceId),
        agents: metadata.flatMap(row => row.agents), roles: metadata.flatMap(row => row.roles),
        issues: metadata.flatMap(row => row.issues), repos: metadata.flatMap(row => row.repos),
        processes: [{ id: 'serve', kind: 'serve', generation: serve!.generation, workspaceId: null, agentName: null, sessionName: null },
          ...workers.map(row => ({ id: row.id, kind: row.kind, generation: row.generation,
            workspaceId: row.workspaceId, agentName: row.agentId, sessionName: row.sessionName, serveGeneration: serve!.generation }))],
        fixtures };
      return { ...structuredClone(cached), evidence };
    },
    async execute(id, command, signal) {
      await verify(id, signal); requireOwned(cached);
      const operation: LegacyAuthorizedOperation = command.argv[0] === 'usage' ? 'loom.cli.usage' : command.argv[1] === 'seed-worktree' ? 'loom.fixture.seedWorktree' :
        command.argv[2] === 'role' ? 'loom.cli.role' : command.argv[4] === 'task' ? 'loom.cli.task' : unsupported();
      const evidence = evidenceFor(operation);
      const plan = checkedCliPlan({ ...cached!, evidence }, command);
      const workspaceId = command.argv[0] === 'usage' ? command.env.LOOM_WORKSPACE_ID! : command.argv[1] === 'seed-worktree' ? command.argv[3]! : command.argv[1]!;
      const actor = operation === 'loom.cli.task' ? command.argv[5] : operation === 'loom.cli.usage' ? command.argv[4] :
        operation === 'loom.fixture.seedWorktree' ? command.argv[5] : undefined;
      const workspace = requireOwnedWorkspaceRecord(fixture, workspaceId, 'legacy-agent-name');
      let observedActor: Awaited<ReturnType<HostFixtureDriver['readWorkspaceLegacyAgent']>> | undefined;
      if (actor !== undefined) {
        await enrollOwnedLegacyAgent(fixture, workspaceId, actor, signal, evidenceStore);
        requireOwned(requireOwnedWorkspaceRecord(fixture, workspaceId, 'legacy-agent-name')?.agentIds.includes(actor));
        // Enrollment records membership once. A task must also narrow its
        // effect against the actual current source assignments, without
        // adopting a changed association or topology anchor as authority.
        observedActor = await driver.readWorkspaceLegacyAgent(fixtureOwnerIdentity(fixture), workspaceId, actor, signal);
        requireOwned(observedActor.name === actor && observedActor.workspaceId === workspaceId &&
          observedActor.storeId === workspace!.storeId && observedActor.storeGeneration === workspace!.storeGeneration);
      }
      if (operation === 'loom.fixture.seedWorktree') requireOwnedWorkspace(fixture, workspaceId, actor, 'legacy-agent-name');
      if (operation === 'loom.cli.task' && command.env.LOOM_SOURCE_REPOS !== '') {
        const selected = cached!.repos.find(repo => repo.workspaceId === workspaceId && repo.sourcePath === command.env.LOOM_SOURCE_REPOS);
        requireOwned(selected);
        if (workspace?.repositories) requireOwnedWorkspace(fixture, workspaceId, actor, 'legacy-agent-name', selected!.name);
      }
      if (operation === 'loom.cli.task' && workspace?.repositories) {
        const assigned = resolveLegacyRepositoryAssignments(workspace.repositories, observedActor!.assignedRepos, observedActor!.assignedRepoGroups);
        const retained = workspace.agentSources!.find(source => source.agentId === actor);
        requireOwned(retained && assigned.every(name => retained.repoNames.includes(name)));
        if (command.env.LOOM_SOURCE_REPOS !== '') requireOwned(workspace.repositories.some(repo =>
          repo.repo === command.env.LOOM_SOURCE_REPOS && assigned.includes(repo.repoName)));
      }
      const seed = operation === 'loom.fixture.seedWorktree' ? seeds.get(seedKey(workspaceId, actor!)) : undefined;
      if (operation === 'loom.fixture.seedWorktree') {
        requireOwned(seed?.state === 'prepared' && seed.relativePath === command.argv[7]);
        const current = await seedResolution(workspaceId, actor!, signal);
        sameSeedRoot(seed!.resolved, current);
        await validateLocalSeedPath(current.root, seed!.relativePath, signal);
      }
      if (operation === 'loom.cli.task') {
        const route = driver.executionRouting;
        requireOwned(route.allowedTaskBackends.includes(command.argv[3]!));
        if (route.evidenceClass !== 'deterministic' && (!route.externalProvider || evidence !== 'live-provider')) return unsupported();
        if (route.modelSelection.kind === 'exact-model') requireOwned(route.modelSelection.model === route.model);
        let configuredModel: string | undefined;
        if (route.modelSelection.kind === 'exact-model' && route.modelSelection.selector === 'opencode-config' && command.argv[3] === 'opencode') {
          const snapshot = await access.snapshot(id, 'provider-default', signal);
          configuredModel = z.object({ model: z.string() }).passthrough().parse(snapshot.previous).model;
        }
        checkConfiguredModel(route.modelSelection, command.argv[3]!, command.env, configuredModel);
      }
      if (seed) seed.state = 'executing';
      try {
        const result = await driver.launchOwnedCli(plan.argv, plan.envOverrides, plan.stdin, plan.waitForExit, signal);
        const registered = await driver.inspectOwnedProcess(result.id, result.generation, signal);
        requireOwned(registered.pid === result.pid);
        // Creation identity and current lineage are canonical retained facts.
        // Mutable updatedAt/model/assignments are not an actor incarnation.
        if (observedActor) await enrollOwnedLegacyAgent(fixture, workspaceId, actor!, signal, evidenceStore);
        if (seed) seed.state = result.completion.complete && result.completion.exitCode === 0 ? 'completed' : 'uncertain';
        return { processId: result.id, generation: result.generation, ...result.completion };
      } catch (error) { if (seed) seed.state = 'uncertain'; throw error; }
    },
    async registerProcess(id, result, signal) { await verify(id, signal); await driver.inspectOwnedProcess(result.processId, result.generation, signal); },
    async stimulate(id, process, operation, request, signal) {
      await verify(id, signal);
      if (operation === 'worker-stop') {
        getFixtureOperationAuthority(fixture, 'loom.runtime.stimulate', LegacyOperationEffects['loom.runtime.stimulate']);
        requireOwned(cached && process.kind === 'worker' && process.workspaceId !== null && process.agentName !== null && process.serveGeneration);
        const target = cached!.processes.filter(row => row.id === process.id);
        requireOwned(target.length === 1 && isDeepStrictEqual(target[0], process));
        requireOwned(request?.method === 'POST' && request.body === null && request.path ===
          `/api/workspaces/${encodeURIComponent(process.workspaceId!)}/agents/${encodeURIComponent(process.agentName!)}/stop`);
        const serves = cached!.processes.filter(row => row.id === 'serve' && row.kind === 'serve');
        requireOwned(serves.length === 1 && serves[0]!.generation === process.serveGeneration);
        await enrollOwnedLegacyAgent(fixture, process.workspaceId!, process.agentName!, signal, evidenceStore);
        const stopped = await driver.stopRegisteredWorker(process.id, process.generation, process.serveGeneration!, signal);
        return { transition: stopped.transition, response: HttpResponse.parse(stopped.response) };
      }
      if (operation !== 'serve-restart' || process.id !== 'serve' || process.kind !== 'serve' || request !== null) return unsupported();
      getFixtureOperationAuthority(fixture, 'loom.runtime.stimulate', [...LegacyOperationEffects['loom.runtime.stimulate'], 'restart-owned-service']);
      const transition = await driver.restartOwnedProcess(process.id, process.generation, signal);
      return { transition, response: null };
    },
    async request(id, target, method, relative, body, signal) {
      await verify(id, signal);
      if ((target !== 'fake-model' && target !== 'fake-github') || method !== 'POST' ||
        !['/__reset', target === 'fake-model' ? '/__script' : '/__fixture'].includes(relative)) return unsupported();
      const generation = baselines.get(target); requireOwned(generation);
      const baseline = await driver.freshFixtureBaseline(target, signal); requireOwned(baseline.generation === generation);
      if (relative === '/__reset') return HttpResponse.parse(await driver.resetFixtureBaseline(target, generation!, signal));
      return HttpResponse.parse(await driver.requestOwnedHttp(target, method, relative, Json.parse(body), signal, generation));
    },
    async validateSeedPath(id, workspaceId, agentName, relativePath, signal) {
      evidenceFor('loom.fixture.seedWorktree'); await verify(id, signal);
      if (fixture.evidenceClass !== 'deterministic') return unsupported();
      const key = seedKey(workspaceId, agentName);
      if (seeds.has(key)) throw new LegacyError('mutation-repeated', 'Owned seed target already has a pending or uncertain mutation');
      const resolved = await seedResolution(workspaceId, agentName, signal);
      await validateLocalSeedPath(resolved.root, relativePath, signal);
      seeds.set(key, { resolved, relativePath, state: 'prepared' });
    },
    async seedCommit(id, workspaceId, agentName, signal) {
      evidenceFor('loom.fixture.seedWorktree'); await verify(id, signal);
      const key = seedKey(workspaceId, agentName), seed = seeds.get(key);
      requireOwned(seed?.state === 'completed');
      sameSeedRoot(seed!.resolved, await seedResolution(workspaceId, agentName, signal));
      const head = await driver.readLegacyWorktreeHead(workspaceId, agentName, signal);
      sameSeedRoot(seed!.resolved, await seedResolution(workspaceId, agentName, signal));
      seeds.delete(key); return head;
    },
    async snapshot(id, target, signal): Promise<ConfigurationSnapshot> {
      await verify(id, signal);
      if (target === 'fake-model' || target === 'fake-github') {
        if (fixture.evidenceClass !== 'deterministic') return unsupported();
        const baseline = await driver.freshFixtureBaseline(target, signal);
        const previousGeneration = baselines.get(target); requireOwned(!previousGeneration || previousGeneration === baseline.generation);
        baselines.set(target, baseline.generation);
        return { complete: true, previous: { restorationPoint: baseline.restore, generation: baseline.generation },
          restoreState: { target, generation: baseline.generation, kind: baseline.restore.kind } };
      }
      if (target !== 'provider-default' || fixture.evidenceClass !== 'deterministic') return unsupported();
      const file = await configuration(signal);
      try {
        const bytes = await file.readFile('utf8');
        const value = z.object({ model: z.string(), provider: z.object({ aft: z.object({ npm: z.literal('@ai-sdk/openai-compatible'),
          options: z.object({ baseURL: z.string().regex(/^http:\/\/127\.0\.0\.1:[0-9]+\/v1$/), apiKey: z.literal('x') }).passthrough() }).passthrough() }).passthrough() }).passthrough().parse(JSON.parse(bytes));
        requireOwned(value.provider.aft.options.baseURL === `${await driver.fakeModelOrigin(signal)}/v1`);
        return { complete: true, previous: { model: value.model, harness: 'opencode' }, restoreState: { bytes } };
      } finally { await file.close(); }
    },
    async restore(id, target, state, signal) {
      identity(id);
      if (target === 'fake-model' || target === 'fake-github') {
        const saved = z.object({ target: z.enum(['fake-model','fake-github']), generation: z.string().min(1), kind: z.literal('startup-empty') }).strict().parse(state);
        requireOwned(saved.target === target && baselines.get(target) === saved.generation);
        await driver.resetFixtureBaseline(target, saved.generation, signal); return;
      }
      if (target !== 'provider-default') return unsupported();
      const saved = z.object({ bytes: z.string().max(65536) }).strict().parse(state);
      const file = await configuration(signal);
      try { await file.truncate(0); await file.writeFile(saved.bytes); } finally { await file.close(); }
    },
    async writeConfiguration(id, target, value, signal) {
      await verify(id, signal); if (target !== 'provider-default') return unsupported();
      z.object({ model: z.literal('aft/m'), harness: z.literal('opencode') }).strict().parse(value);
      const file = await configuration(signal);
      try {
        const current = Json.parse(JSON.parse(await file.readFile('utf8')));
        requireOwned(current && typeof current === 'object' && !Array.isArray(current));
        const provider = z.object({ provider: z.object({ aft: z.object({ options: z.object({ baseURL: z.string() }).passthrough() }).passthrough() }).passthrough() }).passthrough().parse(current);
        requireOwned(provider.provider.aft.options.baseURL === `${await driver.fakeModelOrigin(signal)}/v1`);
        const bytes = JSON.stringify({ ...current as Record<string, Json>, model: 'aft/m' });
        requireOwned(Buffer.byteLength(bytes) <= 65536);
        await file.truncate(0); await file.write(bytes, 0, 'utf8');
      } finally { await file.close(); }
    },
    enrollCleanup(id, cleanup) { identity(id); driver.enrollCleanup(cleanup); },
  };
  return access;
}

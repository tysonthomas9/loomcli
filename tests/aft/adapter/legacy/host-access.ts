import path from 'node:path';
import { constants } from 'node:fs';
import { lstat, realpath, open } from 'node:fs/promises';
import { z } from 'zod';
import type { OwnedFixture } from '../ownership.js';
import { privateFixtureDriver } from '../fixture/providers.js';
import { HostFixtureDriver } from '../fixture/host.js';
import { HttpResponse, Json } from '../protocol.js';
import { checkedCliPlan } from './cli-plan.js';
import { LegacyError, LegacyEvidenceClasses, type LegacyAccess, type LegacyLease } from './operations.js';
import { getFixtureOperationAuthority, type LoomAuthorizedOperation } from '../authority.js';
import { LegacyOperationEffects } from './effects.js';
import { requireOwnedWorkspace } from '../workspaces.js';

const Name = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/);
// These projections follow ops.WorkspaceData and domain.Agent, not AgentRow.
const Workspace = z.object({ success: z.literal(true), data: z.object({ id: Name, path: z.string(),
  repos: z.array(z.object({ name: Name, path: z.string() }).passthrough()) }).passthrough() }).passthrough();
const Agents = z.object({ success: z.literal(true), total: z.number().int().nonnegative(),
  data: z.array(z.object({ workspace_key: Name, name: Name, role_name: Name, updated_at: z.string().min(1) }).passthrough()) }).passthrough();
const Issues = z.object({ success: z.literal(true), data: z.array(z.object({ id: Name }).passthrough()).max(999) }).passthrough();
const Roles = z.array(z.object({ name: Name }).passthrough());
const unsupported = (): never => { throw new LegacyError('unsupported-capability', 'Fixture has no concrete owned hook for this legacy target'); };
const requireOwned = (ok: unknown): void => { if (!ok) throw new LegacyError('ownership-mismatch', 'Legacy fixture identity changed'); };

/** Root composition uses the canonical private driver; no second lease store. */
export function productionLegacyAccess(fixture: OwnedFixture): LegacyAccess {
  const driver = privateFixtureDriver(fixture);
  if (!(driver instanceof HostFixtureDriver)) return unsupported();
  return createHostLegacyAccess(fixture, driver);
}

/** Tests inject the actual HostFixtureDriver's process/HTTP seams. */
export function createHostLegacyAccess(fixture: OwnedFixture, driver: HostFixtureDriver): LegacyAccess {
  requireOwned(z.enum(LegacyEvidenceClasses).safeParse(fixture.evidenceClass).success && fixture.profile.startsWith('legacy-'));
  requireOwned(fixture.profile === 'legacy-deterministic' ? fixture.evidenceClass === 'deterministic' :
    fixture.profile.startsWith('legacy-real-') && fixture.evidenceClass === 'real-native');
  requireOwned(driver.workspaceRoot === fixture.repo && driver.configurationRoot === path.join(fixture.repo, '.loom-config'));
  let cached: LegacyLease | undefined;
  const identity = (id: string) => requireOwned(id === fixture.leaseId);
  const evidenceFor = (operation: LoomAuthorizedOperation) => {
    const expected = operation === 'loom.cli.task' && fixture.profile.startsWith('legacy-real-') ? 'live-provider' : fixture.evidenceClass;
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
      if (cached && Date.now() >= fixture.expiresAtUtcMs) return { ...cached, active: false };
      if (!operation) return unsupported();
      const evidence = evidenceFor(operation);
      await verify(id, signal);
      const records = fixture.ownedWorkspaces?.filter(row => row.identityKind === 'legacy-agent-name');
      requireOwned(records?.length);
      const cli = driver.cliRegistration;
      const metadata = await Promise.all(records!.map(async record => {
        const owned = requireOwnedWorkspace(fixture, record.workspaceId, undefined, 'legacy-agent-name');
        requireOwned(owned.repo.startsWith(driver.runtimeRoot + path.sep) && await realpath(owned.repo) === owned.repo);
        const prefix = `/api/workspaces/${encodeURIComponent(record.workspaceId)}`;
        const workspace = Workspace.parse(await api(prefix, signal)).data;
        requireOwned(workspace.id === record.workspaceId && workspace.path === owned.repo);
        const agents = Agents.parse(await api(`${prefix}/agents`, signal));
        requireOwned(agents.total === agents.data.length && agents.data.every(row => row.workspace_key === record.workspaceId) &&
          new Set(agents.data.map(row => row.name)).size === agents.data.length);
        const repos = await Promise.all(workspace.repos.map(async row => {
          requireOwned(row.path.startsWith(owned.repo + path.sep) || row.path === owned.repo);
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
        return { agents: agents.data.filter(row => record.agentIds.includes(row.name)).map(row => ({ workspaceId: row.workspace_key,
          id: row.name, name: row.name, generation: row.updated_at })), repos,
          roles: roles.map(row => ({ workspaceId: record.workspaceId, name: row.name })),
          issues: issues.map(row => ({ workspaceId: record.workspaceId, id: row.id })) };
      }));
      const serve = driver.processesById.get('serve'); requireOwned(serve);
      cached = { id, runId: fixture.runId, active: Date.now() < fixture.expiresAtUtcMs, evidence: fixture.evidenceClass,
        secrets: fixture.secrets, binary: cli.binary, cwd: cli.cwd, env: cli.env, workspaces: records!.map(row => row.workspaceId),
        agents: metadata.flatMap(row => row.agents), roles: metadata.flatMap(row => row.roles),
        issues: metadata.flatMap(row => row.issues), repos: metadata.flatMap(row => row.repos),
        processes: [{ id: 'serve', kind: 'serve', generation: serve!.generation, workspaceId: null, agentName: null, sessionName: null }],
        fixtures: fixture.profile === 'legacy-deterministic' ? ['provider-default'] : [] };
      return { ...structuredClone(cached), evidence };
    },
    async execute(id, command, signal) {
      await verify(id, signal); requireOwned(cached);
      const operation: LoomAuthorizedOperation = command.argv[0] === 'usage' ? 'loom.cli.usage' : command.argv[1] === 'seed-worktree' ? 'loom.fixture.seedWorktree' :
        command.argv[2] === 'role' ? 'loom.cli.role' : command.argv[4] === 'task' ? 'loom.cli.task' : unsupported();
      const evidence = evidenceFor(operation);
      const plan = checkedCliPlan({ ...cached!, evidence }, command);
      const workspaceId = command.argv[0] === 'usage' ? command.env.LOOM_WORKSPACE_ID! : command.argv[1] === 'seed-worktree' ? command.argv[3]! : command.argv[1]!;
      const actor = operation === 'loom.cli.task' ? command.argv[5] : operation === 'loom.cli.usage' ? command.argv[4] :
        operation === 'loom.fixture.seedWorktree' ? command.argv[5] : undefined;
      requireOwnedWorkspace(fixture, workspaceId, actor, 'legacy-agent-name');
      if (command.argv[4] === 'task' && fixture.profile.startsWith('legacy-real-') &&
        (evidence !== 'live-provider' || command.argv[3] !== fixture.profile.slice('legacy-real-'.length))) return unsupported();
      const result = await driver.launchOwnedCli(plan.argv, plan.envOverrides, plan.stdin, plan.waitForExit, signal);
      const registered = await driver.inspectOwnedProcess(result.id, result.generation, signal);
      requireOwned(registered.pid === result.pid);
      return { processId: result.id, generation: result.generation, ...result.completion };
    },
    async registerProcess(id, result, signal) { await verify(id, signal); await driver.inspectOwnedProcess(result.processId, result.generation, signal); },
    async stimulate(id, process, operation, request, signal) {
      await verify(id, signal);
      if (operation !== 'serve-restart' || process.id !== 'serve' || process.kind !== 'serve' || request !== null) return unsupported();
      getFixtureOperationAuthority(fixture, 'loom.runtime.stimulate', ['stop-owned-process','restart-owned-service']);
      const transition = await driver.restartOwnedProcess(process.id, process.generation, signal);
      return { transition, response: null };
    },
    async request() { return unsupported(); },
    async validateSeedPath() { return unsupported(); },
    async seedCommit() { return unsupported(); },
    async snapshot(id, target, signal) {
      await verify(id, signal);
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
      identity(id); if (target !== 'provider-default') return unsupported();
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

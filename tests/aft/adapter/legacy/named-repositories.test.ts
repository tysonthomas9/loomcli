import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, realpath, rm } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { CapabilityRegistry, calculateImplementationPin, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { createEvidenceStore, putEvidenceStore } from '../evidence.js';
import { createFixtureOperationAuthority } from '../authority.js';
import { putFixture, type OwnedFixture } from '../ownership.js';
import { createOwnedWorkspaceRoster } from '../workspaces.js';
import { ObservationError } from '../protocol.js';
import { createLegacyProviders, LegacyOperationEffects } from './providers.js';
import type { LegacyAccess, LegacyLease, CliCommand } from './operations.js';

// Canonical registry/retained-topology evidence with an injected CLI transport.
// This does not provision or prove a production multi-repository host actor.
test('legacy workspace and usage actors preserve named topology without choosing a repository', async t => {
  const registry = new CapabilityRegistry();
  const pin = calculateImplementationPin(fileURLToPath(new URL('..', import.meta.url)),
    ['legacy/providers.ts', 'legacy/operations.ts', 'workspaces.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  let factories = 0, enrolled = 0;
  const commands: CliCommand[] = [];
  const unused = async (): Promise<never> => { throw new Error('Unused test transport'); };
  const lease: LegacyLease = { id: 'named-lease', runId: 'named-run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/injected/loom', cwd: '/injected/launch', env: {}, workspaces: ['WS'],
    agents: ['all', 'beta-only', 'child'].map(name => ({ workspaceId: 'WS', id: name, name, generation: 'legacy-row-generation' })),
    roles: [], issues: [], processes: [], fixtures: [],
    repos: [{ workspaceId: 'WS', name: 'alpha', sourcePath: '/owned/alpha' },
      { workspaceId: 'WS', name: 'beta', sourcePath: '/owned/beta' }] };
  const access: LegacyAccess = { lease: async () => lease,
    execute: async (_id, command) => {
      commands.push(structuredClone(command));
      return command.argv[4] === 'task'
        ? { processId: 'task-process', generation: 'task-generation', exitCode: null, complete: false, stdout: '', stderr: '' }
        : { processId: 'read-process', generation: 'read-generation', exitCode: 0, complete: true,
          stdout: command.argv[0] === 'usage' ? '{"total":7}' : '[]', stderr: '' };
    }, registerProcess: async () => { enrolled++; }, stimulate: unused, request: unused,
    validateSeedPath: unused, seedCommit: unused, snapshot: unused, restore: unused,
    writeConfiguration: unused, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => { factories++; return access; })) registry.register(provider);
  const context = createCapabilityContext({ file: 'named-repository-protocol.yaml', line: 1 }, registry);
  Object.assign(context, { runId: lease.runId });
  const directory = await mkdtemp(fileURLToPath(new URL('.seed-test-named-', import.meta.url)));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const store = await createEvidenceStore(await realpath(directory)); putEvidenceStore(context, store);
  const fixture: OwnedFixture = { leaseId: lease.id, runId: lease.runId, caseId: context.caseId,
    suiteId: context.suiteId, scope: context.scope, workspaceId: 'WS', repo: '/owned/alpha',
    profile: 'legacy-deterministic', evidenceClass: 'deterministic', expiresAtUtcMs: Number.MAX_SAFE_INTEGER,
    roots: new Map(), agents: new Map(), secrets: [], readApi: unused, readFiles: unused, resolveAgent: unused,
    verify: async () => {}, dispose: async () => {} };
  const owner = { leaseId: fixture.leaseId, runId: fixture.runId, caseId: fixture.caseId,
    suiteId: fixture.suiteId, scope: fixture.scope, profile: fixture.profile };
  fixture.operationAuthority = createFixtureOperationAuthority(owner, Object.fromEntries(Object.entries(LegacyOperationEffects)
    .map(([operation, effects]) => [operation, { evidenceClass: 'deterministic' as const, effects: [...effects] }])));
  const topology = { identityKind: 'legacy-agent-name' as const, workspaceId: 'WS', repo: '/owned/alpha',
    commonDir: '/owned/alpha/.git', storeId: 'owned-store', storeGeneration: 'captured-store-generation',
    agentIds: ['all', 'beta-only'],
    repositories: [
      { repoName: 'alpha', sourceRepoId: 'source-alpha', repo: '/owned/alpha', commonDir: '/owned/alpha/.git', groups: ['frontend'] },
      { repoName: 'beta', sourceRepoId: 'source-beta', repo: '/owned/beta', commonDir: '/owned/beta/.git', groups: ['backend'] },
    ], agentSources: [{ agentId: 'all', repoNames: ['alpha', 'beta'] }, { agentId: 'beta-only', repoNames: ['beta'] }] };
  const creationReceipt = await store.retain(JSON.stringify({ kind: 'workspace-created', ...owner, ...topology }));
  fixture.ownedWorkspaces = await createOwnedWorkspaceRoster(owner, [{ ...topology, creationReceipt }], store);
  let childGroups: string[] = [];
  fixture.readWorkspaceLegacyAgent = async (workspaceId, name) => {
    assert.equal(workspaceId, 'WS');
    if (name !== 'all' && name !== 'child') throw new ObservationError('ownership-mismatch', 'Private actor is absent');
    const repo = name === 'all' ? '/owned/alpha' : '/owned/beta';
    return { kind: 'legacy-agent-enrolled', identityKind: 'legacy-agent-name',
      ...owner, workspaceId, name, repo, commonDir: `${repo}/.git`, storeId: 'owned-store',
      storeGeneration: 'captured-store-generation', parentName: name === 'all' ? null : 'all',
      createdAt: '2026-10-09T00:00:00Z', updatedAt: '2026-10-09T00:01:00Z',
      assignedRepos: name === 'all' || childGroups.length ? [] : ['beta'], assignedRepoGroups: name === 'all' ? [] : childGroups };
  };
  putFixture(context, fixture);
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  const usage = (name: string, workspaceId = 'WS') => invoke('loom.cli.usage', {
    agent: { fixtureLeaseId: lease.id, workspaceId, agentId: name } });
  const task = (agentName: string, repoName: string | null) => invoke('loom.cli.task', {
    leaseId: lease.id, workspaceId: 'WS', agentName, backend: 'codex', mode: 'once', issueId: null, repoName });
  for (const request of [
    () => usage('all', 'FOREIGN'),
    () => usage('foreign-actor'),
    () => task('beta-only', 'alpha'),
    () => task('beta-only', 'missing'),
    () => invoke('loom.fixture.seedWorktree', { leaseId: lease.id, workspaceId: 'WS', agentName: 'all',
      relativePath: 'marker.txt', content: 'fixture', commitMessage: 'fixture only' }),
  ]) {
    const result = await request(); assert.equal(result.availability, 'error');
    assert.equal(result.error?.code, 'ownership-mismatch');
    assert.equal(factories, 0); assert.equal(commands.length, 0);
  }
  childGroups = ['frontend'];
  const changedGroups = await usage('child');
  assert.equal(changedGroups.availability, 'error'); assert.equal(changedGroups.error?.code, 'ownership-mismatch');
  assert.equal(factories, 0); assert.equal(commands.length, 0);
  childGroups = [];
  const role = await invoke('loom.cli.role', { leaseId: lease.id, workspaceId: 'WS', operation: 'list', name: null });
  assert.equal(role.availability, 'observed', JSON.stringify(role));
  const allUsage = await usage('all'); assert.equal(allUsage.availability, 'observed', JSON.stringify(allUsage));
  const childUsage = await usage('child'); assert.equal(childUsage.availability, 'observed', JSON.stringify(childUsage));
  assert.equal(fixture.ownedWorkspaces![0]!.creationReceipt.id, creationReceipt.id);
  assert.deepEqual(fixture.ownedWorkspaces![0]!.agentSources!.at(-1), { agentId: 'child', repoNames: ['beta'] });
  assert.equal((await task('all', null)).availability, 'observed');
  assert.equal((await task('beta-only', 'beta')).availability, 'observed');
  assert.equal(factories, 1); assert.equal(enrolled, 2);
  assert.deepEqual(commands.map(command => ({ argv: command.argv, env: command.env })), [
    { argv: ['--workspace', 'WS', 'role', 'list', '--json'], env: {} },
    { argv: ['usage', '--format', 'json', '--agent', 'all'], env: { LOOM_WORKSPACE_ID: 'WS' } },
    { argv: ['usage', '--format', 'json', '--agent', 'child'], env: { LOOM_WORKSPACE_ID: 'WS' } },
    { argv: ['--workspace', 'WS', '--backend', 'codex', 'task', 'all'],
      env: { LOOM_WORKSPACE_ID: 'WS', LOOM_ASSIGNED_TASK_ID: '', LOOM_SOURCE_REPOS: '' } },
    { argv: ['--workspace', 'WS', '--backend', 'codex', 'task', 'beta-only'],
      env: { LOOM_WORKSPACE_ID: 'WS', LOOM_ASSIGNED_TASK_ID: '', LOOM_SOURCE_REPOS: '/owned/beta' } },
  ]);
});

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { checkedCliPlan } from './cli-plan.js';
import { createLegacyOperations, LegacyError, type LegacyAccess, type LegacyLease, type CliCommand } from './operations.js';

test('fixture CLI boundary admits only generated role, usage, task and seed actor plans', async () => {
  const lease: LegacyLease = { id: 'lease', runId: 'run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/owned/loom', cwd: '/owned/work', env: { PATH: '/owned/bin' }, workspaces: ['WS'],
    agents: [{ workspaceId: 'WS', id: 'agt_1', name: 'worker', generation: 'g' }], roles: [{ workspaceId: 'WS', name: 'lead' }],
    issues: [{ workspaceId: 'WS', id: 'issue' }], repos: [{ workspaceId: 'WS', name: 'repo', sourcePath: '/owned/source' }], processes: [], fixtures: [] };
  const commands: CliCommand[] = [];
  const unused = async (): Promise<never> => { throw new Error('Unused fixture hook'); };
  const access: LegacyAccess = { lease: async () => lease, execute: async (_lease, command) => {
    checkedCliPlan(lease, command); commands.push(command);
    return { processId: 'p', generation: 'g', complete: true, exitCode: 0,
      stdout: command.argv[1] === 'seed-worktree' ? 'seeded worktree: ws=WS agent=worker repos=repo\n' : '[]', stderr: '' };
  }, registerProcess: async () => {}, stimulate: unused, request: unused, validateSeedPath: async () => {}, seedCommit: async () => 'a'.repeat(40),
  snapshot: unused, restore: unused, writeConfiguration: unused, enrollCleanup: () => {} };
  const ops = createLegacyOperations(access); let sequence = 0;
  const call = () => ({ runId: 'run', invocationId: String(sequence++), signal: new AbortController().signal });
  await ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, call());
  await ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'show', name: 'lead' }, call());
  await ops.usage({ agent: { fixtureLeaseId: 'lease', workspaceId: 'WS', agentId: 'agt_1' } }, call());
  for (const backend of ['codex', 'claude', 'cursor', 'opencode']) for (const mode of ['once', 'auto', 'daemon']) {
    await ops.task({ leaseId: 'lease', workspaceId: 'WS', agentName: 'worker', backend, mode, repoName: 'repo', issueId: mode === 'daemon' ? 'issue' : null }, call());
    assert.equal(checkedCliPlan(lease, commands.at(-1)!).waitForExit, false);
  }
  await ops.seedWorktree({ leaseId: 'lease', workspaceId: 'WS', agentName: 'worker', relativePath: 'diff.txt', content: 'exact\nbytes', commitMessage: 'seed' }, call());
  assert.equal(checkedCliPlan(lease, commands.at(-1)!).stdin, 'exact\nbytes');
  const role = commands[0]!;
  for (const patch of [{ binary: '/foreign/loom' }, { cwd: '/foreign' }, { argv: ['data', 'delete', '--all'] },
    { argv: [...role.argv, '--config', '/foreign'] }, { env: { ...role.env, NODE_OPTIONS: '--import=foreign' } },
    { stdin: 'unexpected' }, { argv: ['--workspace', 'FOREIGN', 'role', 'list', '--json'] }]) {
    assert.throws(() => checkedCliPlan(lease, { ...role, ...patch }), error => error instanceof LegacyError && error.code === 'invalid-input');
  }
  const task = commands[3]!;
  assert.throws(() => checkedCliPlan(lease, { ...task, env: { ...task.env, LOOM_ASSIGNED_TASK_ID: 'foreign' } }), LegacyError);
});

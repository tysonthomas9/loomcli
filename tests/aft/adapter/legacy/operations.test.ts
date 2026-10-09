import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createLegacyOperations, LegacyError, type LegacyLease, type LegacyAccess, type Invocation,
  type CliCommand, type ProcessResult } from './operations.js';
import { scenarioCatalog as catalog } from './catalog.js';
import type { Json } from '../protocol.js';

function setup(overrides: Partial<LegacyAccess> = {}) {
  const lease: LegacyLease = { id: 'lease', runId: 'run', active: true, evidence: 'deterministic', secrets: ['private-credential'],
    binary: '/owned/bin/loom', cwd: '/owned/work', env: { LOOM_CONFIG_DIR: '/owned/config' },
    workspaces: ['WS'], agents: [{ workspaceId: 'WS', id: 'agt_1', name: 'worker', generation: 'agent-gen' }],
    roles: [{ workspaceId: 'WS', name: 'lead' }], issues: [{ workspaceId: 'WS', id: 'issue-1' }],
    repos: [{ workspaceId: 'WS', name: 'repo-a', sourcePath: '/owned/source/repo-a' }],
    processes: [
      { id: 'serve', generation: 'gen-1', kind: 'serve', workspaceId: null, agentName: null, sessionName: null },
      { id: 'harness', generation: 'gen-1', kind: 'harness', workspaceId: null, agentName: null, sessionName: null },
      { id: 'worker', generation: 'gen-1', kind: 'worker', workspaceId: 'WS', agentName: 'worker', sessionName: null },
      { id: 'terminal', generation: 'gen-1', kind: 'terminal', workspaceId: 'WS', agentName: 'worker', sessionName: 'tab-1' },
    ], fixtures: ['fake-model', 'fake-github', 'scripted-backend', 'provider-default', 'workspace:WS', 'legacy-e2e-repo'] };
  const commands: CliCommand[] = []; const requests: unknown[] = []; const transitions: unknown[] = [];
  const writes: unknown[] = []; const cleanups: (() => Promise<void>)[] = []; const restores: unknown[] = []; const registered: unknown[] = [];
  let result: ProcessResult = { processId: 'owned-task', generation: 'task-gen', exitCode: 0, complete: true, stdout: '{"name":"lead"}', stderr: '' };
  const access: LegacyAccess = {
    lease: async () => lease,
    execute: async (_id, command) => { commands.push(command); return result; },
    registerProcess: async (_id, process) => { registered.push(process); },
    stimulate: async (_id, target, operation, request) => {
      transitions.push({ target, operation, request });
      return { transition: { beforeGeneration: target.generation, afterGeneration: operation === 'serve-restart' ? 'gen-2' : null,
        affectedIds: [target.id], complete: true }, response: request ? { status: 200, body: { success: true, data: { message: 'requested' } } } : null };
    },
    request: async (id, target, method, path, body) => {
      requests.push({ id, target, method, path, body });
      const responseBody: Json = path === '/__script' ? { queued: (body as { steps: Json[] }).steps.length } :
        path === '/__fixture' ? { ok: true, pr: (body as { pr: Json }).pr, files: (body as { files: Json[] }).files.length } : { ok: true };
      return { status: 200, body: responseBody };
    },
    validateSeedPath: async () => {}, seedCommit: async () => 'a'.repeat(40),
    snapshot: async () => ({ complete: true, previous: { model: 'before' }, restoreState: { model: 'before' } }),
    restore: async (id, target, state) => { restores.push({ id, target, state }); },
    writeConfiguration: async (id, target, value) => { writes.push({ id, target, value }); },
    enrollCleanup: (_id, cleanup) => { cleanups.push(cleanup); }, ...overrides,
  };
  let n = 0;
  const call = (): Invocation => ({ runId: 'run', invocationId: `inv-${n++}`, signal: new AbortController().signal });
  return { lease, access, ops: createLegacyOperations(access), call, commands, requests, transitions, writes, cleanups, restores, registered,
    setResult: (value: ProcessResult) => { result = value; } };
}
const reject = (code: LegacyError['code']) => (error: unknown) => error instanceof LegacyError && error.code === code;
const task = { leaseId: 'lease', workspaceId: 'WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null };
const seed = { leaseId: 'lease', workspaceId: 'WS', agentName: 'worker', relativePath: 'aft-diff.txt', content: 'AFT-DIFF-MARKER\n', commitMessage: 'aft diff' };

test('role show and list retain the original CLI actor and JSON body', async () => {
  const s = setup();
  const shown = await s.ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'show', name: 'lead' }, s.call());
  assert.deepEqual(shown.body, { name: 'lead' });
  await s.ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, s.call());
  assert.deepEqual(s.commands.map(c => c.argv), [['--workspace', 'WS', 'role', 'show', 'lead', '--json'], ['--workspace', 'WS', 'role', 'list', '--json']]);
  assert.equal(s.commands[0]?.binary, '/owned/bin/loom'); assert.equal(s.commands[0]?.env.LOOM_CONFIG_DIR, '/owned/config');
});
test('usage resolves owned Agent ID to its exact CLI name and uses --format json', async () => {
  const s = setup(); await s.ops.usage({ agent: { fixtureLeaseId: 'lease', workspaceId: 'WS', agentId: 'agt_1' } }, s.call());
  assert.deepEqual(s.commands[0]?.argv, ['usage', '--format', 'json', '--agent', 'worker']);
});
test('task honors each requested backend, typed repo scope, and daemon assigned issue', async () => {
  const s = setup();
  for (const backend of ['codex', 'claude', 'cursor', 'opencode']) await s.ops.task({ ...task, backend }, s.call());
  for (const [i, backend] of ['codex', 'claude', 'cursor', 'opencode'].entries()) assert.deepEqual(s.commands[i]?.argv,
    ['--workspace', 'WS', '--backend', backend, 'task', 'worker']);
  await s.ops.task({ ...task, mode: 'auto', repoName: 'repo-a' }, s.call());
  assert.equal(s.commands[4]?.env.LOOM_SOURCE_REPOS, '/owned/source/repo-a');
  const result = await s.ops.task({ ...task, mode: 'daemon', issueId: 'issue-1' }, s.call());
  assert.deepEqual(s.commands[5]?.argv.slice(-2), ['--auto', '--daemon-mode']);
  assert.equal(s.commands[5]?.env.LOOM_ASSIGNED_TASK_ID, 'issue-1'); assert.equal(result.ownedProcessId, 'owned-task');
  assert.equal(s.registered.length, 6);
});
test('mutation invocation is consumed once, including after a failed transport', async () => {
  let attempts = 0; const s = setup({ execute: async () => { attempts++; throw new Error('private-credential'); } });
  const call = s.call(); await assert.rejects(s.ops.task(task, call), reject('process-failed'));
  await assert.rejects(s.ops.task(task, call), reject('mutation-repeated')); assert.equal(attempts, 1);
});
test('runtime stop may have no replacement; owned lifecycle actor is fixed and occurs once', async () => {
  const s = setup();
  for (const [targetId, operation] of [['harness', 'harness-restart'], ['serve', 'serve-restart'], ['worker', 'worker-stop'], ['terminal', 'terminal-close']]) {
    const call = s.call(); const result = await s.ops.stimulate({ leaseId: 'lease', targetId, operation, expectedGeneration: 'gen-1' }, call);
    assert.equal(result.afterGeneration, targetId === 'serve' ? 'gen-2' : null);
    await assert.rejects(s.ops.stimulate({ leaseId: 'lease', targetId, operation, expectedGeneration: 'gen-1' }, call), reject('mutation-repeated'));
  }
  assert.equal(s.transitions.length, 4); assert.equal(s.requests.length, 0);
  assert.deepEqual((s.transitions[2] as { request: unknown }).request, { method: 'POST', path: '/api/workspaces/WS/agents/worker/stop', body: null });
  assert.deepEqual((s.transitions[3] as { request: unknown }).request, { method: 'DELETE', path: '/api/workspaces/WS/terminal/tabs/tab-1', body: null });
});
test('foreign, stale, mismatched and unsafe inputs cause no transport mutations', async () => {
  const s = setup();
  for (const patch of [{ agentName: '-x' }, { agentName: '../worker' }, { workspaceId: 'WS;echo' }, { backend: 'shell' },
    { command: 'anything' }, { issueId: 'issue-1' }, { mode: 'daemon', issueId: null }]) await assert.rejects(s.ops.task({ ...task, ...patch }, s.call()), reject('invalid-input'));
  for (const patch of [{ agentName: 'foreign' }, { workspaceId: 'OTHER' }, { repoName: 'foreign' }, { mode: 'daemon', issueId: 'foreign' }])
    await assert.rejects(s.ops.task({ ...task, ...patch }, s.call()), reject('ownership-mismatch'));
  await assert.rejects(s.ops.usage({ agent: { fixtureLeaseId: 'lease', workspaceId: 'WS', agentId: 'foreign' } }, s.call()), reject('ownership-mismatch'));
  await assert.rejects(s.ops.stimulate({ leaseId: 'lease', targetId: 'foreign', operation: 'serve-restart', expectedGeneration: 'gen-1' }, s.call()), reject('ownership-mismatch'));
  await assert.rejects(s.ops.stimulate({ leaseId: 'lease', targetId: 'serve', operation: 'serve-restart', expectedGeneration: 'old' }, s.call()), reject('stale-generation'));
  await assert.rejects(s.ops.stimulate({ leaseId: 'lease', targetId: 'worker', operation: 'serve-restart', expectedGeneration: 'gen-1' }, s.call()), reject('ownership-mismatch'));
  assert.equal(s.commands.length + s.transitions.length, 0);
});
test('inactive and foreign run leases fail before any action', async () => {
  const s = setup(); s.lease.active = false;
  await assert.rejects(s.ops.task(task, s.call()), reject('ownership-mismatch'));
  s.lease.active = true; s.lease.runId = 'other';
  await assert.rejects(s.ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, s.call()), reject('ownership-mismatch'));
  assert.equal(s.commands.length, 0);
});
test('seed uses stdin and TESTSUPPORT through CLI; it never claims actor activity', async () => {
  const s = setup(); s.setResult({ processId: 'seed', generation: 'seed-gen', exitCode: 0, complete: true,
    stdout: 'seeded worktree: ws=WS agent=worker repos=repo-a\n', stderr: '' });
  const result = await s.ops.seedWorktree(seed, s.call());
  assert.equal(s.commands[0]?.stdin, seed.content); assert.equal(s.commands[0]?.env.LOOM_TESTSUPPORT, '1');
  assert.deepEqual(s.commands[0]?.argv, ['daemon', 'seed-worktree', '--workspace', 'WS', '--agent', 'worker', '--file', 'aft-diff.txt', '--content', '-', '--message', 'aft diff']);
  assert.deepEqual(result.receipt.facts, { commit: 'a'.repeat(40), actorActivity: false });
});
test('seed rejects traversal, Git internals, options, controls and symlink escapes', async () => {
  const s = setup();
  for (const relativePath of ['../escape', '/absolute', 'a/../b', 'a\\b', 'a//b', '.git/config', 'a/.git/index', '-option', 'a/-option', 'a\nfile', 'C:/escape'])
    await assert.rejects(s.ops.seedWorktree({ ...seed, relativePath }, s.call()), reject('invalid-input'));
  const escaped = setup({ validateSeedPath: async () => { throw new LegacyError('ownership-mismatch', 'Symlink escape'); } });
  await assert.rejects(escaped.ops.seedWorktree(seed, escaped.call()), reject('ownership-mismatch')); assert.equal(escaped.commands.length, 0);
  s.lease.evidence = 'live'; await assert.rejects(s.ops.seedWorktree(seed, s.call()), reject('unsupported-capability')); assert.equal(s.commands.length, 0);
});
test('every finite source-grounded model and backend stimulus uses exact protocol bytes', async () => {
  const s = setup();
  for (const entry of catalog.entries) {
    const parameters: Record<string, string> = {};
    for (const key of entry.parameters ?? []) parameters[key] = key === 'headSha' ? '1'.repeat(40) : key === 'baseSha' ? '0'.repeat(40) :
      (entry.agentPrefixes?.[key as 'agentName' | 'secondAgentName'] ?? '') + s.lease.runId;
    await s.ops.configure({ leaseId: 'lease', setting: `${entry.fixtureId}-scenario`, fixtureId: entry.fixtureId,
      scenarioId: entry.id, parameters, agentId: entry.fixtureId === 'scripted-backend' ? 'agt_1' : null }, s.call());
    if (entry.fixtureId === 'scripted-backend') {
      const last = s.writes.at(-1) as { value: { turns: unknown[] } };
      assert.deepEqual(last.value.turns, entry.reset ? [] : Array.isArray(entry.payload) ? entry.payload : [entry.payload]);
    } else if (!entry.parameters?.length) assert.deepEqual((s.requests.at(-1) as { body: unknown }).body, entry.payload);
  }
  assert.equal(s.cleanups.length, 3);
});
test('child-create fixture names are bounded to the run before any protocol request', async () => {
  const s = setup();
  await assert.rejects(s.ops.configure({ leaseId: 'lease', setting: 'fake-model-scenario', fixtureId: 'fake-model',
    scenarioId: 'fake-model-two-owned-children', parameters: { agentName: 'agv1-cl2-a-foreign', secondAgentName: 'agv1-cl2-b-run' } }, s.call()), reject('invalid-input'));
  assert.equal(s.requests.length, 0);
});

test('configuration captures first state, enrolls restoration before mutation and restores after expiry', async () => {
  const s = setup();
  const input = { leaseId: 'lease', setting: 'provider-default', model: 'aft/m', harness: 'opencode' };
  await s.ops.configure(input, s.call()); await s.ops.configure(input, s.call()); assert.equal(s.cleanups.length, 1);
  s.lease.active = false; await s.cleanups[0]!();
  assert.deepEqual(s.restores, [{ id: 'lease', target: 'provider-default', state: { model: 'before' } }]);
});
test('unknown settings, scenario IDs, cross-fixture scenarios and extra parameters fail before mutation', async () => {
  const s = setup(); const input = { leaseId: 'lease', setting: 'fake-model-scenario', fixtureId: 'fake-model', scenarioId: 'fake-model-reset', parameters: {} };
  for (const patch of [{ setting: 'arbitrary' }, { scenarioId: 'unknown' }, { parameters: { command: 'echo' } }, { code: 'evil' }])
    await assert.rejects(s.ops.configure({ ...input, ...patch }, s.call()), reject('invalid-input'));
  await assert.rejects(s.ops.configure({ ...input, scenarioId: 'fake-github-reset' }, s.call()), reject('invalid-input'));
  await assert.rejects(s.ops.configure({ ...input, parameters: { headSha: 'a'.repeat(40) } }, s.call()), reject('invalid-input'));
  assert.equal(s.requests.length + s.writes.length + s.cleanups.length, 0);
});
test('incomplete snapshots, malformed CLI JSON and failed process/HTTP observations never prove absence', async () => {
  const s = setup(); s.setResult({ processId: 'p', generation: 'g', exitCode: 0, complete: true, stdout: '{', stderr: '' });
  await assert.rejects(s.ops.role({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, s.call()), reject('response-invalid'));
  s.setResult({ processId: 'p', generation: 'g', exitCode: 1, complete: true, stdout: '[]', stderr: '' });
  await assert.rejects(s.ops.usage({ agent: { fixtureLeaseId: 'lease', workspaceId: 'WS', agentId: 'agt_1' } }, s.call()), reject('process-failed'));
  const snapshot = setup({ snapshot: async () => ({ complete: false } as never) });
  await assert.rejects(snapshot.ops.configure({ leaseId: 'lease', setting: 'fake-model-scenario', fixtureId: 'fake-model', scenarioId: 'fake-model-reset', parameters: {} }, snapshot.call()), reject('response-invalid'));
  assert.equal(snapshot.requests.length + snapshot.cleanups.length, 0);
  const http = setup({ request: async () => ({ status: 503, body: [] }) });
  await assert.rejects(http.ops.configure({ leaseId: 'lease', setting: 'fake-model-scenario', fixtureId: 'fake-model', scenarioId: 'fake-model-reset', parameters: {} }, http.call()), reject('response-invalid'));
  assert.equal(http.cleanups.length, 1);
});
test('runtime refuses incomplete, foreign or unchanged transitions', async () => {
  for (const transition of [{ beforeGeneration: 'gen-1', afterGeneration: null, affectedIds: ['harness'], complete: false },
    { beforeGeneration: 'gen-1', afterGeneration: 'gen-1', affectedIds: ['harness'], complete: true },
    { beforeGeneration: 'gen-1', afterGeneration: null, affectedIds: ['foreign'], complete: true }]) {
    const s = setup({ stimulate: async () => ({ transition, response: null } as never) });
    await assert.rejects(s.ops.stimulate({ leaseId: 'lease', targetId: 'harness', operation: 'harness-restart', expectedGeneration: 'gen-1' }, s.call()), reject('response-invalid'));
  }
});
test('cancelled operations and stale transport generations do not produce successful facts', async () => {
  const s = setup(); const controller = new AbortController(); controller.abort();
  await assert.rejects(s.ops.task(task, { ...s.call(), signal: controller.signal })); assert.equal(s.commands.length, 0);
  const stale = setup({ stimulate: async () => { throw new LegacyError('stale-generation', 'Changed immediately before mutation'); } });
  await assert.rejects(stale.ops.stimulate({ leaseId: 'lease', targetId: 'harness', operation: 'harness-restart', expectedGeneration: 'gen-1' }, stale.call()), reject('stale-generation'));
});
test('running task ownership is retained and contradictory process completion is refused', async () => {
  const s = setup(); s.setResult({ processId: 'owned-task', generation: 'gen', exitCode: null, complete: false, stdout: '', stderr: '' });
  const observed = await s.ops.task({ ...task, mode: 'auto' }, s.call());
  assert.equal(observed.exitCode, null); assert.equal(observed.complete, false); assert.equal(s.registered.length, 1);
  s.setResult({ processId: 'owned-task', generation: 'gen', exitCode: 0, complete: false, stdout: '', stderr: '' });
  await assert.rejects(s.ops.task(task, s.call()), reject('response-invalid'));
});
test('configuration ownership, acknowledgments and restoration fail closed', async () => {
  const input = { leaseId: 'lease', setting: 'fake-model-scenario', fixtureId: 'fake-model', scenarioId: 'fake-model-reset', parameters: {} };
  const malformed = setup({ request: async () => ({ status: 200, body: [] }) });
  await assert.rejects(malformed.ops.configure(input, malformed.call()), reject('response-invalid'));
  const forge = setup({ request: async () => ({ status: 200, body: { ok: true, pr: {}, files: 1 } }) });
  await assert.rejects(forge.ops.configure({ ...input, fixtureId: 'fake-github', setting: 'fake-github-scenario', scenarioId: 'fake-github-review-widget',
    parameters: { headSha: 'a'.repeat(40), baseSha: 'b'.repeat(40) } }, forge.call()), reject('response-invalid'));
  const s = setup(); await s.ops.configure(input, s.call()); s.lease.runId = 'foreign';
  await assert.rejects(s.cleanups[0]!(), reject('cleanup-failed')); assert.equal(s.restores.length, 0);
  const live = setup(); live.lease.evidence = 'live'; await assert.rejects(live.ops.configure(input, live.call()), reject('unsupported-capability'));
  assert.equal(live.requests.length, 0);
});
test('configuration mutations serialize without sleeps and restoration is enrolled before writes', async () => {
  let release!: () => void; const held = new Promise<void>(resolve => { release = resolve; });
  let snapshotStarted!: () => void; const started = new Promise<void>(resolve => { snapshotStarted = resolve; });
  const s = setup({ snapshot: async () => { snapshotStarted(); await held; return { complete: true, previous: null, restoreState: null }; } });
  const input = { leaseId: 'lease', setting: 'provider-default', model: 'aft/m', harness: 'opencode' };
  const pending = s.ops.configure(input, s.call()); await started;
  await assert.rejects(s.ops.configure(input, s.call()), reject('mutation-repeated')); release(); await pending;
  assert.equal(s.writes.length, 1); assert.equal(s.cleanups.length, 1);
  const noCleanup = setup({ enrollCleanup: () => { throw new LegacyError('cleanup-failed', 'Cleanup enrollment failed'); } });
  await assert.rejects(noCleanup.ops.configure(input, noCleanup.call()), reject('cleanup-failed')); assert.equal(noCleanup.writes.length, 0);
});

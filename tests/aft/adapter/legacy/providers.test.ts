import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { mkdtemp, rm, realpath, readFile } from 'node:fs/promises';
import { createEvidenceStore, putEvidenceStore } from '../evidence.js';
import { CapabilityRegistry, calculateImplementationPin, createCapabilityContext, getRegisteredResource,
  revokeCapabilityContext, type CapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ObservationResultSchema } from '@tysonthomas9/aft/types';
import { putFixture, disposeFixtures, type OwnedFixture } from '../ownership.js';
import { createLegacyProviders, RoleOutput } from './providers.js';
import type { LegacyAccess, LegacyLease } from './operations.js';
import { createFixtureOperationAuthority } from '../authority.js';
import { LegacyOperationEffects } from './providers.js';
import { testLegacyRoster } from './test-roster.js';

test('all six legacy providers register strict contracts and return canonical envelopes', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts', 'legacy/operations.ts', 'legacy/catalog.ts',
    'legacy/scenarios.json', 'protocol.ts', 'operation.ts', 'ownership.ts', 'evidence.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); let executed = 0; let verified = 0; let factories = 0;
  const lease: LegacyLease = { id: 'lease', runId: 'run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/owned/bin/loom', cwd: '/owned/work', env: {}, workspaces: ['WS'], agents: [], roles: [], issues: [], repos: [], processes: [], fixtures: [] };
  const unsupported = async (): Promise<never> => { throw new Error('Unused transport'); };
  const access: LegacyAccess = { lease: async () => lease,
    execute: async () => { executed++; return { processId: 'cli', generation: 'gen', complete: true, exitCode: 0, stdout: '[]', stderr: '' }; },
    registerProcess: unsupported, stimulate: unsupported, request: unsupported, validateSeedPath: unsupported,
    seedCommit: unsupported, snapshot: unsupported, restore: unsupported, writeConfiguration: unsupported, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => { factories++; return access; })) registry.register(provider);
  for (const id of ['loom.cli.role', 'loom.cli.usage', 'loom.cli.task', 'loom.runtime.stimulate', 'loom.fixture.seedWorktree', 'loom.fixture.configure'])
    assert.equal(registry.get(id, 1).retry, 'never');
  const context = createCapabilityContext({ file: 'test.yaml', line: 1 }, registry);
  Object.assign(context, { runId: 'run', caseId: 'case' });
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true }));
  const evidenceStore = await createEvidenceStore(evidenceRoot); putEvidenceStore(context, evidenceStore);
  const fixture: OwnedFixture = { leaseId: 'lease', runId: 'run', caseId: 'case', suiteId: context.suiteId, scope: context.scope, workspaceId: 'WS', repo: '/owned/source', profile: 'legacy',
    expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic', roots: new Map(), agents: new Map(), secrets: [],
    readApi: unsupported, readFiles: unsupported, resolveAgent: unsupported, verify: async () => { verified++; }, dispose: async () => {} };
  fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: fixture.leaseId, runId: fixture.runId, suiteId: fixture.suiteId, scope: fixture.scope, caseId: fixture.caseId, profile: fixture.profile }, Object.fromEntries(Object.entries(LegacyOperationEffects).map(([operation, effects]) =>
    [operation, { evidenceClass: 'deterministic' as const, effects: [...effects] }])));
  fixture.ownedWorkspaces = await testLegacyRoster(fixture, evidenceStore);
  putFixture(context, fixture);
  const terminal = await registry.invoke({ id: 'loom.runtime.stimulate', version: 1, input: {} },
    { leaseId: 'lease', targetId: 'terminal', operation: 'terminal-close', expectedGeneration: 'gen' }, context);
  assert.equal(terminal.availability, 'unsupported'); assert.equal(factories, 0); assert.equal(executed, 0);
  const invoke = (input: unknown) => registry.invoke({ id: 'loom.cli.role', version: 1, input: {} }, input, context);
  const result = ObservationResultSchema.parse(await invoke({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }));
  assert.equal(result.availability, 'observed'); assert.ok(result.data); assert.equal(result.provenance.identity.fixtureLeaseId, 'lease');
  const retained = await evidenceStore.resolve(result.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), result.data);
  assert.equal(result.provenance.evidenceClass, 'deterministic'); assert.equal(executed, 1); assert.equal(verified, 1);
  const foreign = ObservationResultSchema.parse(await invoke({ leaseId: 'lease', workspaceId: 'OTHER', operation: 'list', name: null }));
  assert.equal(foreign.availability, 'error'); assert.equal(foreign.error?.code, 'ownership-mismatch'); assert.equal(foreign.data, undefined);
  await assert.rejects(invoke({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null, shell: 'echo' }));
  assert.equal(executed, 1);
  const cloned = ObservationResultSchema.parse(await registry.invoke({ id: 'loom.cli.role', version: 1, input: {} },
    { leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, { ...context }));
  assert.equal(cloned.availability, 'error'); assert.equal(cloned.error?.code, 'ownership-mismatch'); assert.equal(executed, 1);
  lease.evidence = 'real-native';
  const mismatch = ObservationResultSchema.parse(await invoke({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }));
  assert.equal(mismatch.availability, 'error'); assert.equal(mismatch.error?.code, 'source-mismatch'); assert.equal(executed, 1);
  // A second registered fixture tests metadata preservation only: its injected
  // CLI result is not evidence of a real native actor.
  const nativeContext = createCapabilityContext({ file: 'native-envelope.yaml', line: 1 }, registry);
  Object.assign(nativeContext, { runId: 'run' }); putEvidenceStore(nativeContext, evidenceStore);
  const nativeFixture: OwnedFixture = { ...fixture, suiteId: nativeContext.suiteId, scope: nativeContext.scope,
    caseId: nativeContext.caseId, evidenceClass: 'real-native' };
  nativeFixture.operationAuthority = createFixtureOperationAuthority({ leaseId: nativeFixture.leaseId, runId: nativeFixture.runId, suiteId: nativeFixture.suiteId, scope: nativeFixture.scope, caseId: nativeFixture.caseId, profile: nativeFixture.profile }, { 'loom.cli.role': { evidenceClass: 'real-native', effects: [...LegacyOperationEffects['loom.cli.role']] } });
  nativeFixture.ownedWorkspaces = await testLegacyRoster(nativeFixture, evidenceStore);
  putFixture(nativeContext, nativeFixture);
  const native = ObservationResultSchema.parse(await registry.invoke({ id: 'loom.cli.role', version: 1, input: {} },
    { leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, nativeContext));
  assert.equal(native.availability, 'observed'); assert.equal(native.provenance.evidenceClass, 'real-native');
  assert.equal(RoleOutput.parse(native.data).receipt.evidence, 'real-native'); assert.equal(executed, 2);
  lease.evidence = 'deterministic';
  revokeCapabilityContext(context);
  const revoked = ObservationResultSchema.parse(await invoke({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }));
  assert.equal(revoked.availability, 'error'); assert.equal(revoked.error?.code, 'ownership-mismatch'); assert.equal(executed, 2);
});

test('suite fixture configuration keeps its original restoration across declared cases', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts', 'legacy/operations.ts', 'legacy/catalog.ts',
    'legacy/scenarios.json', 'protocol.ts', 'operation.ts', 'ownership.ts', 'evidence.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry();
  let state = 'original'; let factories = 0; let enrollments = 0; let cleanup: (() => Promise<void>) | undefined;
  const lease: LegacyLease = { id: 'lease', runId: 'run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/owned/bin/loom', cwd: '/owned/work', env: {}, workspaces: ['WS'], agents: [], roles: [], issues: [], repos: [], processes: [], fixtures: ['provider-default'] };
  const unsupported = async (): Promise<never> => { throw new Error('Unused transport'); };
  const access: LegacyAccess = { lease: async () => lease, execute: unsupported, registerProcess: unsupported,
    stimulate: unsupported, request: unsupported, validateSeedPath: unsupported, seedCommit: unsupported,
    snapshot: async () => ({ complete: true, previous: state, restoreState: state }),
    restore: async (_lease, _target, saved) => { state = String(saved); },
    writeConfiguration: async () => { state = 'configured'; },
    enrollCleanup: (_lease, action) => { enrollments++; cleanup = action; } };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => { factories++; return access; })) registry.register(provider);
  const suite = createCapabilityContext({ file: 'suite.yaml', line: 1 }, registry);
  Object.assign(suite, { runId: 'run', caseId: 'setup', scope: 'suite' });
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true }));
  const evidence = await createEvidenceStore(evidenceRoot); putEvidenceStore(suite, evidence);
  const fixture: OwnedFixture = { leaseId: 'lease', runId: 'run', caseId: 'setup', suiteId: suite.suiteId, scope: 'suite', workspaceId: 'WS', repo: '/owned/source', profile: 'legacy',
    expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic', roots: new Map(), agents: new Map(), secrets: [],
    readApi: unsupported, readFiles: unsupported, resolveAgent: unsupported, verify: async () => {}, dispose: async () => { await cleanup?.(); } };
  fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: fixture.leaseId, runId: fixture.runId, suiteId: fixture.suiteId, scope: fixture.scope, caseId: fixture.caseId, profile: fixture.profile }, { 'loom.fixture.configure': { evidenceClass: 'deterministic', effects: [...LegacyOperationEffects['loom.fixture.configure']] } });
  putFixture(suite, fixture);
  // This bounded unit accessor exercises provider state across cases; actual
  // runner suite authorization remains the engine's separate integration gate.
  const caseContext = (caseId: string, handles: string[]) => {
    const context = createCapabilityContext({ file: 'suite.yaml', line: 1 }, registry);
    Object.assign(context, { runId: suite.runId, suiteId: suite.suiteId, caseId,
      suite: { id: suite.suiteId, handles, getResource: (key: string, handle: string) => handles.includes(handle) ? getRegisteredResource(suite, key, handle) : undefined } });
    return context;
  };
  const invoke = (context: CapabilityContext) => registry.invoke({ id: 'loom.fixture.configure', version: 1, input: {} }, input, context);
  const input = { leaseId: 'lease', setting: 'provider-default', model: 'aft/m', harness: 'opencode' };
  const firstCase = caseContext('first', ['lease']); const secondCase = caseContext('second', ['lease']);
  assert.equal(ObservationResultSchema.parse(await invoke(firstCase)).availability, 'observed');
  await disposeFixtures(firstCase); assert.equal(state, 'configured');
  const second = ObservationResultSchema.parse(await invoke(secondCase));
  assert.equal(second.availability, 'observed'); assert.equal(factories, 1); assert.equal(enrollments, 1);
  assert.equal(ObservationResultSchema.parse(await invoke(caseContext('foreign', []))).availability, 'error');
  lease.active = false; await disposeFixtures(suite); assert.equal(state, 'original');
  const retained = await evidence.resolve(second.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), second.data);
});

test('registry authorizes operation classes and external effects before factories; backend/model changes fail closed', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts','legacy/operations.ts','authority.ts','redaction.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); let factories = 0, effects = 0;
  const unused = async (): Promise<never> => { throw new Error('Unused deterministic transport'); };
  const lease: LegacyLease = { id: 'mixed-lease', runId: 'mixed-run', active: true, evidence: 'real-native', secrets: [],
    binary: '/injected/loom', cwd: '/injected/work', env: {}, workspaces: ['WS'], agents: [{ workspaceId: 'WS', id: 'worker', name: 'worker', generation: 'row-revision' }],
    roles: [], issues: [], repos: [], processes: [], fixtures: [] };
  const access: LegacyAccess = { lease: async (_id, _signal, operation) => ({ ...lease, evidence: operation === 'loom.cli.task' ? 'live-provider' : 'real-native' }),
    async execute(_id, command) { effects++; return { processId: 'owned-cli', generation: 'owned-generation',
      exitCode: command.argv[4] === 'task' ? null : 0, complete: command.argv[4] !== 'task', stdout: '[]', stderr: '' }; },
    registerProcess: async () => {}, stimulate: unused, request: unused, validateSeedPath: unused, seedCommit: unused,
    snapshot: unused, restore: unused, writeConfiguration: unused, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => { factories++; return access; }, { taskExecution: 'live-provider' })) registry.register(provider);
  const context = createCapabilityContext({ file: 'declared-live-double.yaml', line: 1 }, registry); Object.assign(context, { runId: 'mixed-run' });
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true })); const evidenceStore = await createEvidenceStore(evidenceRoot); putEvidenceStore(context, evidenceStore);
  const fixture: OwnedFixture = { leaseId: lease.id, runId: lease.runId, suiteId: context.suiteId, scope: context.scope, caseId: context.caseId,
    workspaceId: 'WS', repo: '/injected/work', profile: 'legacy-real-codex', expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'real-native',
    roots: new Map(), agents: new Map(), secrets: [], readApi: unused, readFiles: unused, resolveAgent: unused, verify: async () => {}, dispose: async () => {} };
  fixture.readWorkspaceLegacyAgent = unused;
  fixture.ownedWorkspaces = await testLegacyRoster(fixture, evidenceStore, [{ workspaceId: 'WS', repo: fixture.repo, agentIds: ['worker'] }]);
  putFixture(context, fixture);
  const task = { leaseId: lease.id, workspaceId: 'WS', agentName: 'worker', backend: 'codex', mode: 'once', issueId: null };
  const invoke = (id: string, input: unknown) => registry.invoke({ id, version: 1, input: {} }, input, context);
  assert.equal((await invoke('loom.cli.task', task)).availability, 'unsupported'); assert.equal(factories, 0);
  const owner = { leaseId: fixture.leaseId, runId: fixture.runId, suiteId: fixture.suiteId, scope: fixture.scope, caseId: fixture.caseId, profile: fixture.profile };
  fixture.operationAuthority = createFixtureOperationAuthority(owner, { 'loom.cli.task': { evidenceClass: 'live-provider', effects: ['start-owned-process'] } });
  assert.equal((await invoke('loom.cli.task', task)).availability, 'unsupported'); assert.equal(factories, 0);
  fixture.operationAuthority = createFixtureOperationAuthority(owner, {
    'loom.cli.role': { evidenceClass: 'real-native', effects: [...LegacyOperationEffects['loom.cli.role']] },
    'loom.cli.task': { evidenceClass: 'live-provider', effects: [...LegacyOperationEffects['loom.cli.task'], 'external-provider'] },
  });
  assert.equal((await invoke('loom.cli.task', { ...task, backend: 'claude' })).availability, 'unsupported'); assert.equal(factories, 0);
  await assert.rejects(invoke('loom.cli.task', { ...task, model: 'unreviewed-paid-model' })); assert.equal(factories, 0);
  const role = await invoke('loom.cli.role', { leaseId: lease.id, workspaceId: 'WS', operation: 'list', name: null });
  assert.equal(role.availability, 'observed'); assert.equal(role.provenance.evidenceClass, 'real-native');
  const paidDouble = await invoke('loom.cli.task', task);
  assert.equal(paidDouble.availability, 'observed'); assert.equal(paidDouble.provenance.evidenceClass, 'live-provider');
  assert.equal((paidDouble.data as { receipt: { evidence: string } }).receipt.evidence, 'live-provider');
  assert.equal(factories, 1); assert.equal(effects, 2);
  // Declared classes above exercise envelopes only. No backend or paid service ran.
});

test('legacy redaction receipts distinguish omitted source fields from absence', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts','legacy/operations.ts','redaction.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); const unused = async (): Promise<never> => { throw new Error('Unused transport'); };
  const lease: LegacyLease = { id: 'privacy-lease', runId: 'privacy-run', active: true, evidence: 'deterministic', secrets: ['literal-private'], binary: '/injected/loom',
    cwd: '/injected/work', env: {}, workspaces: ['WS'], agents: [], roles: [], issues: [], repos: [], processes: [], fixtures: [] };
  const access: LegacyAccess = { lease: async () => lease, execute: async () => ({ processId: 'p', generation: 'g', exitCode: 0, complete: true, stderr: '',
    stdout: JSON.stringify({ authorization: 'literal-private', nested: { text: 'literal-private', visible: 'safe' } }) }),
    registerProcess: unused, stimulate: unused, request: unused, validateSeedPath: unused, seedCommit: unused, snapshot: unused, restore: unused, writeConfiguration: unused, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => access)) registry.register(provider);
  const context = createCapabilityContext({ file: 'privacy-double.yaml', line: 1 }, registry); Object.assign(context, { runId: lease.runId });
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true })); const evidenceStore = await createEvidenceStore(evidenceRoot); putEvidenceStore(context, evidenceStore);
  const fixture: OwnedFixture = { leaseId: lease.id, runId: lease.runId, suiteId: context.suiteId, scope: context.scope, caseId: context.caseId,
    workspaceId: 'WS', repo: '/injected/work', profile: 'legacy-deterministic', expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic',
    roots: new Map(), agents: new Map(), secrets: lease.secrets, readApi: unused, readFiles: unused, resolveAgent: unused, verify: async () => {}, dispose: async () => {} };
  fixture.operationAuthority = createFixtureOperationAuthority({ leaseId: fixture.leaseId, runId: fixture.runId, suiteId: fixture.suiteId,
    scope: fixture.scope, caseId: fixture.caseId, profile: fixture.profile }, { 'loom.cli.role': { evidenceClass: 'deterministic', effects: [...LegacyOperationEffects['loom.cli.role']] } }); fixture.ownedWorkspaces = await testLegacyRoster(fixture, evidenceStore); putFixture(context, fixture);
  const result = await registry.invoke({ id: 'loom.cli.role', version: 1, input: {} }, { leaseId: lease.id, workspaceId: 'WS', operation: 'list', name: null }, context);
  assert.equal(result.availability, 'observed');
  const data = RoleOutput.parse(result.data);
  assert.deepEqual(data.body, { nested: { text: '[REDACTED]', visible: 'safe' } });
  assert.deepEqual(data.receipt.factsRedaction, { omittedPaths: ['/body/authorization'], replacedTextPaths: ['/body/nested/text'] });
  assert.ok(data.redaction.omittedPaths.includes('/body/authorization')); assert.ok(data.redaction.replacedTextPaths.includes('/body/nested/text'));
  assert.equal(JSON.stringify(data).includes('literal-private'), false);
});

test('legacy names are scoped by retained workspace kind and dynamic enrollment before factories', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts','legacy/effects.ts','workspaces.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); let factories = 0, effects = 0, nativeReads = 0;
  const unused = async (): Promise<never> => { throw new Error('Unused injected transport'); };
  const lease: LegacyLease = { id: 'roster-lease', runId: 'roster-run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/injected/loom', cwd: '/injected/repo', env: {}, workspaces: ['WS','OTHER'],
    agents: ['WS','OTHER'].flatMap(workspaceId => ['worker','child','cycle'].map(name => ({ workspaceId, id: name, name, generation: 'row-revision' }))),
    roles: [], issues: [], repos: [], processes: [], fixtures: [] };
  const access: LegacyAccess = { lease: async () => lease,
    execute: async () => { effects++; return { processId: 'p', generation: 'g', exitCode: 0, complete: true, stdout: '{}', stderr: '' }; },
    registerProcess: unused, stimulate: unused, request: unused, validateSeedPath: unused, seedCommit: unused,
    snapshot: unused, restore: unused, writeConfiguration: unused, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => { factories++; return access; })) registry.register(provider);
  const context = createCapabilityContext({ file: 'roster-double.yaml', line: 1 }, registry); Object.assign(context, { runId: lease.runId });
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true })); const store = await createEvidenceStore(evidenceRoot); putEvidenceStore(context, store);
  const fixture: OwnedFixture = { leaseId: lease.id, runId: lease.runId, suiteId: context.suiteId, scope: context.scope, caseId: context.caseId,
    workspaceId: 'WS', repo: '/injected/repo', profile: 'legacy-deterministic', expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic',
    roots: new Map(), agents: new Map(), secrets: [], readApi: unused, readFiles: unused, resolveAgent: unused,
    readWorkspaceAgent: async () => { nativeReads++; throw new Error('Legacy name must never use native ID reader'); }, verify: async () => {}, dispose: async () => {} };
  const owner = { leaseId: fixture.leaseId, runId: fixture.runId, suiteId: fixture.suiteId, scope: fixture.scope, caseId: fixture.caseId, profile: fixture.profile };
  fixture.operationAuthority = createFixtureOperationAuthority(owner, { 'loom.cli.usage': { evidenceClass: 'deterministic', effects: [...LegacyOperationEffects['loom.cli.usage']] } });
  fixture.ownedWorkspaces = await testLegacyRoster(fixture, store, ['WS','OTHER'].map(workspaceId => ({ workspaceId, repo: '/injected/repo', agentIds: ['worker'] })));
  fixture.readWorkspaceLegacyAgent = async (workspaceId, name) => ({ kind: 'legacy-agent-enrolled', identityKind: 'legacy-agent-name', ...owner,
    workspaceId, name, repo: '/injected/repo', commonDir: '/injected/repo/.git', storeId: 'injected-legacy-store', storeGeneration: 'injected-store-generation',
    parentName: name === 'worker' ? null : name === 'cycle' ? 'cycle' : 'worker', createdAt: '2026-10-09T00:00:00Z', updatedAt: '2026-10-09T00:01:00Z' });
  putFixture(context, fixture);
  const invoke = (workspaceId: string, agentId: string) => registry.invoke({ id: 'loom.cli.usage', version: 1, input: {} },
    { agent: { fixtureLeaseId: lease.id, workspaceId, agentId } }, context);
  assert.equal((await invoke('FOREIGN','worker')).availability, 'error'); assert.equal(factories, 0); assert.equal(effects, 0);
  assert.equal((await invoke('WS','cycle')).availability, 'error'); assert.equal(factories, 0); assert.equal(effects, 0);
  for (const workspaceId of ['WS','OTHER']) {
    const result = await invoke(workspaceId, 'worker'); assert.equal(result.availability, 'observed');
    assert.equal(result.provenance.identity.workspaceId, workspaceId);
  }
  const initial = fixture.ownedWorkspaces![0]!.creationReceipt;
  const child = await invoke('WS','child'); assert.equal(child.availability, 'observed'); assert.equal(nativeReads, 0);
  assert.equal(fixture.ownedWorkspaces![0]!.creationReceipt.id, initial.id, 'Dynamic enrollment preserves original creation bytes');
  assert.equal(fixture.ownedWorkspaces![0]!.enrollmentReceipts.length, 1);
  const receipt = JSON.parse(await readFile(await store.resolve(fixture.ownedWorkspaces![0]!.enrollmentReceipts[0]!.id), 'utf8'));
  assert.equal(receipt.identityKind, 'legacy-agent-name'); assert.equal(receipt.name, 'child'); assert.equal(receipt.parentName, 'worker');
  assert.equal(factories, 1); assert.equal(effects, 3);
});

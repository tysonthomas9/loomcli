import { test } from 'node:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { mkdtemp, rm, realpath, readFile } from 'node:fs/promises';
import { createEvidenceStore, putEvidenceStore } from '../evidence.js';
import { CapabilityRegistry, calculateImplementationPin, createCapabilityContext } from '@tysonthomas9/aft/capabilities';
import { ObservationResultSchema } from '@tysonthomas9/aft/types';
import { putFixture, disposeFixtures, type OwnedFixture } from '../ownership.js';
import { createLegacyProviders } from './providers.js';
import type { LegacyAccess, LegacyLease } from './operations.js';

test('all six legacy providers register strict contracts and return canonical envelopes', async t => {
  const root = fileURLToPath(new URL('..', import.meta.url));
  const pin = calculateImplementationPin(root, ['legacy/providers.ts', 'legacy/operations.ts', 'legacy/catalog.ts',
    'legacy/scenarios.json', 'protocol.ts', 'operation.ts', 'ownership.ts', 'evidence.ts'], 'legacy/providers.ts', 'createLegacyProviders');
  const registry = new CapabilityRegistry(); let executed = 0; let verified = 0;
  const lease: LegacyLease = { id: 'lease', runId: 'run', active: true, evidence: 'deterministic', secrets: [],
    binary: '/owned/bin/loom', cwd: '/owned/work', env: {}, workspaces: ['WS'], agents: [], roles: [], issues: [], repos: [], processes: [], fixtures: [] };
  const unsupported = async (): Promise<never> => { throw new Error('Unused transport'); };
  const access: LegacyAccess = { lease: async () => lease,
    execute: async () => { executed++; return { processId: 'cli', generation: 'gen', complete: true, exitCode: 0, stdout: '[]', stderr: '' }; },
    registerProcess: unsupported, stimulate: unsupported, request: unsupported, validateSeedPath: unsupported,
    seedCommit: unsupported, snapshot: unsupported, restore: unsupported, writeConfiguration: unsupported, enrollCleanup: () => {} };
  for (const provider of createLegacyProviders(pin, pin.sha256, () => access)) registry.register(provider);
  for (const id of ['loom.cli.role', 'loom.cli.usage', 'loom.cli.task', 'loom.runtime.stimulate', 'loom.fixture.seedWorktree', 'loom.fixture.configure'])
    assert.equal(registry.get(id, 1).retry, 'never');
  const context = { ...createCapabilityContext({ file: 'test.yaml', line: 1 }, registry), runId: 'run', caseId: 'case' };
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true }));
  const evidenceStore = await createEvidenceStore(evidenceRoot); putEvidenceStore(context, evidenceStore);
  const fixture: OwnedFixture = { leaseId: 'lease', runId: 'run', caseId: 'case', suiteId: context.suiteId, scope: context.scope, workspaceId: 'WS', repo: '/owned/source', profile: 'legacy',
    expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic', roots: new Map(), agents: new Map(), secrets: [],
    readApi: unsupported, readFiles: unsupported, resolveAgent: unsupported, verify: async () => { verified++; }, dispose: async () => {} };
  putFixture(context, fixture);
  const provider = registry.get('loom.cli.role', 1);
  const result = ObservationResultSchema.parse(await provider.execute({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null }, context));
  assert.equal(result.availability, 'observed'); assert.ok(result.data); assert.equal(result.provenance.identity.fixtureLeaseId, 'lease');
  const retained = await evidenceStore.resolve(result.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), result.data);
  assert.equal(result.provenance.evidenceClass, 'deterministic'); assert.equal(executed, 1); assert.equal(verified, 1);
  const foreign = ObservationResultSchema.parse(await provider.execute({ leaseId: 'lease', workspaceId: 'OTHER', operation: 'list', name: null }, context));
  assert.equal(foreign.availability, 'error'); assert.equal(foreign.error?.code, 'ownership-mismatch'); assert.equal(foreign.data, undefined);
  const unsafe = ObservationResultSchema.parse(await provider.execute({ leaseId: 'lease', workspaceId: 'WS', operation: 'list', name: null, shell: 'echo' }, context));
  assert.equal(unsafe.availability, 'error'); assert.equal(unsafe.data, undefined); assert.equal(executed, 1);
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
  const suite = { ...createCapabilityContext({ file: 'suite.yaml', line: 1 }, registry), runId: 'run', caseId: 'setup', scope: 'suite' as const };
  const evidenceRoot = await realpath(await mkdtemp(fileURLToPath(new URL('.seed-test-evidence-', import.meta.url))));
  t.after(() => rm(evidenceRoot, { recursive: true, force: true }));
  const evidence = await createEvidenceStore(evidenceRoot); putEvidenceStore(suite, evidence);
  const fixture: OwnedFixture = { leaseId: 'lease', runId: 'run', caseId: 'setup', suiteId: suite.suiteId, scope: 'suite', workspaceId: 'WS', repo: '/owned/source', profile: 'legacy',
    expiresAtUtcMs: Number.MAX_SAFE_INTEGER, evidenceClass: 'deterministic', roots: new Map(), agents: new Map(), secrets: [],
    readApi: unsupported, readFiles: unsupported, resolveAgent: unsupported, verify: async () => {}, dispose: async () => { await cleanup?.(); } };
  putFixture(suite, fixture);
  const caseContext = (caseId: string, handles: string[]) => ({ ...suite, caseId, scope: 'case' as const, resources: new Map<string, unknown>(),
    suite: { id: suite.suiteId, handles, getResource: (key: string, handle: string) => handles.includes(handle) ? suite.resources.get(key) : undefined } });
  const provider = registry.get('loom.fixture.configure', 1);
  const input = { leaseId: 'lease', setting: 'provider-default', model: 'aft/m', harness: 'opencode' };
  const firstCase = caseContext('first', ['lease']); const secondCase = caseContext('second', ['lease']);
  assert.equal(ObservationResultSchema.parse(await provider.execute(input, firstCase)).availability, 'observed');
  await disposeFixtures(firstCase); assert.equal(state, 'configured');
  const second = ObservationResultSchema.parse(await provider.execute(input, secondCase));
  assert.equal(second.availability, 'observed'); assert.equal(factories, 1); assert.equal(enrollments, 1);
  assert.equal(ObservationResultSchema.parse(await provider.execute(input, caseContext('foreign', []))).availability, 'error');
  lease.active = false; await disposeFixtures(suite); assert.equal(state, 'original');
  const retained = await evidence.resolve(second.provenance.artifacts[0]!.id);
  assert.deepEqual(JSON.parse(await readFile(retained, 'utf8')), second.data);
});

import { lstat, realpath } from 'node:fs/promises';
import path from 'node:path';
import { z } from 'zod';
import type { CapabilityContext, CapabilityProvider, CapabilityRegistry, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import { ArtifactRefSchema, type EvidenceClass } from '@tysonthomas9/aft/types';
import { defineOperation } from '../operation.js';
import { putFixture, getFixture, releaseFixture, fixturesKey, type OwnedFixture, type OwnedAgent } from '../ownership.js';
import { createNativeHostAccess } from '../native-host.js';
import { createSyntheticProbe } from '../synthetic-probe.js';
import { ContainerRootIdentity, containerFilesystemObserver, containerGitObserver, containerGitLifecycleObserver, type ContainerObservationRead } from '../container-observations.js';
import { AgentRow, AgentHistory, NativeRef, ServiceRegistration, HttpResponse, Id, Digest, ObservationError, requireFact,
  type NativeAccess, type ProcessIdentity, type ReadTransport } from '../protocol.js';
import { FixtureLifecycle, FixtureError, type FixtureDriver, type FixturePlan, type AcquireRequest } from './lifecycle.js';
import { HostFixtureDriver, readHttp, type HostConfig } from './host.js';
import { ComposeFixtureDriver, type ProductionConfig } from './production.js';
import { ContainerReadRequest } from './container-read.js';
import { bindRenderer } from './renderer.js';
import { createEvidenceStore, putEvidenceStore, evidenceKey, type EvidenceStore } from '../evidence.js';

// Operation-specific schemas are owned here. General envelopes, artifact refs,
// capability registration and public resource ownership come from core/AFT.
const Revision = z.object({ repository: Id, commit: z.string().regex(/^[a-f0-9]{40}$/), tree: Id,
  sourceManifestSha256: Digest, buildManifestSha256: Digest }).strict();
const Profiles = z.enum(['agents-real-opencode', 'agents-emulator', 'legacy-deterministic',
  'legacy-real-codex', 'legacy-real-claude', 'legacy-real-cursor', 'legacy-real-opencode', 'legacy-real-codex-podman']);
export const AcquireInput = z.object({ runId: Id, profile: Profiles, loomRevision: Revision, fleetRevision: Revision,
  model: Id, maxCases: z.number().int().positive(), selectionSha256: Digest }).strict();
export const LeaseInput = z.object({ leaseId: Id }).strict();
const Lease = z.object({ id: Id, runId: Id, resourceManifestSha256: Digest, source: Revision, engine: Revision, adapter: Revision, expiresAt: Id }).strict();
export const AcquireOutput = z.object({ lease: Lease, apiOrigin: Id, filesOrigin: Id, workspaceId: Id, repo: Id,
  browserLeaseId: Id, ownershipArtifact: ArtifactRefSchema, syntheticProbeHandle: Id, syntheticProbeRunId: Id,fixtureRunId:Id.optional() }).strict();
const ProvisionedOutput = AcquireOutput.omit({ syntheticProbeHandle: true, syntheticProbeRunId: true,fixtureRunId:true });
export const ReleaseOutput = z.object({ released: z.boolean(), remainingOwnedResources: z.array(Id), receipt: ArtifactRefSchema }).strict();
export const ObserveOutput = z.object({ owned: z.boolean(), sourceMatches: z.boolean(), services: z.array(z.object({
  id: Id, pid: z.number().int().nonnegative(), generation: Id, state: Id }).strict()), inventory: ArtifactRefSchema }).strict();
type Released = z.infer<typeof ReleaseOutput>;
interface PrivateFixture {
  manager: FixtureLifecycle; driver: FixtureDriver; lastRelease?: Released;
}
const privateFixtures = new WeakMap<OwnedFixture, PrivateFixture>();
export function privateFixtureDriver(fixture: OwnedFixture): FixtureDriver {
  const owned = privateFixtures.get(fixture);
  requireFact(owned, 'ownership-mismatch', 'Fixture driver is not registered'); return owned.driver;
}
export interface FixtureProviderOptions {
  implementation: ImplementationPin; implementationSha256: string;
  plans: readonly FixturePlan[];
  driver(profile: string): FixtureDriver;
  bind(driver: FixtureDriver, acquired: z.infer<typeof ProvisionedOutput>, input: AcquireRequest, context: CapabilityContext): Promise<{
    evidenceClass: EvidenceClass; roots: OwnedFixture['roots']; secrets: readonly string[];
    readApi: ReadTransport; readFiles: ReadTransport; resolveAgent: OwnedFixture['resolveAgent'];
    evidenceStore: EvidenceStore;
    rendererTarget?: OwnedFixture['rendererTarget'];
    operationAuthority?: OwnedFixture['operationAuthority'];
    ownedWorkspaces?: OwnedFixture['ownedWorkspaces'];
    readWorkspaceLegacyAgent?: OwnedFixture['readWorkspaceLegacyAgent'];
    fixtureRunId?:string;
  }>;
  evidenceAfterFailure(driver: FixtureDriver): Promise<EvidenceStore>;
}
async function checked<T>(operation: () => Promise<T>): Promise<T> {
  try { return await operation(); }
  catch (error) { if (error instanceof FixtureError) throw new ObservationError(error.code, `Fixture ${error.code}`); throw error; }
}
export function createFixtureProviders(options: FixtureProviderOptions): CapabilityProvider[] {
  const common = { implementation: options.implementation, implementationSha256: options.implementationSha256,
    evidenceClasses: ['deterministic', 'real-native', 'live-provider'] as EvidenceClass[] };
  return [
    defineOperation({ ...common, id: 'loom.fixture.acquire', inputSchema: AcquireInput, outputSchema: AcquireOutput,
      effects: ['write-fixture', 'start-owned-process'], retry: 'never', cleanup: 'release-lease',
      async dispose(context) {
        // Final cleanup stays in the canonical owner store, including expired or
        // cancelled resources. Never sweep another suite/case's private ledger.
        const { disposeFixtures } = await import('../ownership.js'); await disposeFixtures(context);
      },
      async run(input, context) {
        requireFact(input.runId === context.runId, 'identity-mismatch', 'Acquisition run does not match execution');
        let driver: FixtureDriver | undefined;
        const manager = new FixtureLifecycle(options.plans, () => { driver = options.driver(input.profile); return driver; },
          () => context.clock.epochUtcMs + context.clock.now());
        let acquired: z.infer<typeof ProvisionedOutput>;
        const registerIncomplete = async (leaseId: string) => {
            if (!context.resources.has(evidenceKey)) putEvidenceStore(context, await options.evidenceAfterFailure(driver!));
            // Cleanup-only registration is never an available fixture. It lets
            // canonical final disposal retry a failed partial acquisition.
            const unavailable = async (): Promise<never> => { throw new ObservationError('observation-failed', 'Fixture acquisition is incomplete'); };
            const fixture: OwnedFixture = { leaseId, runId: context.runId, suiteId: context.suiteId, scope: context.scope, caseId: context.caseId,
              profile: input.profile, workspaceId: 'acquisition-incomplete', repo: 'acquisition-incomplete', expiresAtUtcMs: 0,
              evidenceClass: 'deterministic', roots: new Map(), agents: new Map(), secrets: [],
              readApi: unavailable, readFiles: unavailable, resolveAgent: unavailable, verify: unavailable,
              async dispose() {
                const state = privateFixtures.get(fixture)!;
                const released = await manager.release(leaseId, input.runId); state.lastRelease = released;
                requireFact(released.released, 'ownership-mismatch', 'Partial acquisition cleanup is incomplete');
              } };
            privateFixtures.set(fixture, { manager, driver: driver! }); putFixture(context, fixture);
        };
        try { acquired = await manager.acquire(input, context.signal); }
        catch (error) {
          if (error instanceof FixtureError && error.leaseId && error.remainingOwnedResources.length) await registerIncomplete(error.leaseId);
          if (error instanceof FixtureError) throw new ObservationError(error.code, `Fixture ${error.code}`);
          throw error;
        }
        try {
          const binding = await options.bind(driver!, acquired, input, context);
          const { evidenceStore,fixtureRunId, ...transport } = binding;
          if (!context.resources.has(evidenceKey)) putEvidenceStore(context, evidenceStore);
          const fixture: OwnedFixture = { leaseId: acquired.lease.id, runId: context.runId, caseId: context.caseId,
            suiteId: context.suiteId, scope: context.scope, profile: input.profile,
            workspaceId: acquired.workspaceId, repo: acquired.repo, expiresAtUtcMs: Date.parse(acquired.lease.expiresAt),
            agents: new Map(), syntheticProbe: createSyntheticProbe(context.runId, acquired.lease.id), ...transport,
            async verify(signal) { signal.throwIfAborted(); await checked(() => manager.observe(acquired.lease.id, input.runId)); },
            async dispose() {
              const state = privateFixtures.get(fixture)!;
              const released = await checked(() => manager.release(acquired.lease.id, input.runId)); state.lastRelease = released;
              requireFact(released.released, 'ownership-mismatch', 'Fixture cleanup is incomplete; safe receipt retained');
            },
          };
          privateFixtures.set(fixture, { manager, driver: driver! });
          putFixture(context, fixture);
          return { value: { ...acquired,...(fixtureRunId?{fixtureRunId}:{}), syntheticProbeHandle: fixture.syntheticProbe!.handle, syntheticProbeRunId: fixture.syntheticProbe!.runId }, evidenceClass: fixture.evidenceClass,
            identity: { fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId } };
        } catch (error) {
          const released = await manager.release(acquired.lease.id, input.runId);
          if (!released.released && !context.resources.has(`${fixturesKey}:${acquired.lease.id}`)) await registerIncomplete(acquired.lease.id);
          throw error;
        }
      },
    }),
    defineOperation({ ...common, id: 'loom.fixture.observe', inputSchema: LeaseInput, outputSchema: ObserveOutput,
      effects: ['read-filesystem'], retry: 'read-only-until-deadline', cleanup: 'none',
      async run(input, context) {
        const fixture = await getFixture(context, input.leaseId); const state = privateFixtures.get(fixture);
        requireFact(state, 'ownership-mismatch', 'Fixture lifecycle is missing');
        const value = await checked(() => state.manager.observe(input.leaseId, fixture.runId));
        return { value, evidenceClass: fixture.evidenceClass, identity: { fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId } };
      },
    }),
    defineOperation({ ...common, id: 'loom.fixture.release', inputSchema: LeaseInput, outputSchema: ReleaseOutput,
      effects: ['release-owned-resource'], retry: 'never', cleanup: 'none',
      async run(input, context) {
        // Direct local lookup is intentional: core release authorizes the scope
        // while permitting cleanup after expiration and signal cancellation.
        const fixture = context.resources.get(`${fixturesKey}:${input.leaseId}`) as OwnedFixture | undefined;
        requireFact(fixture && fixture.runId === context.runId && fixture.suiteId === context.suiteId &&
          fixture.scope === context.scope && (fixture.scope === 'suite' || fixture.caseId === context.caseId),
        'ownership-mismatch', 'Cannot release a foreign fixture');
        const state = privateFixtures.get(fixture); requireFact(state, 'ownership-mismatch', 'Fixture lifecycle is missing');
        state.lastRelease = undefined;
        try { await releaseFixture(context, input.leaseId); }
        catch (error) { if (!state.lastRelease) throw error; }
        requireFact(state.lastRelease, 'observation-failed', 'Fixture release receipt is missing');
        return { value: state.lastRelease, evidenceClass: fixture.evidenceClass,
          identity: { fixtureLeaseId: fixture.leaseId, workspaceId: fixture.workspaceId } };
      },
    }),
  ];
}
export function registerFixtures(registry: CapabilityRegistry, options: FixtureProviderOptions) {
  for (const provider of createFixtureProviders(options)) registry.register(provider);
}

function fixedRead(origin: string): ReadTransport {
  return async (route, signal) => {
    requireFact(route.startsWith('/api/') && !route.startsWith('//') && !route.includes('\\') && !route.split('?')[0]!.split('/').includes('..'),
      'ownership-mismatch', 'Read route is outside the fixture API');
    return HttpResponse.parse(await readHttp(origin, 'GET', route, null, signal));
  };
}
function containerNative(driver: ComposeFixtureDriver, pinnedExecutable: string): NativeAccess {
  const read = (request: z.infer<typeof ContainerReadRequest>, signal = AbortSignal.timeout(15000)) => driver.nativeRead(ContainerReadRequest.parse(request), signal);
  return { pinnedExecutable,
    registration: async () => ServiceRegistration.parse(await read({ operation: 'registration' })),
    process: async () => {
      const result = await read({ operation: 'process' }) as ProcessIdentity;
      requireFact(result.executable === pinnedExecutable && Number.isInteger(result.pid) && result.pid > 0 && result.generation,
        'identity-mismatch', 'Native process is not pinned'); return result;
    },
    agent: async agentId => AgentRow.parse(await read({ operation: 'agent', agentId })),
    history: async agentId => AgentHistory.parse(await read({ operation: 'agent-history', agentId })),
    sessions: async agentId => z.array(NativeRef).parse(await read({ operation: 'sessions', agentId })),
    read: async (route, signal) => HttpResponse.parse(await read({ operation: 'read', route }, signal)),
  };
}
export function productionFixtureOptions(implementation: ImplementationPin, implementationSha256: string,
  plans: readonly FixturePlan[], compose: ProductionConfig, host: HostConfig): FixtureProviderOptions {
  return { implementation, implementationSha256, plans,
    driver: profile => {
      if(profile.startsWith('agents-')||profile==='legacy-real-codex-podman')return new ComposeFixtureDriver(compose);
      // Production legacy binding requires actual captured store generation.
      // Reject its missing launcher prerequisite before auth or provisioning.
      requireFact(host.registeredProcesses,'unsupported-capability','Owned host store process capture is not configured');
      return new HostFixtureDriver(host);
    },
    async evidenceAfterFailure(driver) {
      requireFact(driver instanceof ComposeFixtureDriver || driver instanceof HostFixtureDriver, 'ownership-mismatch', 'Fixture driver is not owned');
      return createEvidenceStore(path.join(driver.runtimeRoot, 'evidence'));
    },
    async bind(driver, acquired, input, context) {
      const isCompose = driver instanceof ComposeFixtureDriver;
      requireFact(isCompose || driver instanceof HostFixtureDriver, 'ownership-mismatch', 'Fixture transport is not a production driver');
      const runtimeRoot = driver.runtimeRoot;
      const stat = await lstat(runtimeRoot);
      const roots: OwnedFixture['roots'] = new Map([['runtime', { path: runtimeRoot, device: stat.dev, inode: stat.ino }]]);
      let resolveAgent: OwnedFixture['resolveAgent'];
      if (isCompose) {
        const pinnedExecutable = input.profile === 'agents-emulator' ? '/opt/fixture/loom-harness-emu' : input.profile === 'legacy-real-codex-podman' ? '/usr/local/bin/codex' : '/usr/local/bin/opencode';
        const native = containerNative(driver, pinnedExecutable);
        const read: ContainerObservationRead = (request, signal) => driver.nativeRead(request, signal);
        const managed = ContainerRootIdentity.parse(await read({ operation: 'filesystem-root', root: { kind: 'managed-repo' } }, context.signal));
        roots.set('managed-repo', { ...managed, remoteObserve: containerFilesystemObserver(read, { kind: 'managed-repo' }, managed) });
        const temporary=ContainerRootIdentity.parse(await read({operation:'filesystem-root',root:{kind:'fixture-temporary'}},context.signal));
        roots.set('fixture-temporary',{...temporary,remoteObserve:containerFilesystemObserver(read,{kind:'fixture-temporary'},temporary)});
        resolveAgent = async (agentId, signal): Promise<OwnedAgent> => {
          const row = await native.agent(agentId);
          const common = z.object({ commonDir: Id }).strict().parse(await driver.nativeRead({ operation: 'git-common-dir', agentId }, signal));
          const selector = { kind: 'agent-worktree' as const, agentId };
          const stamp = ContainerRootIdentity.parse(await read({ operation: 'filesystem-root', root: selector }, signal));
          roots.set(`agent-worktree:${agentId}`, { ...stamp, remoteObserve: containerFilesystemObserver(read, selector, stamp) });
          return { row, commonDir: common.commonDir, native, gitObserve: containerGitObserver(read,
            { fixtureLeaseId: acquired.lease.id, workspaceId: acquired.workspaceId, agentId },
            { worktree: row.worktree_path, commonDir: common.commonDir, branch: row.branch }),
            gitLifecycle: containerGitLifecycleObserver(read,{fixtureLeaseId:acquired.lease.id,workspaceId:acquired.workspaceId,agentId},
              {sourceRoot:acquired.repo,commonDir:common.commonDir,branch:row.branch,worktree:row.worktree_path}) };
        };
      } else {
        const native = createNativeHostAccess({ configRoot: path.join(driver.workspaceRoot, '.loom-config'), workspaceId: acquired.workspaceId,
          repo: acquired.repo, pinnedExecutable: input.profile === 'legacy-real-opencode' ? host.realBinaries.opencode!.executable : host.pinnedOpenCodeBinary });
        resolveAgent = async (agentId): Promise<OwnedAgent> => {
          const row = await native.agent(agentId);
          const worktree = await realpath(row.worktree_path);
          requireFact(worktree.startsWith(runtimeRoot + path.sep), 'ownership-mismatch', 'Agent worktree is outside fixture');
          // Root Git reader validates the exact common directory after binding.
          const { execFile } = await import('node:child_process');
          const common = await new Promise<string>((resolve, reject) => execFile(host.gitBinary, ['rev-parse', '--git-common-dir'],
            { cwd: worktree, env: { PATH: host.toolPath }, encoding: 'utf8' }, (error, stdout) => error ? reject(new Error('Owned Git read failed')) : resolve(stdout.trim())));
          const commonDir = await realpath(path.resolve(worktree, common));
          requireFact(commonDir.startsWith(runtimeRoot + path.sep), 'ownership-mismatch', 'Agent Git directory is outside fixture');
          return { row, commonDir, native };
        };
      }
      const owner={leaseId:acquired.lease.id,runId:context.runId,suiteId:context.suiteId,scope:context.scope,caseId:context.caseId,profile:input.profile};
      const route=driver.executionRouting,operationAuthority=driver.createOperationAuthority(owner);
      const rendererTarget=await bindRenderer(isCompose?compose.loom:host.loom,acquired.lease.id,await driver.rendererRuntimeTarget(context.signal),path.join(runtimeRoot,'evidence'),roots);
      const evidenceStore=await createEvidenceStore(path.join(runtimeRoot,'evidence'));
      const workspaceBinding=isCompose?{}:{ownedWorkspaces:await driver.ownedWorkspaceRoster(owner,evidenceStore,context.signal),
        readWorkspaceLegacyAgent:(workspaceId:string,name:string,signal:AbortSignal)=>driver.readWorkspaceLegacyAgent(owner,workspaceId,name,signal)};
      return { ...await driver.runtimeIdentity(context.signal),evidenceClass: route.evidenceClass, roots, secrets: isCompose ? driver.fixtureSecrets : [], rendererTarget, operationAuthority,
        evidenceStore,...workspaceBinding,
        readApi: fixedRead(acquired.apiOrigin), readFiles: fixedRead(acquired.filesOrigin), resolveAgent };
    },
  };
}

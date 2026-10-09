import { randomUUID, createHash } from 'node:crypto';

// Runner-owned configuration and seams are never supplied by suite documents.
export interface Revision {
  repository: string; commit: string; tree: string;
  sourceManifestSha256: string; buildManifestSha256: string;
}
export interface AcquireRequest {
  runId: string; profile: string; loomRevision: Revision; fleetRevision: Revision;
  model: string; maxCases: number; selectionSha256: string;
}
export interface Artifact {
  id: string; sha256: string; bytes: number; mediaType: string; redaction: 'sanitized';
}
export interface Resource {
  id: string; kind: 'directory' | 'lock' | 'ports' | 'compose' | 'process'; generation: string;
}
export interface Inventory {
  complete: boolean; owned: boolean;
  services: { id: string; pid: number; generation: string; state: string }[];
}
export interface FixturePlan {
  profile: string; loomRevision: Revision; fleetRevision: Revision;
  engineRevision: Revision; adapterRevision: Revision;
  model: string; maxCases: number; caseCount: number; selectionSha256: string;
  leaseDurationMs: number;
  // Trusted launcher policy, never acquisition/YAML data. A paid operation
  // requires exact selected backend/model authority in addition to its lease.
  liveProvider?: { backend: 'codex'|'claude'|'cursor'|'opencode'; model: string };
}
export interface FixtureDriver {
  preflight(plan: FixturePlan, signal: AbortSignal): Promise<void>;
  identity(plan: FixturePlan): Promise<boolean>;
  allocate(leaseId: string, runId: string, record: (resource: Resource) => void): Promise<void>;
  provision(plan: FixturePlan, record: (resource: Resource) => void, signal: AbortSignal): Promise<{
    apiOrigin: string; filesOrigin: string; workspaceId: string; repo: string;
  }>;
  inspect(resource: Resource): Promise<Inventory>;
  remove(resource: Resource): Promise<void>;
  artifact(kind: 'acquire' | 'release' | 'observe' | 'failure', value: unknown): Promise<Artifact>;
}
export class FixtureError extends Error {
  constructor(readonly code: 'ownership-mismatch' | 'identity-mismatch' | 'source-mismatch' |
    'unsupported-capability' | 'observation-failed', readonly receipt?: Artifact,
    readonly remainingOwnedResources: readonly string[] = [], readonly leaseId?: string) { super(`Fixture ${code}`); }
}
const digest = (value: unknown) => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const equal = (a: Revision, b: Revision) => ['repository', 'commit', 'tree', 'sourceManifestSha256', 'buildManifestSha256']
  .every(key => a[key as keyof Revision] === b[key as keyof Revision]);
const fail = (condition: unknown, code: FixtureError['code']) => { if (!condition) throw new FixtureError(code); };

// A distinct manager belongs to one runner run. Public lease IDs are opaque;
// authorization is the manager's private map, never an author-supplied token.
export class FixtureLifecycle {
  private readonly leases = new Map<string, {
    request: AcquireRequest; driver: FixtureDriver; resources: Resource[];
    expiresAt: number; released: boolean; busy: boolean;
  }>();
  private readonly plans: ReadonlyMap<string, FixturePlan>;
  private readonly pendingRuns = new Set<string>();
  constructor(plans: readonly FixturePlan[], private readonly driver: () => FixtureDriver,
    private readonly now: () => number = Date.now, private readonly uuid: () => string = randomUUID) {
    this.plans = new Map(plans.map(plan => [plan.profile, structuredClone(plan)]));
    fail(this.plans.size === plans.length, 'identity-mismatch');
  }

  async acquire(input: AcquireRequest, signal: AbortSignal) {
    fail(!this.pendingRuns.has(input.runId), 'ownership-mismatch');
    this.pendingRuns.add(input.runId);
    try { return await this.acquireChecked(input, signal); }
    finally { this.pendingRuns.delete(input.runId); }
  }
  private async acquireChecked(input: AcquireRequest, signal: AbortSignal) {
    // No lease lookup occurs here. Reject selection/cap/source before creating a
    // driver, touching credentials, taking locks, building or launching anything.
    const plan = this.plans.get(input.profile);
    fail(plan, 'unsupported-capability');
    const expected = plan!;
    fail(input.runId.length > 0 && input.runId.length <= 512, 'identity-mismatch');
    fail(equal(input.loomRevision, expected.loomRevision) && equal(input.fleetRevision, expected.fleetRevision), 'source-mismatch');
    fail(input.model === expected.model && input.selectionSha256 === expected.selectionSha256, 'identity-mismatch');
    fail(!expected.liveProvider || expected.liveProvider.model === expected.model, 'identity-mismatch');
    fail(Number.isSafeInteger(input.maxCases) && input.maxCases > 0 && input.maxCases <= expected.maxCases &&
      expected.caseCount > 0 && expected.caseCount <= input.maxCases &&
      (input.profile !== 'agents-real-opencode' || input.maxCases <= 10) &&
      Number.isSafeInteger(expected.leaseDurationMs) && expected.leaseDurationMs > 0, 'identity-mismatch');
    fail(![...this.leases.values()].some(lease => lease.request.runId === input.runId && !lease.released), 'ownership-mismatch');
    signal.throwIfAborted();
    const driver = this.driver();
    await driver.preflight(expected, signal);
    fail(await driver.identity(expected), 'source-mismatch');
    const id = this.uuid();
    fail(!this.leases.has(id), 'ownership-mismatch');
    const lease = { request: structuredClone(input), driver, resources: [] as Resource[],
      expiresAt: this.now() + expected.leaseDurationMs, released: false, busy: true };
    this.leases.set(id, lease);
    const record = (resource: Resource) => {
      const previous = lease.resources.find(existing => existing.id === resource.id);
      if (previous) {
        fail(previous.kind === 'process' && resource.kind === 'process', 'ownership-mismatch');
        previous.generation = resource.generation; return;
      }
      lease.resources.push(structuredClone(resource));
    };
    try {
      await driver.allocate(id, input.runId, record);
      signal.throwIfAborted();
      const endpoints = await driver.provision(expected, record, signal);
      fail(await driver.identity(expected), 'source-mismatch');
      for (const resource of lease.resources) {
        const observed = await driver.inspect(resource);
        fail(observed.complete && observed.owned, 'ownership-mismatch');
      }
      signal.throwIfAborted();
      fail(this.now() < lease.expiresAt, 'identity-mismatch');
      const ownershipArtifact = await driver.artifact('acquire', {
        leaseId: id, runId: input.runId, profile: input.profile, resources: lease.resources,
        loomRevision: input.loomRevision, fleetRevision: input.fleetRevision,
        engineRevision: expected.engineRevision, adapterRevision: expected.adapterRevision,
        model: input.model, selectionSha256: input.selectionSha256, maxCases: input.maxCases,
      });
      return { lease: { id, runId: input.runId, resourceManifestSha256: digest(lease.resources),
        source: input.loomRevision, engine: expected.engineRevision, adapter: expected.adapterRevision,
        expiresAt: new Date(lease.expiresAt).toISOString() }, ...endpoints,
        browserLeaseId: id, ownershipArtifact };
    } catch (error) {
      const cleanup = await this.cleanup(lease);
      const code = error instanceof FixtureError ? error.code : 'observation-failed';
      const receipt = await driver.artifact('failure', { leaseId: id, code, ...cleanup });
      throw new FixtureError(code, receipt, cleanup.remainingOwnedResources, id);
    } finally { lease.busy = false; }
  }

  private owned(leaseId: string, runId: string, allowExpired = false) {
    const lease = this.leases.get(leaseId);
    fail(lease && lease.request.runId === runId && !lease.busy, 'ownership-mismatch');
    fail(allowExpired || (!lease!.released && this.now() < lease!.expiresAt), 'identity-mismatch');
    return lease!;
  }
  private async cleanup(lease: { driver: FixtureDriver; resources: Resource[]; released: boolean }) {
    const remaining: Resource[] = [];
    const failures: { id: string; reason: string }[] = [];
    // If compose teardown fails, keep runtime directories/locks/ports. Releasing
    // them would permit a second run to reuse live or unverified resources.
    let retain = false;
    for (const resource of [...lease.resources].reverse()) {
      try {
        if (retain) { remaining.push(resource); continue; }
        const observed = await lease.driver.inspect(resource);
        fail(observed.complete && observed.owned, 'ownership-mismatch');
        await lease.driver.remove(resource);
      } catch {
        remaining.push(resource); retain = true;
        failures.push({ id: resource.id, reason: 'cleanup-unverified' });
      }
    }
    lease.resources = remaining.reverse();
    lease.released = remaining.length === 0;
    return { released: lease.released, remainingOwnedResources: lease.resources.map(resource => resource.id), failures };
  }
  async release(leaseId: string, runId: string) {
    const lease = this.owned(leaseId, runId, true);
    lease.busy = true;
    try {
      const result = await this.cleanup(lease);
      const receipt = await lease.driver.artifact('release', { leaseId, ...result });
      return { released: result.released, remainingOwnedResources: result.remainingOwnedResources, receipt };
    } finally { lease.busy = false; }
  }
  async observe(leaseId: string, runId: string) {
    const lease = this.owned(leaseId, runId);
    lease.busy = true;
    try {
      const plan = this.plans.get(lease.request.profile)!;
      const sourceMatches = await lease.driver.identity(plan);
      fail(sourceMatches, 'source-mismatch');
      const services: Inventory['services'] = [];
      for (const resource of lease.resources) {
        const observed = await lease.driver.inspect(resource);
        fail(observed.complete && observed.owned, 'ownership-mismatch');
        services.push(...observed.services);
      }
      const inventory = await lease.driver.artifact('observe', { leaseId, resources: lease.resources, services });
      return { owned: true, sourceMatches, services, inventory };
    } finally { lease.busy = false; }
  }
  async dispose(runId: string) {
    const results = [];
    for (const [id, lease] of this.leases) if (lease.request.runId === runId && !lease.released) results.push(await this.release(id, runId));
    return results;
  }
}

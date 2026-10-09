import { z } from 'zod';
import { isDeepStrictEqual } from 'node:util';
import type { CapabilityContext, ImplementationPin } from '@tysonthomas9/aft/capabilities';
import { fixtureOwnerIdentity, getFixtureOperationAuthority, LoomAuthorizedOperation } from '../authority.js';
import { getFixtureEvidenceStore } from '../evidence.js';
import type { EvidenceStore } from '../evidence.js';
import { HostFixtureDriver } from '../fixture/host.js';
import { privateFixtureDriver } from '../fixture/providers.js';
import { defineOperation } from '../operation.js';
import { disposeFixtures, getFixture, type OwnedFixture } from '../ownership.js';
import { HttpResponse, Id, Json, ObservationError, redact } from '../protocol.js';
import { RedactionFacts, redactionFacts } from '../redaction.js';
import { enrollOwnedLegacyAgent, requireOwnedWorkspaceRecord } from '../workspaces.js';
import { LegacyEvidenceClasses, LegacyError } from './operations.js';
import { createTerminalMetadataDetach, TerminalDetachEffects, TerminalDetachFacts, TerminalDetachId, TerminalDetachInput,
  type TerminalMetadataAccess } from './terminal-metadata.js';

const Receipt = z.object({ operation: z.literal(TerminalDetachId), leaseId: Id, runId: Id, invocationId: Id,
  actor: z.literal('runtime-api'), evidence: z.enum(LegacyEvidenceClasses), facts: Json, factsRedaction: RedactionFacts }).strict();
export const TerminalDetachOutput = TerminalDetachFacts.extend({ receipt: Receipt, redaction: RedactionFacts }).strict();
const check = (ok: unknown): void => { if (!ok) throw new LegacyError('ownership-mismatch', 'Terminal fixture identity changed'); };
const operation = () => LoomAuthorizedOperation.parse(TerminalDetachId);

export function createHostTerminalMetadataAccess(fixture: OwnedFixture, driver: HostFixtureDriver, store: EvidenceStore): TerminalMetadataAccess {
  check(driver.workspaceRoot.startsWith(driver.runtimeRoot + '/') && fixture.profile.startsWith('legacy-'));
  const prefix = (workspaceId: string) => `/api/workspaces/${encodeURIComponent(workspaceId)}/terminal/tabs`;
  return {
    async assertOwned(input, call) {
      call.signal.throwIfAborted();
      check(fixture.leaseId === input.leaseId && fixture.runId === call.runId && Date.now() < fixture.expiresAtUtcMs);
      const grant = getFixtureOperationAuthority(fixture, operation(), TerminalDetachEffects);
      const routing = driver.executionRouting;
      check(routing.profile === fixture.profile && routing.evidenceClass === fixture.evidenceClass);
      if (grant.evidenceClass !== routing.evidenceClass)
        throw new LegacyError('source-mismatch', 'Terminal metadata evidence differs from the owned observation route');
      const process = await driver.inspectOwnedProcess('serve', input.expectedServeGeneration, call.signal);
      check(process.state === 'running');
      await fixture.verify(call.signal);
      const record = requireOwnedWorkspaceRecord(fixture, input.workspaceId, 'legacy-agent-name');
      check(record);
      const actual = await driver.ownedWorkspaceRoster(fixtureOwnerIdentity(fixture), store, call.signal);
      const created = actual.find(row => row.workspaceId === input.workspaceId && row.identityKind === 'legacy-agent-name');
      check(created && created.repo === record!.repo && created.commonDir === record!.commonDir &&
        created.storeId === record!.storeId && created.storeGeneration === record!.storeGeneration &&
        isDeepStrictEqual(created.repositories, record!.repositories));
      await enrollOwnedLegacyAgent(fixture, input.workspaceId, input.agentName, call.signal, store);
      check(requireOwnedWorkspaceRecord(fixture, input.workspaceId, 'legacy-agent-name')?.agentIds.includes(input.agentName));
      return { leaseId: fixture.leaseId, runId: fixture.runId, evidenceClass: grant.evidenceClass, secrets: fixture.secrets };
    },
    readTabs: async (input, signal) => HttpResponse.parse(await driver.requestOwnedHttp('api', 'GET', prefix(input.workspaceId), null, signal, input.expectedServeGeneration)),
    deleteCapturedTab: async (input, sessionName, signal) => HttpResponse.parse(await driver.requestOwnedHttp('api', 'DELETE',
      `${prefix(input.workspaceId)}/${encodeURIComponent(sessionName)}`, null, signal, input.expectedServeGeneration)),
  };
}
export type TerminalMetadataAccessFactory = (context: CapabilityContext, fixture: OwnedFixture) => TerminalMetadataAccess;
export const productionTerminalMetadataAccess: TerminalMetadataAccessFactory = (context, fixture) => {
  const driver = privateFixtureDriver(fixture);
  if (!(driver instanceof HostFixtureDriver)) throw new LegacyError('unsupported-capability', 'Fixture has no owned terminal API transport');
  return createHostTerminalMetadataAccess(fixture, driver, getFixtureEvidenceStore(context, fixture.leaseId));
};
export function createTerminalDetachProviders(implementation: ImplementationPin, implementationSha256: string,
  accessFactory: TerminalMetadataAccessFactory = productionTerminalMetadataAccess) {
  const stores = new WeakMap<OwnedFixture, { detach: ReturnType<typeof createTerminalMetadataDetach>; sequence: number }>();
  return [defineOperation({ id: TerminalDetachId, implementation, implementationSha256, inputSchema: TerminalDetachInput,
    outputSchema: TerminalDetachOutput, effects: [...TerminalDetachEffects], evidenceClasses: [...LegacyEvidenceClasses],
    retry: 'never', cleanup: 'release-lease', dispose: disposeFixtures,
    async run(input, context) {
      const fixture = await getFixture(context, input.leaseId);
      const grant = getFixtureOperationAuthority(fixture, operation(), TerminalDetachEffects);
      if (!z.enum(LegacyEvidenceClasses).safeParse(grant.evidenceClass).success)
        throw new ObservationError('source-mismatch', 'Terminal grant has an unsupported evidence class');
      requireOwnedWorkspaceRecord(fixture, input.workspaceId, 'legacy-agent-name');
      await enrollOwnedLegacyAgent(fixture, input.workspaceId, input.agentName, context.signal, getFixtureEvidenceStore(context, fixture.leaseId));
      let state = stores.get(fixture);
      if (!state) { state = { detach: createTerminalMetadataDetach(accessFactory(context, fixture)), sequence: 0 }; stores.set(fixture, state); }
      const call = { runId: context.runId, invocationId: `${context.caseId}:${state.sequence++}`, signal: context.signal };
      try {
        const result = await state.detach(input, call);
        if (result.identity.evidenceClass !== grant.evidenceClass) throw new ObservationError('source-mismatch', 'Terminal evidence differs from the fixture grant');
        const receipt = Receipt.parse({ operation: TerminalDetachId, leaseId: fixture.leaseId, runId: call.runId, invocationId: call.invocationId,
          actor: 'runtime-api' as const, evidence: result.identity.evidenceClass,
          facts: redact(result.rawFacts, fixture.secrets), factsRedaction: redactionFacts(result.rawFacts, fixture.secrets) });
        const raw = { ...result.facts, receipt };
        return { value: { ...raw, redaction: redactionFacts(Json.parse(raw), fixture.secrets) }, evidenceClass: grant.evidenceClass,
          identity: { fixtureLeaseId: fixture.leaseId, workspaceId: input.workspaceId }, secrets: fixture.secrets };
      } catch (error) {
        if (!(error instanceof LegacyError)) throw error;
        throw new ObservationError(error.code === 'ownership-mismatch' ? 'ownership-mismatch' :
          error.code === 'source-mismatch' ? 'source-mismatch' : 'observation-failed', error.message);
      }
    },
  })];
}

import { z } from 'zod';
import type { EvidenceClass } from '@tysonthomas9/aft/types';
import { HttpResponse, Id, Json, ObservationError } from '../protocol.js';
import { LegacyError, type Invocation } from './operations.js';
export { TerminalDetachEffects } from './effects.js';

export const TerminalDetachId = 'loom.runtime.detachTerminal' as const;
// DELETE attempts the product's best-effort Kill. Its metadata acknowledgment
// cannot establish process exit, even when the reread contains no matching tab.
const Name = z.string().regex(/^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$/);
// Frozen tabmeta.ValidateSessionName accepts this exact alphabet.
const Session = z.string().min(1).max(256).regex(/^[A-Za-z0-9_-]+$/);
export const TerminalDetachInput = z.object({ leaseId: Id, workspaceId: Name, agentName: Name,
  expectedServeGeneration: Id }).strict();
export type TerminalDetachInput = z.infer<typeof TerminalDetachInput>;
export const TerminalDetachFacts = z.object({ kind: z.literal('terminal-metadata-detached'), workspaceId: Name, agentName: Name,
  serve: z.object({ id: z.literal('serve'), generation: Id }).strict(),
  capturedSessions: z.array(Session).max(128),
  deletions: z.array(z.object({ sessionName: Session, status: z.number().int().min(100).max(599).nullable(),
    outcome: z.enum(['http-success', 'http-failure', 'transport-error']) }).strict()).max(128),
  remainingSessions: z.array(Session).max(128), complete: z.literal(true) }).strict();
export type TerminalDetachFacts = z.infer<typeof TerminalDetachFacts>;
export interface TerminalMetadataIdentity { leaseId: string; runId: string; evidenceClass: EvidenceClass; secrets: readonly string[] }
/** Private fixed endpoints, supplied by the fixture owner; no suite path/body/session parameter. */
export interface TerminalMetadataAccess {
  assertOwned(input: TerminalDetachInput, call: Invocation): Promise<TerminalMetadataIdentity>;
  readTabs(input: TerminalDetachInput, signal: AbortSignal): Promise<HttpResponse>;
  deleteCapturedTab(input: TerminalDetachInput, sessionName: string, signal: AbortSignal): Promise<HttpResponse>;
}
const invalid = (): never => { throw new LegacyError('response-invalid', 'Owned terminal metadata is incomplete'); };
function reachable(response: HttpResponse): HttpResponse {
  const wire = HttpResponse.safeParse(response);
  if (!wire.success || wire.data.status < 200 || wire.data.status >= 300) return invalid();
  return wire.data;
}
function sessions(response: HttpResponse, agentName: string): string[] {
  const payload = reachable(response).body;
  const tabs = Array.isArray(payload) ? payload : payload && typeof payload === 'object' ? payload.data : undefined;
  if (!Array.isArray(tabs)) return invalid();
  if (tabs.length > 1000 || !Array.isArray(payload) && payload && typeof payload === 'object' &&
    (payload.has_more === true || payload.total !== undefined && payload.total !== tabs.length))
    throw new ObservationError('incomplete-pages', 'Terminal tab listing is incomplete or exceeds its bound');
  const result: string[] = [];
  for (const tab of tabs) {
    if (!tab || typeof tab !== 'object' || Array.isArray(tab) || tab.agent_id !== agentName || !tab.session_name) continue;
    const session = Session.safeParse(tab.session_name);
    if (!session.success) return invalid();
    if (result.length === 128) throw new ObservationError('incomplete-pages', 'Matching terminal tabs exceed the mutation bound');
    result.push(session.data);
  }
  return result;
}
export function createTerminalMetadataDetach(access: TerminalMetadataAccess) {
  const attempted = new Set<string>();
  return async (raw: unknown, call: Invocation) => {
    const parsed = TerminalDetachInput.safeParse(raw);
    if (!parsed.success || !call.invocationId) throw new LegacyError('invalid-input', 'Terminal detach input is invalid');
    const input = parsed.data;
    const owned = async () => {
      call.signal.throwIfAborted();
      const identity = await access.assertOwned(input, call);
      if (identity.leaseId !== input.leaseId || identity.runId !== call.runId)
        throw new LegacyError('ownership-mismatch', 'Terminal fixture belongs to another invocation');
      return identity;
    };
    const identity = await owned();
    const key = `${input.leaseId}\0${call.invocationId}`;
    if (attempted.has(key)) throw new LegacyError('mutation-repeated', 'Terminal detach invocation was already attempted');
    // Frozen helper first probes reachability, then takes a fresh matching list.
    reachable(await access.readTabs(input, call.signal));
    await owned();
    const capturedSessions = sessions(await access.readTabs(input, call.signal), input.agentName);
    attempted.add(key);
    const deletions: TerminalDetachFacts['deletions'] = [];
    for (const sessionName of capturedSessions) {
      await owned();
      let response: HttpResponse;
      try { response = await access.deleteCapturedTab(input, sessionName, call.signal); }
      catch {
        // Retain an uncertain attempt; never retry it or infer successful Kill.
        await owned();
        deletions.push({ sessionName, status: null, outcome: 'transport-error' });
        continue;
      }
      const wire = HttpResponse.safeParse(response);
      if (!wire.success) return invalid();
      deletions.push({ sessionName, status: wire.data.status,
        outcome: wire.data.status >= 200 && wire.data.status < 300 ? 'http-success' : 'http-failure' });
    }
    await owned();
    const remainingSessions = sessions(await access.readTabs(input, call.signal), input.agentName);
    await owned();
    const facts = TerminalDetachFacts.parse({ kind: 'terminal-metadata-detached', workspaceId: input.workspaceId,
      agentName: input.agentName, serve: { id: 'serve', generation: input.expectedServeGeneration },
      capturedSessions, deletions, remainingSessions, complete: true });
    // The suite, rather than this operation, owns all-success/zero-remaining predicates.
    return { facts, identity, rawFacts: Json.parse(facts) };
  };
}

import { probeOccurrences, type SyntheticProbe } from './synthetic-probe.js';
import { z } from 'zod';
import { RedactionFacts, redactionFacts } from './redaction.js';
import { EventPage, Id, Json, ObservationError, requireFact, type Event, type ReadTransport } from './protocol.js';

export const SavedEventsInput = z.object({
  agent: z.object({ fixtureLeaseId: Id, workspaceId: Id, agentId: Id }).strict(),
  after: z.number().int().nonnegative(), pageSize: z.number().int().min(1).max(500),
  snapshotSeq: z.number().int().nonnegative().optional(),
  maxPages: z.number().int().min(1).max(1000), maxRecords: z.number().int().min(1).max(100000),
  kinds: z.array(Id).max(100), probeHandle: Id.nullable().optional(),
}).strict();
export const SavedEventsOutput = z.object({
  snapshotSeq: z.number().int().nonnegative(), after: z.number().int().nonnegative(),
  complete: z.literal(true), events: z.array(z.object({
    agentId: Id, seq: z.number().int().positive(), eventId: Id, kind: Id,
    turnId: z.string().nullable(), payload: Json, createdAt: Id, redaction: RedactionFacts,
    probe: z.object({ handle: Id, payloadOccurrences: z.number().int().nonnegative() }).strict().nullable(),
  }).strict()),
}).strict();
export async function collectSavedEvents(
  input: z.infer<typeof SavedEventsInput>, read: ReadTransport, signal: AbortSignal, probe?: SyntheticProbe, secrets: readonly string[] = [],
): Promise<z.infer<typeof SavedEventsOutput>> {
  requireFact(!input.probeHandle || probe?.handle === input.probeHandle, 'ownership-mismatch', 'Saved-event synthetic probe is not bound');
  requireFact(input.snapshotSeq === undefined || input.snapshotSeq >= input.after,
    'identity-mismatch', 'Saved event cursor exceeds its captured snapshot');
  const events: Event[] = [];
  let cursor = input.after;
  let boundary: number | undefined = input.snapshotSeq;
  const ids = new Set<string>();
  for (let n = 0; n < input.maxPages; n++) {
    signal.throwIfAborted();
    const params = new URLSearchParams({ after: String(cursor), limit: String(input.pageSize) });
    if (boundary !== undefined) params.set('snapshot', String(boundary));
    if (input.kinds.length) params.set('kind', input.kinds.join(','));
    const response = await read(`/api/workspaces/${encodeURIComponent(input.agent.workspaceId)}/v1/agents/${encodeURIComponent(input.agent.agentId)}/events?${params}`, signal);
    requireFact(response.status === 200, 'observation-failed', 'Saved events are unreadable');
    const parsed = EventPage.safeParse(response.body);
    requireFact(parsed.success, 'observation-failed', 'Saved events response is malformed');
    const page = parsed.data;
    boundary ??= page.snapshot_seq;
    requireFact(page.snapshot_seq === boundary, 'identity-mismatch', 'Saved event snapshot changed');
    requireFact(page.events.length <= input.pageSize, 'incomplete-pages', 'Saved event page exceeds its bound');
    for (const event of page.events) {
      requireFact(event.agent_id === input.agent.agentId, 'ownership-mismatch', 'Foreign saved event');
      requireFact(event.seq > cursor && event.seq <= boundary && !ids.has(event.event_id), 'identity-mismatch', 'Duplicate, stale or reordered saved event');
      // Unfiltered persisted seqs are allocated contiguously. A missing prefix or
      // middle event cannot prove absence even if a server claims more=false.
      if (!input.kinds.length) requireFact(event.seq === cursor + 1, 'incomplete-pages', 'Saved event sequence is incomplete');
      else requireFact(input.kinds.includes(event.kind), 'identity-mismatch', 'Unexpected filtered event kind');
      cursor = event.seq; ids.add(event.event_id); events.push(event);
      requireFact(events.length <= input.maxRecords, 'incomplete-pages', 'Saved event record bound reached');
    }
    requireFact(page.next === cursor, 'identity-mismatch', 'Saved event cursor does not match page');
    if (!page.more) {
      if (!input.kinds.length) requireFact(cursor === Math.max(input.after, boundary), 'incomplete-pages', 'Saved event tail is missing');
      return { snapshotSeq: boundary, after: input.after, complete: true, events: events.map(event => ({
        agentId: event.agent_id, seq: event.seq, eventId: event.event_id, kind: event.kind,
        turnId: event.turn_id, payload: event.payload, createdAt: event.created_at, redaction: redactionFacts(event.payload, secrets),
        probe: probe ? { handle: probe.handle, payloadOccurrences: probeOccurrences(JSON.stringify(event.payload), probe) } : null,
      })) };
    }
    requireFact(page.events.length > 0 && cursor < boundary, 'incomplete-pages', 'Saved event cursor cannot advance');
  }
  throw new ObservationError('incomplete-pages', 'Saved event page bound reached');
}

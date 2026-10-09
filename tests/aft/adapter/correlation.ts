import type { SyntheticProbe } from './synthetic-probe.js';
import { z } from 'zod';
import { Id, Json, requireFact } from './protocol.js';
import { SavedEventsInput, SavedEventsOutput, collectSavedEvents } from './events.js';
import type { ReadTransport } from './protocol.js';
export const CorrelationInput = SavedEventsInput.extend({
  eventIds: z.array(Id).min(1).max(100), turnId: Id, requestId: Id.nullable(), itemId: Id.nullable(),
}).strict();
export const CorrelationOutput = z.object({ turnId: Id, requestId: Id.nullable(), itemId: Id.nullable(),
  snapshotSeq: z.number().int().nonnegative(), events: SavedEventsOutput.shape.events,
}).strict();
/** A bounded read selector binds exact source IDs. It does not evaluate any
 * scenario outcome, cancellation/tool-success count, or expected text. */
export async function correlateEvents(input: z.infer<typeof CorrelationInput>, read: ReadTransport, signal: AbortSignal, probe?: SyntheticProbe) {
  requireFact(new Set(input.eventIds).size === input.eventIds.length, 'identity-mismatch', 'Duplicate event correlation IDs');
  const history = await collectSavedEvents(input, read, signal, probe);
  const events = input.eventIds.map(id => {
    const matches = history.events.filter(event => event.eventId === id);
    requireFact(matches.length === 1, 'identity-mismatch', 'Requested saved event is missing or duplicated');
    const event = matches[0]!;
    requireFact(event.turnId === input.turnId, 'identity-mismatch', 'Saved event belongs to a different turn');
    const payload = Json.parse(event.payload);
    if (input.requestId !== null || input.itemId !== null) {
      requireFact(payload && typeof payload === 'object' && !Array.isArray(payload), 'identity-mismatch', 'Saved correlation payload is missing');
      if (input.requestId !== null) requireFact(payload.requestId === input.requestId, 'identity-mismatch', 'Saved event belongs to a different request');
      if (input.itemId !== null) requireFact(payload.itemId === input.itemId, 'identity-mismatch', 'Saved event belongs to a different item');
    }
    return event;
  });
  return { turnId: input.turnId, requestId: input.requestId, itemId: input.itemId, snapshotSeq: history.snapshotSeq, events };
}

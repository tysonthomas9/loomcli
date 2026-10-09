import { z } from 'zod';
import { AgentRef, Digest, Id, Json, requireFact, sha256, type AgentRow, type NativeAccess } from './protocol.js';
import { observeNative, verifyNativeService } from './native.js';
export const FailureInput = z.object({ agent: AgentRef, nativeSessionId: Id, nativeRoot: z.string(), expectedGeneration: Id,
  maxEvents: z.number().int().min(1).max(500),
}).strict();
export const FailureOutput = z.object({ agentId: Id, nativeSessionId: Id, nativeRoot: z.string(), serviceGeneration: Id,
  watermark: z.number().int().nonnegative(), eventIds: z.array(Id), failures: z.array(z.object({
    eventId: Id, seq: z.number().int().nonnegative(), category: Id, status: z.number().int().min(100).max(599).nullable(),
    messageBytes: z.number().int().nonnegative(), messageSha256: Digest,
  }).strict()),
}).strict();
const categories = new Set(['provider.rate-limit', 'provider.auth', 'provider.quota', 'provider.content-filter', 'provider.transport',
  'provider.internal', 'provider.invalid-output', 'provider.invalid-request', 'provider.unsupported-operation', 'provider.no-route',
  'provider.unknown', 'provider.timeout', 'permission.rejected', 'tool.execution', 'unknown']);
export async function projectNativeLog(raw: string, nativeSessionId: string, maxEvents: number) {
  requireFact(Buffer.byteLength(raw) <= 262144 && /\r?\n\r?\n$/.test(raw), 'incomplete-pages', 'Native log is truncated or over bound');
  const frames = raw.split(/\r?\n\r?\n/).filter(Boolean);
  requireFact(frames.length <= maxEvents + 1, 'incomplete-pages', 'Native log event bound reached');
  const events: { id: string; type: string; seq: number; data: Json }[] = [];
  let watermark: number | undefined;
  for (const frame of frames) {
    const lines = frame.split(/\r?\n/).filter(line => line.startsWith('data:')).map(line => line.slice(5).trimStart());
    if (!lines.length) continue;
    const data = JSON.parse(lines.join('\n')) as unknown;
    const sync = z.object({ type: z.literal('log.synced'), aggregateID: z.literal(nativeSessionId), seq: z.number().int().nonnegative() }).passthrough().safeParse(data);
    if (sync.success) {
      requireFact(watermark === undefined, 'identity-mismatch', 'Native log watermark duplicated'); watermark = sync.data.seq; continue;
    }
    requireFact(watermark === undefined, 'identity-mismatch', 'Native durable event follows watermark');
    const event = z.object({ id: Id, type: Id, durable: z.object({ aggregateID: Id, seq: z.number().int().nonnegative() }).passthrough(), data: Json }).passthrough().parse(data);
    requireFact(event.durable.aggregateID === nativeSessionId && event.durable.seq === events.length,
      'identity-mismatch', 'Native log has foreign, missing or reordered events');
    if (event.data && typeof event.data === 'object' && !Array.isArray(event.data) && event.data.sessionID !== undefined)
      requireFact(event.data.sessionID === nativeSessionId, 'identity-mismatch', 'Foreign native log session');
    requireFact(!events.some(prior => prior.id === event.id), 'identity-mismatch', 'Duplicate native event ID');
    events.push({ id: event.id, type: event.type, seq: event.durable.seq, data: event.data });
  }
  requireFact(events.length > 0 && events[0]!.type === 'session.created' && watermark === events.at(-1)!.seq,
    'incomplete-pages', 'Native log prefix or terminal watermark is missing');
  const failures: z.infer<typeof FailureOutput>['failures'] = [];
  for (const event of events.filter(event => event.type === 'session.execution.failed')) {
    const parsed = z.object({ error: z.object({ type: Id, message: z.string(), status: z.number().int().min(100).max(599).optional() }).passthrough() }).passthrough().parse(event.data);
    requireFact(categories.has(parsed.error.type), 'observation-failed', 'Native failure category is unsupported');
    const message = parsed.error.message || parsed.error.type;
    failures.push({ eventId: event.id, seq: event.seq, category: parsed.error.type, status: parsed.error.status ?? null,
      messageBytes: Buffer.byteLength(message), messageSha256: await sha256(message) });
  }
  return { watermark: watermark!, eventIds: events.map(event => event.id), failures };
}
export async function observeNativeFailure(input: z.infer<typeof FailureInput>, access: NativeAccess, row: AgentRow, signal: AbortSignal) {
  requireFact(access.log, 'unsupported-capability', 'Native durable log observation is unavailable');
  await observeNative({ ...input, view: 'session', maxMessages: 200 }, access, row, signal);
  const before = await verifyNativeService(access, input.expectedGeneration, signal);
  const facts = await projectNativeLog(await access.log(input.nativeSessionId, signal), input.nativeSessionId, input.maxEvents);
  const after = await verifyNativeService(access, input.expectedGeneration, signal);
  requireFact(before.pid === after.pid && before.endpointId === after.endpointId && before.generation === after.generation,
    'identity-mismatch', 'Native process changed during log observation');
  return { agentId: row.agent_id, nativeSessionId: input.nativeSessionId, nativeRoot: input.nativeRoot, serviceGeneration: after.generation, ...facts };
}

import { z } from 'zod';
import { Id } from '../protocol.js';
import { NativeSessionQuery, NativeSessionQueryReply } from '../native-session-query.js';
import { FixtureError } from './lifecycle.js';
import { NativeReplyFrame, NativeSessionBudget, NativeSessionLimits } from './native-session-boundaries.js';
import type { captureNativeQuerySession } from './native-session.js';
import type { NativeSessionPipe } from './native-session-pipe.js';

const sequence = z.number().int().min(1).max(NativeSessionLimits.frames);
// Private transport control only. Domain queries/replies have ONE canonical
// schema in root; neither this envelope nor a sequence grants authority.
export const NativeSessionWireRequest = z.union([
  z.object({ sequence, query: NativeSessionQuery }).strict(),
  z.object({ sequence, workspaceBindingId: Id, shutdown: z.literal(true) }).strict(),
]);
export const NativeSessionWireReply = z.union([
  z.object({ sequence, reply: NativeSessionQueryReply }).strict(),
  z.object({ sequence, workspaceBindingId: Id, closed: z.literal(true) }).strict(),
]);
const deny = (): never => { throw new FixtureError('observation-failed'); };
const decode = (bytes: Uint8Array): unknown => JSON.parse(new TextDecoder('utf-8', { fatal: true, ignoreBOM: true }).decode(bytes));
const encode = (value: unknown) => Buffer.from(JSON.stringify(value));

/** Exactly one request. Reject its smaller header bound BEFORE the shared
 * decoder can allocate; reuse its UTF8/node/depth/trailing-byte checks. The
 * fixed stream owner must also count raw rejected/idle input and retain EOF. */
export class NativeRequestFrame {
  private readonly header = Buffer.alloc(4);
  private readonly frame = new NativeReplyFrame();
  private headerBytes = 0;
  private failed = false;
  push(chunk: Uint8Array): Buffer | undefined {
    try {
      if (this.failed) deny();
      if (this.headerBytes === 4) return this.frame.push(chunk);
      const count = Math.min(4 - this.headerBytes, chunk.byteLength);
      this.header.set(chunk.subarray(0, count), this.headerBytes); this.headerBytes += count;
      if (this.headerBytes < 4) return;
      const bytes = this.header.readUInt32BE();
      if (!bytes || bytes + 4 > NativeSessionLimits.request) deny();
      this.frame.push(this.header);
      return this.frame.push(chunk.subarray(count));
    } catch (error) { this.failed = true; throw error; }
  }
  end() { if (this.failed) deny(); this.frame.end(); }
}

type QueryOwner = Awaited<ReturnType<typeof captureNativeQuerySession>>;
export type NativeSessionWireVerify = (signal: AbortSignal, phase: 'active' | 'descriptors-closed') => Promise<void>;
/** Fixed inside-helper handler. Caller owns actual stdin/stdout, process exit,
 * sealed build and namespace/kernel binding; this never starts a process.
 * Each body has already passed NativeRequestFrame. A closing ack is written
 * only AFTER the retained query owner has closed all its descriptors. */
export function createNativeSessionWireHandler(workspaceBindingId: string, owner: QueryOwner,
  verify: NativeSessionWireVerify, writeReply: (frame: Buffer, signal: AbortSignal) => Promise<void>) {
  Id.parse(workspaceBindingId);
  const budget = new NativeSessionBudget();
  let next = 1, active = false, terminal = false;
  return Object.freeze({
    async handle(payload: Uint8Array, signal: AbortSignal) {
      signal.throwIfAborted();
      if (terminal || active) deny();
      active = true;
      try {
        if (payload.byteLength + 4 > NativeSessionLimits.request) deny();
        const raw = decode(payload);
        const shutdown = typeof raw === 'object' && raw !== null && 'shutdown' in raw && raw.shutdown === true;
        budget.request(payload, shutdown);
        const request = NativeSessionWireRequest.parse(raw);
        if (request.sequence !== next || ('query' in request ? request.query.workspaceBindingId : request.workspaceBindingId) !== workspaceBindingId) deny();
        next++;
        await verify(signal, 'active'); signal.throwIfAborted();
        const value = 'shutdown' in request ? (await owner.dispose(), { sequence: request.sequence, workspaceBindingId, closed: true as const }) :
          { sequence: request.sequence, reply: await owner.query(request.query, signal) };
        // Shutdown intentionally cannot re-read the now-closed store; retained
        // helper identity/actual exit belongs to the enclosing stream owner.
        await verify(signal, shutdown ? 'descriptors-closed' : 'active');
        signal.throwIfAborted();
        const bytes = encode(NativeSessionWireReply.parse(value));
        if (bytes.length + 4 > NativeSessionLimits.reply) deny();
        const frame = Buffer.alloc(bytes.length + 4); frame.writeUInt32BE(bytes.length); frame.set(bytes, 4);
        budget.stdout(frame);
        await writeReply(frame, signal); signal.throwIfAborted();
        await verify(signal, shutdown ? 'descriptors-closed' : 'active'); signal.throwIfAborted();
        budget.completeReply(); if (shutdown) terminal = true;
      } catch (error) { terminal = true; budget.invalidate(); throw error; }
      finally { active = false; }
    },
    receipt: () => budget.receipt(),
  });
}

/** Canonical query facade over the approved fixed pipe. The exact issued
 * binding and helper/container/kernel verification are trusted owner ports,
 * never caller-selected paths or a substitute for continuous creator proof.
 * Errors are terminal; there is no retry or sequence reset. Cleanup remains
 * the already-enrolled pipe and helper owners' responsibility. */
export function createNativeSessionWireClient(workspaceBindingId: string, pipe: Pick<NativeSessionPipe, 'exchange'>,
  verify: NativeSessionWireVerify) {
  Id.parse(workspaceBindingId);
  let next = 1, active = false, terminal = false;
  const exchange = async (query: NativeSessionQuery | undefined, signal: AbortSignal) => {
    signal.throwIfAborted(); if (terminal || active) deny();
    active = true;
    const sentSequence = next++;
    try {
      const request = NativeSessionWireRequest.parse(query ? { sequence: sentSequence, query } :
        { sequence: sentSequence, workspaceBindingId, shutdown: true });
      await verify(signal, 'active'); signal.throwIfAborted();
      const bytes = await pipe.exchange(encode(request), signal, !query);
      signal.throwIfAborted();
      if (bytes.byteLength + 4 > NativeSessionLimits.reply) deny();
      const reply = NativeSessionWireReply.parse(decode(bytes));
      if (reply.sequence !== sentSequence || (query ? !('reply' in reply) || reply.reply.workspaceBindingId !== workspaceBindingId ||
        reply.reply.operation !== query.operation : !('closed' in reply) || reply.workspaceBindingId !== workspaceBindingId)) deny();
      await verify(signal, query ? 'active' : 'descriptors-closed');
      signal.throwIfAborted(); if (!query) terminal = true;
      return reply;
    } catch (error) { terminal = true; throw error; }
    finally { active = false; }
  };
  return Object.freeze({
    async query(raw: unknown, signal: AbortSignal): Promise<NativeSessionQueryReply> {
      const query = NativeSessionQuery.parse(raw);
      if (query.workspaceBindingId !== workspaceBindingId) deny();
      const reply = await exchange(query, signal); if (!('reply' in reply)) return deny(); return reply.reply;
    },
    async shutdown(signal: AbortSignal) { return exchange(undefined, signal); },
  });
}

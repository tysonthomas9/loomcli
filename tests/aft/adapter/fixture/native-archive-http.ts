import type { AgentRef } from '../protocol.js';
import { FixtureError } from './lifecycle.js';

/** Fixed wire transport only. The owning callback must authenticate the current
 * actor and hold its serve/container reservation before calling this port. */
export type NativeArchiveHttp = (origin: string, agent: AgentRef, idempotencyKey: string,
  signal: AbortSignal) => Promise<number>;

const check = (value: unknown) => { if (!value) throw new FixtureError('identity-mismatch'); };

export function createNativeArchiveHttp(fetchPort: typeof fetch = fetch,
  timeout: (milliseconds: number) => AbortSignal = milliseconds => AbortSignal.timeout(milliseconds)): NativeArchiveHttp {
  return async (origin, agent, idempotencyKey, signal) => {
    signal.throwIfAborted();
    const base = new URL(origin);
    check(base.protocol === 'http:' && base.hostname === '127.0.0.1' && base.port &&
      !base.username && !base.password && base.pathname === '/' && !base.search && !base.hash);
    check(agent.workspaceId && !/[\/\\\s\u0000]/.test(agent.workspaceId) &&
      agent.workspaceId !== '.' && agent.workspaceId !== '..' && /^agt_[A-Za-z0-9_-]+$/.test(agent.agentId));
    // Reject header normalization rather than changing the retained key.
    check(idempotencyKey && /^[\x21-\x7e]+$/.test(idempotencyKey));
    const headers = new Headers({ 'Content-Type': 'application/json', 'Idempotency-Key': idempotencyKey });
    check(headers.get('Idempotency-Key') === idempotencyKey);
    const bounded = AbortSignal.any([signal, timeout(15000)]);
    bounded.throwIfAborted();
    const route = `/api/workspaces/${encodeURIComponent(agent.workspaceId)}/v1/agents/${encodeURIComponent(agent.agentId)}/archive`;
    const response = await fetchPort(new URL(route, base), {
      method: 'POST', headers, body: '{"cancel":true}', redirect: 'error', signal: bounded,
    });
    try {
      bounded.throwIfAborted();
      check(Number.isInteger(response.status) && response.status >= 100 && response.status <= 599);
      return response.status;
    } finally {
      // The source helper closes the response without parsing its body. Keep
      // cancellation effective through the awaited response cleanup as well.
      await response.body?.cancel();
      bounded.throwIfAborted();
    }
  };
}

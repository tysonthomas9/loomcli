/**
 * Terminal lifecycle configuration advertised by the server.
 *
 * Fetched once at app load (promise is cached) and read by TerminalInstance
 * to decide how long to keep auto-retrying a dropped WebSocket. Values of
 * zero mean "disabled" — the server never auto-kills detached sessions.
 *
 * Local `loom serve` returns 0 / 0. Remote `loom-agentd` (persistent agents
 * inside Firecracker) returns non-zero values; the frontend shrinks the
 * retry window to stay ≤ the server's grace period so the client doesn't
 * give up while the server is still holding the shell open.
 */

import { get, unwrapResponse } from "./client";

export interface TerminalLifecycleConfig {
  /** Kill a detached session after this many ms with no reattach. 0 = disabled. */
  gracePeriodMs: number;
  /** Kill a session after this many ms with no output + no WS. 0 = disabled. */
  idleTimeoutMs: number;
  /** Concurrent-session cap (attached + detached). */
  maxSessions: number;
}

interface WireData {
  grace_period_ms: number;
  idle_timeout_ms: number;
  max_sessions: number;
}

let cached: Promise<TerminalLifecycleConfig> | undefined;

/**
 * Fetch terminal lifecycle config. Result is memoised for the app's lifetime
 * — the value is effectively static per server build. Failures fall back to
 * sane defaults (no timeout) so the UI stays functional.
 */
export function getTerminalConfig(): Promise<TerminalLifecycleConfig> {
  if (!cached) {
    cached = get<{ success: boolean; data?: WireData; error?: string }>(
      "/api/config/terminal",
    )
      .then((envelope) => {
        const data = unwrapResponse<WireData>(envelope);
        return {
          gracePeriodMs: data?.grace_period_ms ?? 0,
          idleTimeoutMs: data?.idle_timeout_ms ?? 0,
          maxSessions: data?.max_sessions ?? 0,
        };
      })
      .catch(() => ({ gracePeriodMs: 0, idleTimeoutMs: 0, maxSessions: 0 }));
  }
  return cached;
}

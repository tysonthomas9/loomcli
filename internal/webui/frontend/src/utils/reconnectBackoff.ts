/**
 * WebSocket reconnection with exponential backoff.
 * Pure utility — no React dependency.
 */

export interface ReconnectConfig {
  /** Base delay in milliseconds (default: 1000). */
  baseDelay: number;
  /** Maximum delay in milliseconds (default: 30000). */
  maxDelay: number;
  /** Maximum number of reconnection attempts (default: 10). */
  maxAttempts: number;
  /** Jitter factor — delay is multiplied by (1-jitter/2) to (1+jitter/2) (default: 0.5 for ±25%). */
  jitterFactor: number;
}

export const DEFAULT_RECONNECT_CONFIG: ReconnectConfig = {
  baseDelay: 1000,
  maxDelay: 30000,
  maxAttempts: 10,
  jitterFactor: 0.5,
};

export interface ReconnectState {
  /** Current attempt number (0-indexed). */
  attempt: number;
  /** Timestamp (ms since epoch) of the next retry, or null if not scheduled. */
  nextRetryAt: number | null;
  /** True if max attempts exhausted. */
  gaveUp: boolean;
}

/**
 * Calculate the backoff delay for a given attempt number.
 * Formula: min(baseDelay * 2^attempt, maxDelay) * jitter
 * where jitter is in range [1 - jitterFactor/2, 1 + jitterFactor/2].
 */
export function calculateBackoffDelay(
  attempt: number,
  config: ReconnectConfig = DEFAULT_RECONNECT_CONFIG,
): number {
  const raw = Math.min(config.baseDelay * 2 ** attempt, config.maxDelay);
  const jitterMin = 1 - config.jitterFactor / 2;
  const jitterMax = 1 + config.jitterFactor / 2;
  return raw * (jitterMin + Math.random() * (jitterMax - jitterMin));
}

/**
 * Start an automatic reconnection loop with exponential backoff.
 *
 * @param connectFn - Called on each reconnection attempt. Return true if connection succeeded, false otherwise. May return a Promise for async connection attempts.
 * @param onStateChange - Called whenever the reconnect state changes.
 * @param config - Backoff configuration.
 * @returns A cancel function that stops the reconnection loop.
 */
export function startAutoReconnect(
  connectFn: () => boolean | Promise<boolean>,
  onStateChange: (state: ReconnectState) => void,
  config: ReconnectConfig = DEFAULT_RECONNECT_CONFIG,
): () => void {
  let cancelled = false;
  let timerId: ReturnType<typeof setTimeout> | null = null;
  let attempt = 0;

  function scheduleNext(): void {
    if (cancelled) return;

    if (attempt >= config.maxAttempts) {
      onStateChange({ attempt, nextRetryAt: null, gaveUp: true });
      return;
    }

    const delay = calculateBackoffDelay(attempt, config);
    const nextRetryAt = Date.now() + delay;

    onStateChange({ attempt, nextRetryAt, gaveUp: false });

    timerId = setTimeout(() => {
      if (cancelled) return;
      timerId = null;

      Promise.resolve(connectFn()).then((succeeded) => {
        if (cancelled) return;
        if (!succeeded) {
          attempt++;
          scheduleNext();
        }
      });
    }, delay);
  }

  // Start the first scheduled attempt
  scheduleNext();

  return () => {
    cancelled = true;
    if (timerId !== null) {
      clearTimeout(timerId);
      timerId = null;
    }
  };
}

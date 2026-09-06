/**
 * RFC 7231 `Retry-After` parsing.
 *
 * The Go server propagates the upstream header verbatim, which per the RFC
 * may be either delta-seconds or an HTTP-date. The client is where it becomes
 * a number, so both forms are handled here.
 */

/** Upper bound on an honored wait, so a hostile or buggy upstream cannot freeze the UI. */
const MAX_RETRY_AFTER_MS = 300_000;

/**
 * Parse a `Retry-After` header value into milliseconds.
 *
 * Returns `undefined` for an absent, empty, negative or unparseable value —
 * callers then fall back to their own backoff. Results are clamped to
 * `[0, 300_000]`; a date in the past clamps to 0.
 */
export function parseRetryAfter(value: string | null): number | undefined {
  if (value == null) return undefined;
  const trimmed = value.trim();
  if (trimmed === "") return undefined;

  // delta-seconds. A negative delta is malformed, not "retry now": reject it
  // here rather than letting Date.parse read "-5" as a year.
  if (/^[+-]?\d+$/.test(trimmed)) {
    const seconds = Number(trimmed);
    if (seconds < 0) return undefined;
    return Math.min(seconds * 1000, MAX_RETRY_AFTER_MS);
  }

  // HTTP-date
  const parsed = Date.parse(trimmed);
  if (!Number.isNaN(parsed)) {
    const delta = parsed - Date.now();
    return Math.min(Math.max(delta, 0), MAX_RETRY_AFTER_MS);
  }

  return undefined;
}

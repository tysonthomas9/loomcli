// Wire layer: every HTTP exchange is RECORDED before it is interpreted.
//
// The ordering rule is load-bearing. If we parsed first and recorded second, a body
// that fails to parse would produce an exception with no transcript -- exactly the
// failure mode that leaves you debugging blind. Capture raw text, then parse.

import type { Service } from "./contract.ts";

export type Exchange = {
  seq: number;
  service: Service;
  method: string;
  url: string;
  pathname: string;
  requestBody: unknown;
  status: number;
  contentType: string;
  rawBody: string;
  body: unknown;
  parseError: string | null;
  durationMs: number;
  checkpoint: string | null;
  /** Rate-limit retries this exchange needed. Recorded, never hidden. */
  retries: number;
  /** True when a 5xx was really a rate limit wearing a different status. */
  rateLimitMasked: boolean;
};

export type Recorder = {
  exchanges: Exchange[];
  currentCheckpoint: string | null;
};

export function newRecorder(): Recorder {
  return { exchanges: [], currentCheckpoint: null };
}

export type CallOpts = {
  service: Service;
  baseUrl: string;
  method: string;
  path: string;
  body?: unknown;
  headers?: Record<string, string>;
  /** Hard ceiling. Never unbounded: some endpoints long-poll or stream forever. */
  timeoutMs?: number;
};

export const DEFAULT_TIMEOUT_MS = 10_000;
const MAX_RATE_LIMIT_RETRIES = 5;

/**
 * fleet-db rate-limits per IP (default 100/s burst 200), and loom's embedded fleet-db
 * shares that bucket with us. loom then RELABELS fleet-db's 429 as a 503 with
 * `kind: "unavailable"` -- dropping the status and the Retry-After header fleet-db set.
 *
 * That matters more than it looks: `PC-NO-5XX` deliberately treats 503+kind:unavailable
 * as declared degradation, so without this detector a fully rate-limited backend sails
 * through every postcondition and the only symptom is phantom oracle failures.
 */
export function isRateLimited(status: number, raw: string): boolean {
  if (status === 429) return true;
  return status === 503 && /rate.?limit/i.test(raw);
}

function retryDelayMs(attempt: number): number {
  return Math.min(2_000, 150 * 2 ** attempt);
}

export async function call(rec: Recorder, o: CallOpts): Promise<Exchange> {
  const url = `${o.baseUrl}${o.path}`;
  const headers: Record<string, string> = {
    accept: "application/json",
    "x-actor": "api-aft",
    ...(o.headers ?? {}),
  };
  if (o.body !== undefined) headers["content-type"] = "application/json";

  const started = performance.now();
  let status = 0;
  let rawBody = "";
  let contentType = "";
  let retries = 0;
  let rateLimitMasked = false;
  for (;;) {
  try {
    const res = await fetch(url, {
      method: o.method,
      headers,
      body: o.body === undefined ? undefined : JSON.stringify(o.body),
      signal: AbortSignal.timeout(o.timeoutMs ?? DEFAULT_TIMEOUT_MS),
    });
    status = res.status;
    contentType = res.headers.get("content-type") ?? "";
    rawBody = await res.text(); // capture BEFORE parse
  } catch (err) {
    const e = err as Error;
    // A timeout is recorded, never thrown: an endpoint that blocks past the ceiling is
    // itself a finding (usually a stream or long-poll misclassified as a plain read).
    rawBody = e.name === "TimeoutError" || e.name === "AbortError"
      ? `TIMEOUT after ${o.timeoutMs ?? DEFAULT_TIMEOUT_MS}ms`
      : `TRANSPORT_ERROR: ${e.message}`;
    status = 0;
  }
    if (!isRateLimited(status, rawBody) || retries >= MAX_RATE_LIMIT_RETRIES) break;
    if (status === 503) rateLimitMasked = true;
    await new Promise((r) => setTimeout(r, retryDelayMs(retries)));
    retries++;
  }
  const durationMs = performance.now() - started;

  let body: unknown = null;
  let parseError: string | null = null;
  if (rawBody.length > 0) {
    if (contentType.includes("json")) {
      try {
        body = JSON.parse(rawBody);
      } catch (err) {
        parseError = (err as Error).message;
      }
    } else {
      body = rawBody;
    }
  }

  const ex: Exchange = {
    seq: rec.exchanges.length + 1,
    service: o.service,
    method: o.method.toUpperCase(),
    url,
    pathname: new URL(url).pathname,
    requestBody: o.body ?? null,
    status,
    contentType,
    rawBody,
    body,
    parseError,
    durationMs,
    checkpoint: rec.currentCheckpoint,
    retries,
    rateLimitMasked,
  };
  rec.exchanges.push(ex);
  return ex;
}

// --- normalization -----------------------------------------------------------
//
// Diffs are only meaningful once generated identity and clock noise are neutralised.
// Rules are FIELD-NAMED, never positional, so a reordered response does not silently
// blank a different field than intended.

const VOLATILE_EXACT = new Set([
  "id",
  "created_at",
  "updated_at",
  "closed_at",
  "started_at",
  "finished_at",
  "duration",
  "duration_ms",
  "request_id",
  "session_id",
  "run_id",
  "token",
  "path",
  "worktree",
  "version",
  "events_checked",
  "issues_checked",
]);

const ISO_RE = /^\d{4}-\d{2}-\d{2}T[\d:.]+(Z|[+-]\d{2}:\d{2})$/;
const ID_RE = /^(FLEET|E2E|LOOM)-[A-Za-z0-9_-]+$/;

export function normalize(v: unknown, keyPath: string[] = []): unknown {
  const key = keyPath[keyPath.length - 1] ?? "";
  if (Array.isArray(v)) return v.map((x, i) => normalize(x, [...keyPath, String(i)]));
  if (v !== null && typeof v === "object") {
    const out: Record<string, unknown> = {};
    for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
      out[k] = normalize(val, [...keyPath, k]);
    }
    return out;
  }
  if (typeof v === "string") {
    if (VOLATILE_EXACT.has(key)) return `<${key}>`;
    if (ISO_RE.test(v)) return "<timestamp>";
    if (ID_RE.test(v)) return "<id>";
    return v;
  }
  if (typeof v === "number" && VOLATILE_EXACT.has(key)) return 0;
  return v;
}

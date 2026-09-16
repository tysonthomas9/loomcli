// The breadth lane.
//
// Scenarios build a real world through user-path calls; the sweep then replays every
// safe GET against that world. Depth comes from invariants, breadth from here --
// neither pretends to be the other.
//
// A GET is only swept if every path parameter can be BOUND from state a scenario
// actually created. Fabricating an id would test the 404 path and score it as
// coverage, which is worse than an honest gap: it makes the number go up while
// checking nothing. Unbindable operations are reported as a work list.

import { call, isRateLimited, type Recorder } from "./wire.ts";
import { family, type Planned } from "./plan.ts";
import type { Stack } from "./stack.ts";

export type Bindings = Map<string, string[]>;

export type SweepResult = {
  swept: number;
  skipped: { operationId: string; missing: string[] }[];
  nonOk: { operationId: string; path: string; status: number; body: string }[];
};

/**
 * Universal postconditions -- true of every safe GET regardless of endpoint, so they
 * cost nothing per operation and scale with the sweep.
 */
export type Postcondition = { id: string; check: (status: number, body: unknown, raw: string) => string | null };

export const POSTCONDITIONS: Postcondition[] = [
  {
    id: "PC-NO-5XX",
    // A 503 carrying a structured `kind: unavailable` is declared degradation (a
    // missing daemon, no egress), not a server fault. Flagging it as high would train
    // the reader to ignore this postcondition, which is how a real 500 gets missed.
    check: (status, body) => {
      if (status < 500) return null;
      const kind = (body as { kind?: string } | null)?.kind;
      // A rate limit wearing a 503 is NOT declared degradation -- see PC-RATE-MASK.
      const raw = JSON.stringify(body ?? "");
      if (isRateLimited(status, raw)) return null;
      if (status === 503 && (kind === "unavailable" || kind === "egress_unavailable")) return null;
      return `server error ${status} on a read-only request`;
    },
  },
  {
    id: "PC-RATE-MASK",
    // Status fidelity across the seam. A client that sees 503 "unavailable" retries a
    // dead backend; one that sees 429 + Retry-After backs off correctly. Losing the
    // distinction turns a recoverable condition into an apparent outage.
    check: (status, body, raw) =>
      status === 503 && isRateLimited(status, raw)
        ? "fleet-db rate limit (429) surfaced by loom as 503 kind:unavailable, dropping the status and Retry-After"
        : null,
  },
  {
    id: "PC-JSON-PARSES",
    check: (status, body, raw) =>
      status >= 200 && status < 300 && raw.length > 0 && body === null
        ? "2xx body did not parse as JSON"
        : null,
  },
  {
    id: "PC-NO-NULL-COLLECTION",
    check: (status, body) => {
      // A null where a list belongs is the classic Go zero-value leak: clients that
      // do `for (const x of res.items)` crash instead of iterating nothing.
      if (status < 200 || status >= 300 || body === null || typeof body !== "object") return null;
      const bad: string[] = [];
      const walk = (v: unknown, path: string, depth: number): void => {
        if (depth > 4 || v === null || typeof v !== "object") return;
        for (const [k, val] of Object.entries(v as Record<string, unknown>)) {
          if (val === null && /s$|list|items|entries|results/i.test(k)) bad.push(`${path}.${k}`);
          walk(val, `${path}.${k}`, depth + 1);
        }
      };
      walk(body, "$", 0);
      return bad.length > 0 ? `null where a collection is expected: ${bad.join(", ")}` : null;
    },
  },
];

/** Only `ws`/`workspace` are global; every other parameter must be family-qualified. */
const GLOBAL_PARAMS = new Set(["ws", "workspace"]);

function lookup(fam: string, param: string, bindings: Bindings): string | undefined {
  const qualified = bindings.get(`${fam}.${param}`);
  if (qualified && qualified.length > 0) return qualified[0];
  if (GLOBAL_PARAMS.has(param)) {
    const global = bindings.get(param);
    if (global && global.length > 0) return global[0];
  }
  return undefined;
}

function bind(p: Planned, bindings: Bindings): { path: string; missing: string[] } {
  const fam = family(p);
  const missing: string[] = [];
  let path = p.pathTemplate;
  for (const param of p.params) {
    const val = lookup(fam, param, bindings);
    if (val === undefined) {
      missing.push(param);
      continue;
    }
    path = path.replace(`{${param}}`, encodeURIComponent(val));
  }
  return { path, missing };
}

export async function readSweep(
  stack: Stack,
  rec: Recorder,
  planned: Planned[],
  bindings: Bindings,
): Promise<SweepResult> {
  const result: SweepResult = { swept: 0, skipped: [], nonOk: [] };
  rec.currentCheckpoint = "read-sweep";

  for (const p of planned) {
    if (p.klass !== "safe-read") continue;
    const bound = bind(p, bindings);
    if (bound.missing.length > 0) {
      result.skipped.push({ operationId: p.operationId, missing: bound.missing });
      continue;
    }
    const baseUrl = p.service === "loom" ? stack.loomUrl : stack.fleetUrl;
    const ex = await call(rec, {
      service: p.service === "loom" ? "loom" : "fleetdb",
      baseUrl,
      method: "GET",
      path: bound.path,
    });
    result.swept++;
    if (ex.status < 200 || ex.status >= 300) {
      result.nonOk.push({ operationId: p.operationId, path: bound.path, status: ex.status, body: ex.rawBody.slice(0, 160) });
    }
  }

  rec.currentCheckpoint = null;
  return result;
}

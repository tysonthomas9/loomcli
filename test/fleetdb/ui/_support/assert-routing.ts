/**
 * Three-way verification that a fleet-tab write action actually reached
 * fleet-db. Without this guard, an adapter bug could silently fall back
 * to the local reference store and every "regression pass" result would be
 * meaningless.
 *
 * Per ui-test-plan.md §3, we verify a fleet-tab write through three
 * independent observation channels:
 *   1. Playwright network intercept — the fleet tab emitted a matching
 *      /api/issues XHR (evidence the frontend made the call).
 *   2. Fleet-db access log delta — fleet-db received a request (evidence
 *      the loom-fleet adapter didn't swallow it).
 *   3. Redis XLEN events:FLEETDB — the event stream grew (evidence fleet-db
 *      actually applied the write).
 *
 * If ANY signal is zero on a write action, we fail the test with
 * "silent fallback detected" and dump forensics.
 */
import { Page, Request } from "@playwright/test";
import { execSync } from "node:child_process";
import * as path from "node:path";
import * as fs from "node:fs/promises";
import { FLEETDB_URLS, ARTIFACTS_DIR } from "../playwright.config";
import { composeRun } from "./compose";

const REPO_ROOT = path.resolve(__dirname, "../../../..");

export type RoutingVerdict = "confirmed" | "silent-fallback" | "not-a-write";

export interface RoutingProof {
  test_id: string;
  action: string;
  write_verb: string;
  request_url: string;
  browser_request_count: number;
  fleetdb_log_delta: number;
  redis_xlen_before: number;
  redis_xlen_after: number;
  redis_xlen_delta: number;
  verdict: RoutingVerdict;
  notes: string[];
}

/**
 * Install a network listener on the fleet tab before any action runs.
 * Returns a handle used to assert a subsequent action hit fleet-db through
 * all three channels.
 */
export function attachFleetNetworkSpy(fleet: Page) {
  const writes: Array<{
    url: string;
    method: string;
    at: number;
  }> = [];
  const listener = (req: Request) => {
    const method = req.method();
    if (!["POST", "PUT", "PATCH", "DELETE"].includes(method)) return;
    const url = req.url();
    if (
      !/\/api\/(workspaces\/[^/]+\/)?issues/.test(url) &&
      !/\/api\/(workspaces\/[^/]+\/)?(comments|deps|labels|close|reopen|search)/.test(
        url,
      )
    ) {
      return;
    }
    writes.push({ url, method, at: Date.now() });
  };
  fleet.on("request", listener);
  return {
    writes,
    detach: () => fleet.off("request", listener),
  };
}

function redisXLen(): number {
  try {
    const out = execSync(
      // Stream key is fleet-db:{ws}:events (workspace-wide firehose).
      // Per-issue streams (fleet-db:{ws}:events:issue:{id}) exist but
      // each one only reflects its own issue — assertion needs the
      // workspace-scoped total to see cross-issue writes land.
      composeRun(
        `exec -T redis redis-cli XLEN fleet-db:${FLEETDB_URLS.workspace}:events`,
      ),
      {
        cwd: REPO_ROOT,
        encoding: "utf-8",
        timeout: 5000,
      },
    ).trim();
    const n = parseInt(out, 10);
    return Number.isFinite(n) ? n : 0;
  } catch {
    return 0;
  }
}

// writeRequiresEventStream returns true if a successful write to the
// observed URL is expected to produce an entry on fleet-db's per-workspace
// `fleet-db:{ws}:events` stream. Issue lifecycle ops (create, patch, close,
// claim, comment) are streamed; relation ops (dependencies, labels) and
// some metadata ops are not — fleet-db treats them as side-effects of the
// owning issue rather than independent events. Without this guard, every
// dep / label test trips the "redis delta=0" silent-fallback branch even
// though the action actually landed on fleet-db.
function writeRequiresEventStream(
  write: { url?: string } | undefined,
): boolean {
  if (!write?.url) return true;
  const u = write.url;
  // Anything containing /dependencies, /labels, /tags is a relation-only
  // op; fleet-db doesn't emit a workspace-stream event for these.
  if (/\/(dependencies|deps|labels|tags)(\/|\?|$)/.test(u)) return false;
  return true;
}

function fleetDBRequestCount(): number {
  // fleet-db's /metrics exposes per-route histogram counters. Sum all
  // *_count series across labels for a single grand total. Earlier
  // versions of this helper looked for `http_requests_total` which
  // fleet-db never emits — that returned 0 unconditionally and made
  // every routing assertion think the request had silently fallen back.
  try {
    const m = execSync(
      `curl -sf ${FLEETDB_URLS.fleetDB}/metrics 2>/dev/null || true`,
      { encoding: "utf-8", timeout: 3000 },
    );
    const re =
      /^fleetdb_http_request_duration_seconds_count\{[^}]*\}\s+(\d+(?:\.\d+)?)/gm;
    let total = 0;
    let match: RegExpExecArray | null;
    while ((match = re.exec(m)) !== null) {
      total += Math.floor(parseFloat(match[1]));
    }
    if (total > 0) return total;
  } catch {
    // fall through
  }
  try {
    const logs = execSync(
      composeRun(
        `logs --tail=1000 fleet-db 2>&1 | grep -cE 'POST|PUT|PATCH|DELETE' || true`,
      ),
      { cwd: REPO_ROOT, encoding: "utf-8", timeout: 5000 },
    );
    return parseInt(logs.trim(), 10) || 0;
  } catch {
    return 0;
  }
}

async function dumpForensics(
  testId: string,
  proof: RoutingProof,
): Promise<void> {
  const dir = path.join(ARTIFACTS_DIR, "forensics", testId);
  await fs.mkdir(dir, { recursive: true });
  // Proof JSON
  await fs.writeFile(
    path.join(dir, `routing-${proof.action.replace(/[^a-z0-9]/gi, "_")}.json`),
    JSON.stringify(proof, null, 2),
  );
  // Fleet-db log tail
  try {
    const logs = execSync(composeRun(`logs --tail=200 fleet-db 2>&1`), {
      cwd: REPO_ROOT,
      encoding: "utf-8",
      timeout: 5000,
    });
    await fs.writeFile(path.join(dir, `fleet-db-log.txt`), logs);
  } catch (e: any) {
    await fs.writeFile(
      path.join(dir, `fleet-db-log.txt`),
      `(unreachable: ${e?.message})`,
    );
  }
}

/**
 * Perform the fleet-tab write action inside the `action` thunk, then
 * prove it reached fleet-db through three independent channels.
 *
 * Usage:
 *   const spy = attachFleetNetworkSpy(tabs.fleet);
 *   await assertRoutingForAction("create-issue", spy, async () => {
 *       await tabs.fleet.click('text=Create');
 *       await tabs.fleet.waitForResponse(/\/issues$/);
 *   });
 */
export async function assertRoutingForAction(
  testId: string,
  actionName: string,
  spy: ReturnType<typeof attachFleetNetworkSpy>,
  action: () => Promise<void>,
): Promise<RoutingProof> {
  const logBefore = fleetDBRequestCount();
  const xBefore = redisXLen();
  const writesBefore = spy.writes.length;

  await action();
  // Wait up to 3s for fleet-db to see and Redis to propagate the write.
  // Poll every 200ms.
  const deadline = Date.now() + 3000;
  let logAfter = logBefore;
  let xAfter = xBefore;
  while (Date.now() < deadline) {
    logAfter = fleetDBRequestCount();
    xAfter = redisXLen();
    if (logAfter > logBefore && xAfter > xBefore) break;
    await new Promise((r) => setTimeout(r, 200));
  }

  const browserDelta = spy.writes.length - writesBefore;
  const logDelta = Math.max(0, logAfter - logBefore);
  const redisDelta = Math.max(0, xAfter - xBefore);
  const lastWrite = spy.writes[spy.writes.length - 1];

  let verdict: RoutingVerdict = "confirmed";
  const notes: string[] = [];
  if (browserDelta === 0) {
    verdict = "not-a-write";
    notes.push(
      "Browser emitted no /api/issues write XHR — action was a read-only navigation?",
    );
  } else if (logDelta === 0) {
    verdict = "silent-fallback";
    notes.push(
      "Browser sent a write but fleet-db's request count did NOT grow — silent fallback to reference.",
    );
  } else if (redisDelta === 0 && writeRequiresEventStream(lastWrite)) {
    verdict = "silent-fallback";
    notes.push(
      "Fleet-db saw the request but Redis events:FLEETDB did NOT grow — fleet-db rejected or stream not wired.",
    );
  }

  const proof: RoutingProof = {
    test_id: testId,
    action: actionName,
    write_verb: lastWrite?.method ?? "(none)",
    request_url: lastWrite?.url ?? "(none)",
    browser_request_count: browserDelta,
    fleetdb_log_delta: logDelta,
    redis_xlen_before: xBefore,
    redis_xlen_after: xAfter,
    redis_xlen_delta: redisDelta,
    verdict,
    notes,
  };

  // Always persist the proof so the afterAll coverage report can include it.
  await appendRoutingProof(proof);

  if (verdict === "silent-fallback") {
    await dumpForensics(testId, proof);
    throw new Error(
      `silent-fallback detected: ${actionName}\n` +
        `  browser writes=${browserDelta}  fleet-db delta=${logDelta}  redis delta=${redisDelta}\n` +
        `  notes: ${notes.join("; ")}`,
    );
  }
  return proof;
}

/**
 * Convenience wrapper that issues a workspace-scoped fleet-tab fetch
 * inside an assertRoutingForAction block. The write specs repeatedly
 * materialized this same pattern by hand:
 *
 *   await assertRoutingForAction(testId, actionName, spy, async () => {
 *       await tabs.fleet.evaluate(async ({ id, ws }) => {
 *           const r = await fetch(`/api/workspaces/${ws}/<path>`, {
 *               method: "<M>",
 *               headers: { "Content-Type": "application/json" },
 *               body: JSON.stringify(<body>),
 *           });
 *           if (!r.ok) throw new Error(...);
 *       }, {...});
 *   });
 *
 * routedFleetRequest reduces each of those sites to a single call and
 * threads the routing assertion through uniformly. Returns whatever
 * assertRoutingForAction returns so callers can inspect the proof.
 */
export async function routedFleetRequest(
  tabs: { fleet: Page; testId: string },
  spy: ReturnType<typeof attachFleetNetworkSpy>,
  actionName: string,
  opts: {
    path: string; // e.g. "issues" or `issues/${id}/close`
    method: "POST" | "PUT" | "PATCH" | "DELETE";
    body?: unknown; // JSON body; omit or pass null for a bodyless request
    acceptStatus?: number[]; // status codes considered success (default: 2xx)
  },
): Promise<RoutingProof> {
  const { path: suffix, method, body, acceptStatus } = opts;
  return assertRoutingForAction(tabs.testId, actionName, spy, async () => {
    await tabs.fleet.evaluate(
      async ({ suffix, method, body, ws, acceptStatus }) => {
        const init: RequestInit = {
          method,
          headers: { "Content-Type": "application/json" },
        };
        if (body !== undefined && body !== null) {
          init.body = JSON.stringify(body);
        }
        const r = await fetch(`/api/workspaces/${ws}/${suffix}`, init);
        const ok = r.ok || (acceptStatus ?? []).includes(r.status);
        if (!ok) {
          throw new Error(`${method} /${suffix}: ${r.status}`);
        }
      },
      {
        suffix,
        method,
        body: body ?? null,
        ws: FLEETDB_URLS.workspace,
        acceptStatus: acceptStatus ?? [],
      },
    );
  });
}

const ROUTING_PROOF_PATH = path.join(
  ARTIFACTS_DIR,
  "reports",
  "routing-proof.json",
);

async function appendRoutingProof(proof: RoutingProof): Promise<void> {
  await fs.mkdir(path.dirname(ROUTING_PROOF_PATH), { recursive: true });
  let existing: RoutingProof[] = [];
  try {
    const s = await fs.readFile(ROUTING_PROOF_PATH, "utf-8");
    existing = JSON.parse(s);
  } catch {
    // First write.
  }
  existing.push(proof);
  await fs.writeFile(ROUTING_PROOF_PATH, JSON.stringify(existing, null, 2));
}

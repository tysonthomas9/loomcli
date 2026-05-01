/**
 * Coverage tracker: records which REQUIRED_ROUTES each test exercised.
 * In afterAll, writes a report to artifacts/reports/coverage.json. The
 * suite-level afterAll fails if any route was not exercised by any test.
 *
 * Per ui-test-plan.md §5.
 */
import * as fs from "node:fs/promises";
import * as fsSync from "node:fs";
import * as path from "node:path";
import { ARTIFACTS_DIR } from "../playwright.config";

// Routes the parity suite must exercise at least once. The
// normalizeUrlToRoute helper below collapses live URLs (which include
// concrete UUIDs and IDs) into these canonical patterns; the gate then
// asserts that every required route appears in the recorded set.
//
// Earlier drafts of this list used /api/issues/... — a pre-multi-
// workspace shape that the production webui never shipped — which made
// the gate guaranteed-to-fail on every run regardless of test results.
// The current canonical shape is /api/issues, /api/issues/:id, etc. with
// the workspace prefix stripped by the normalizer.
export const REQUIRED_ROUTES = [
    "/api/config",
    "/api/workspaces",
    "/api/issues",
    "/api/issues/:id",
    "/api/issues/:id/close",
    "/api/issues/:id/reopen",
    "/api/issues/:id/comments",
    "/api/issues/:id/dependencies",
    "/api/issues/:id/events",
    "/api/issues/search",
    "/api/ready",
    "/api/blocked",
    "/api/stats",
];

export const REQUIRED_FIELDS = [
    "id",
    "title",
    "description",
    "status",
    "priority",
    "type",
    "assignee",
    "owner",
    "labels",
    "external_ref",
    "defer_until",
    "due_at",
    "parent_id",
    "repo",
    "created_at",
    "created_by",
    "updated_at",
    "closed_at",
    "close_reason",
];

export interface CoverageRecord {
    test: string;
    routes_exercised: string[];
    fields_covered: string[];
}

// Mutable singleton — flushed in afterAll.
const records: CoverageRecord[] = [];
const coverageHitsPath = path.join(ARTIFACTS_DIR, "reports", "coverage-hits.ndjson");

export function recordRoutes(test: string, routes: string[], fields: string[] = []) {
    const record = { test, routes_exercised: routes, fields_covered: fields };
    records.push(record);
    fsSync.mkdirSync(path.dirname(coverageHitsPath), { recursive: true });
    fsSync.appendFileSync(coverageHitsPath, `${JSON.stringify(record)}\n`);
}

export function resetCoverageRecords() {
    records.length = 0;
    fsSync.rmSync(coverageHitsPath, { force: true });
}

/**
 * Normalize a URL to a route pattern like "/api/issues/:id/close".
 *
 * Strips the workspace UUID and concrete issue IDs so the canonical
 * route shape can be matched against REQUIRED_ROUTES. Bare /api/workspaces
 * (the list endpoint) is preserved separately from /api/workspaces/{uuid}/...
 * (which collapse to /api/...).
 */
export function normalizeUrlToRoute(url: string): string | null {
    try {
        const u = new URL(url);
        let p = u.pathname;
        // Bare /api/workspaces (list) — preserve as-is.
        if (p === "/api/workspaces" || p === "/api/workspaces/") {
            return "/api/workspaces";
        }
        // /api/workspaces/{uuid}/... → /api/...
        p = p.replace(/^\/api\/workspaces\/[^/]+/, "/api");
        // /api/issues/{id}/{action} → /api/issues/:id/{action}
        p = p.replace(/\/issues\/[^/]+\/(close|reopen|labels|comments|dependencies|deps|events)$/, "/issues/:id/$1");
        // /api/issues/search? → /api/issues/search (preserve before :id collapse)
        if (/\/issues\/search(\?|$)/.test(p)) return "/api/issues/search";
        // /api/issues/{id} (no trailing action) → /api/issues/:id
        p = p.replace(/\/issues\/[^/]+$/, "/issues/:id");
        // Direct REQUIRED_ROUTES match.
        const known = REQUIRED_ROUTES.find((r) => r === p || r === p.replace(/\/$/, ""));
        if (known) return known;
        // SSE event paths.
        if (p === "/api/events" || /\/events(\/|$)/.test(p)) return "/api/sse";
        return REQUIRED_ROUTES.includes(p) ? p : p.startsWith("/api/") ? p : null;
    } catch {
        return null;
    }
}

export async function writeCoverageReport(): Promise<{
    covered: string[];
    missing: string[];
    per_test: CoverageRecord[];
}> {
    const persisted = await readPersistedCoverageRecords();
    const allRecords = [...records, ...persisted];
    const covered = new Set<string>();
    for (const r of allRecords) for (const rt of r.routes_exercised) covered.add(rt);
    const missing = REQUIRED_ROUTES.filter((r) => !covered.has(r));
    const out = {
        generated_at: new Date().toISOString(),
        required_routes: REQUIRED_ROUTES,
        covered: [...covered].sort(),
        missing,
        per_test: allRecords,
    };
    const outPath = path.join(ARTIFACTS_DIR, "reports", "coverage.json");
    await fs.mkdir(path.dirname(outPath), { recursive: true });
    await fs.writeFile(outPath, JSON.stringify(out, null, 2));
    return { covered: [...covered].sort(), missing, per_test: allRecords };
}

async function readPersistedCoverageRecords(): Promise<CoverageRecord[]> {
    const raw = await fs.readFile(coverageHitsPath, "utf8").catch(() => "");
    return raw
        .split(/\n+/)
        .map((line) => line.trim())
        .filter(Boolean)
        .map((line) => {
            try {
                return JSON.parse(line) as CoverageRecord;
            } catch {
                return null;
            }
        })
        .filter((r): r is CoverageRecord => !!r);
}

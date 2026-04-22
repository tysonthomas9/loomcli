/**
 * Coverage tracker: records which REQUIRED_ROUTES each test exercised.
 * In afterAll, writes a report to artifacts/reports/coverage.json. The
 * suite-level afterAll fails if any route was not exercised by any test.
 *
 * Per ui-test-plan.md §5.
 */
import * as fs from "node:fs/promises";
import * as path from "node:path";
import { ARTIFACTS_DIR } from "../playwright.config";

export const REQUIRED_ROUTES = [
    "/api/issues",
    "/api/issues/:id",
    "/api/issues/:id/close",
    "/api/issues/:id/reopen",
    "/api/issues/:id/labels",
    "/api/issues/:id/comments",
    "/api/issues/:id/deps",
    "/api/issues/:id/events",
    "/api/issues/ready",
    "/api/issues/blocked",
    "/api/issues/search",
    "/api/issues/stats",
    "/api/config",
    "/api/workspaces",
    "/api/sse",
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

export function recordRoutes(test: string, routes: string[], fields: string[] = []) {
    records.push({ test, routes_exercised: routes, fields_covered: fields });
}

/**
 * Normalize a URL to a route pattern like "/api/issues/:id/close".
 */
export function normalizeUrlToRoute(url: string): string | null {
    try {
        const u = new URL(url);
        let p = u.pathname;
        // Workspace-scoped to plan-level route
        p = p.replace(/^\/api\/workspaces\/[^/]+/, "/api");
        p = p.replace(/\/issues\/[^/]+\/(close|reopen|labels|comments|deps|events)$/, "/issues/:id/$1");
        p = p.replace(/\/issues\/[^/]+$/, "/issues/:id");
        if (p.startsWith("/api/workspaces")) return "/api/workspaces";
        if (p === "/api/events" || /\/events(\/|$)/.test(p)) return "/api/sse";
        // Only return paths we care about.
        const known = REQUIRED_ROUTES.find((r) => r === p || r === p.replace(/\/$/, ""));
        return known ?? (REQUIRED_ROUTES.includes(p) ? p : p.startsWith("/api/") ? p : null);
    } catch {
        return null;
    }
}

export async function writeCoverageReport(): Promise<{
    covered: string[];
    missing: string[];
    per_test: CoverageRecord[];
}> {
    const covered = new Set<string>();
    for (const r of records) for (const rt of r.routes_exercised) covered.add(rt);
    const missing = REQUIRED_ROUTES.filter((r) => !covered.has(r));
    const out = {
        generated_at: new Date().toISOString(),
        required_routes: REQUIRED_ROUTES,
        covered: [...covered].sort(),
        missing,
        per_test: records,
    };
    const outPath = path.join(ARTIFACTS_DIR, "reports", "coverage.json");
    await fs.mkdir(path.dirname(outPath), { recursive: true });
    await fs.writeFile(outPath, JSON.stringify(out, null, 2));
    return { covered: [...covered].sort(), missing, per_test: records };
}

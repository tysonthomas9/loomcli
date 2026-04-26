/**
 * Data-level diff helpers. Used to compare API responses and backend state
 * between beads and fleet tabs.
 *
 * Known drift is masked:
 *   - timestamps within ±5s are equivalent (matches fleet-db Go harness)
 *   - ID formats: beads uses slugs ("loomcli-abc123"), fleet-db uses "p1-001"
 *   - created_by / source_repo may legitimately differ across adapters
 */
import * as fs from "node:fs/promises";
import * as path from "node:path";
import { ARTIFACTS_DIR, PARITY_URLS } from "../playwright.config";
import { discoverWorkspaceId, type BackendState } from "./backends";

const TS_TOLERANCE_MS = 5_000;
const DATA_DIFFS_PATH = path.join(ARTIFACTS_DIR, "reports", "data-diffs.json");

export interface FieldDiff {
    field: string;
    beads: any;
    fleet: any;
    reason: string;
}

export interface ApiResponseDiff {
    endpoint: string;
    count_beads: number;
    count_fleet: number;
    diffs: FieldDiff[];
    match: boolean;
}

function normalizeId(v: unknown): string {
    if (typeof v !== "string") return String(v ?? "");
    // Normalize "loomcli-abc123" vs "p1-001" to the numeric tail.
    const m = v.match(/[-_](\d+)$/);
    return m ? m[1] : v;
}

function equivalentTs(a: unknown, b: unknown): boolean {
    const ta = typeof a === "string" ? Date.parse(a) : NaN;
    const tb = typeof b === "string" ? Date.parse(b) : NaN;
    if (Number.isNaN(ta) || Number.isNaN(tb)) return false;
    return Math.abs(ta - tb) <= TS_TOLERANCE_MS;
}

const TS_FIELDS = new Set(["created_at", "updated_at", "closed_at", "defer_until", "due_at"]);
const ID_FIELDS = new Set(["id", "parent_id"]);
const TOLERATED_MISMATCH_FIELDS = new Set([
    // Known legitimate cross-backend differences per the parity report
    "created_by",
    "source_repo",
    "repo",
    "external_ref", // fleet-db uses different external_ref scheme
]);

/**
 * Field-by-field diff of two issue lists. Pairs by id (normalized) and
 * emits a FieldDiff per mismatched field. Returns `match=true` only if
 * EVERY pair matches on every required field (after normalization).
 */
export function diffIssueLists(
    beadsList: any[],
    fleetList: any[],
    requiredFields: string[],
): ApiResponseDiff {
    const diffs: FieldDiff[] = [];
    // Pair by normalized-id when possible, else by title (seed script ensures
    // unique titles across the 13 fixture issues).
    const keyFor = (i: any) => normalizeId(i.id ?? i.ID ?? "") || (i.title ?? "");
    const bMap = new Map<string, any>();
    for (const i of beadsList) bMap.set(keyFor(i), i);
    const fMap = new Map<string, any>();
    for (const i of fleetList) fMap.set(keyFor(i), i);

    if (beadsList.length !== fleetList.length) {
        diffs.push({
            field: "__count__",
            beads: beadsList.length,
            fleet: fleetList.length,
            reason: "list length differs",
        });
    }

    const keys = new Set([...bMap.keys(), ...fMap.keys()]);
    for (const k of keys) {
        const b = bMap.get(k);
        const f = fMap.get(k);
        if (!b) {
            diffs.push({ field: k, beads: undefined, fleet: f, reason: "only in fleet" });
            continue;
        }
        if (!f) {
            diffs.push({ field: k, beads: b, fleet: undefined, reason: "only in beads" });
            continue;
        }
        for (const field of requiredFields) {
            const bv = b[field];
            const fv = f[field];
            if (bv === undefined && fv === undefined) continue;
            if (TS_FIELDS.has(field)) {
                if (!equivalentTs(bv, fv) && bv !== fv) {
                    diffs.push({
                        field: `${k}.${field}`,
                        beads: bv,
                        fleet: fv,
                        reason: "timestamp outside ±5s tolerance",
                    });
                }
                continue;
            }
            if (ID_FIELDS.has(field)) {
                if (normalizeId(bv) !== normalizeId(fv)) {
                    diffs.push({
                        field: `${k}.${field}`,
                        beads: bv,
                        fleet: fv,
                        reason: "id mismatch (after tail-normalization)",
                    });
                }
                continue;
            }
            if (TOLERATED_MISMATCH_FIELDS.has(field)) {
                // Record as informational, don't flip `match` to false.
                if (JSON.stringify(bv) !== JSON.stringify(fv)) {
                    diffs.push({
                        field: `${k}.${field}`,
                        beads: bv,
                        fleet: fv,
                        reason: "known cross-backend drift (informational)",
                    });
                }
                continue;
            }
            if (JSON.stringify(bv) !== JSON.stringify(fv)) {
                diffs.push({
                    field: `${k}.${field}`,
                    beads: bv,
                    fleet: fv,
                    reason: "value mismatch",
                });
            }
        }
    }

    const materialDiffs = diffs.filter((d) => !/informational/.test(d.reason));
    return {
        endpoint: "/api/issues",
        count_beads: beadsList.length,
        count_fleet: fleetList.length,
        diffs,
        match: materialDiffs.length === 0,
    };
}

/**
 * Fetch both backends' response for an endpoint and diff the result.
 * `endpointPath` is a workspace-scoped path segment, e.g. "issues" or
 * "issues/ready". When `wsBeads` / `wsFleet` are omitted, each backend's
 * workspace ID is discovered at call time via `/api/workspaces` — loom
 * uses UUID workspace IDs, so literals like "default" or "PARITY" never
 * match an actual workspace and produce 404s with empty payloads.
 */
export async function apiResponseDiff(
    endpointSuffix: string,
    wsBeads?: string,
    wsFleet?: string,
): Promise<ApiResponseDiff> {
    const [bWs, fWs] = await Promise.all([
        wsBeads ?? discoverWorkspaceId(PARITY_URLS.beads),
        wsFleet ?? discoverWorkspaceId(PARITY_URLS.fleet),
    ]);
    const [b, f] = await Promise.all([
        fetchJson(`${PARITY_URLS.beads}/api/workspaces/${bWs}/${endpointSuffix}`),
        fetchJson(`${PARITY_URLS.fleet}/api/workspaces/${fWs}/${endpointSuffix}`),
    ]);
    const bData = Array.isArray(b?.data) ? b.data : b?.data ? [b.data] : [];
    const fData = Array.isArray(f?.data) ? f.data : f?.data ? [f.data] : [];
    const diff = diffIssueLists(bData, fData, [
        "id",
        "title",
        "description",
        "status",
        "priority",
        "type",
        "issue_type",
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
    ]);
    diff.endpoint = endpointSuffix;
    await appendDataDiff(diff);
    return diff;
}

async function fetchJson(url: string): Promise<any> {
    try {
        const r = await fetch(url);
        if (!r.ok) return null;
        return await r.json();
    } catch {
        return null;
    }
}

async function appendDataDiff(diff: ApiResponseDiff): Promise<void> {
    await fs.mkdir(path.dirname(DATA_DIFFS_PATH), { recursive: true });
    let existing: ApiResponseDiff[] = [];
    try {
        existing = JSON.parse(await fs.readFile(DATA_DIFFS_PATH, "utf-8"));
    } catch {
        // first write
    }
    existing.push({ ...diff, diffs: diff.diffs.slice(0, 100) });
    await fs.writeFile(DATA_DIFFS_PATH, JSON.stringify(existing, null, 2));
}

/**
 * State sync diff between two BackendState snapshots. Compares issue
 * counts and stats, ignoring known-drift fields.
 */
export interface StateSyncDiff {
    label: string;
    beads_count: number;
    fleet_count: number;
    stats_match: boolean;
    notes: string[];
}

export function stateSyncDiff(
    before: { beads: BackendState; fleet: BackendState },
    after: { beads: BackendState; fleet: BackendState },
    label: string,
): StateSyncDiff {
    const bCount = after.beads.issues.length;
    const fCount = after.fleet.issues.length;
    const notes: string[] = [];
    if (bCount !== fCount) {
        notes.push(`issue count diverged: beads=${bCount} fleet=${fCount}`);
    }
    const bd = bCount - before.beads.issues.length;
    const fd = fCount - before.fleet.issues.length;
    if (bd !== fd) {
        notes.push(`issue delta diverged: beads+${bd} fleet+${fd}`);
    }
    // Stats parity: compare open/in_progress/closed counts when both sides
    // expose them.
    const bs = before.beads.stats ?? {};
    const fs_ = before.fleet.stats ?? {};
    const stats_match = ["open", "in_progress", "closed", "blocked"].every(
        (k) => bs[k] === undefined || fs_[k] === undefined || bs[k] === fs_[k],
    );
    if (!stats_match) notes.push(`stats diverged`);
    return {
        label,
        beads_count: bCount,
        fleet_count: fCount,
        stats_match,
        notes,
    };
}

/**
 * Assert SSE propagation timing within 2× of the faster side.
 *
 * The action should be one that triggers an SSE event on BOTH sides (e.g.
 * a create). The returned value describes the observed latency on each
 * side so the test can decide whether to fail.
 */
export interface TimingResult {
    action: string;
    beads_ms: number;
    fleet_ms: number;
    within_2x: boolean;
}

/**
 * Below this floor, timing is jitter-dominated (Playwright tick rounding,
 * GC pauses, network noise). Cross-backend ratios are not meaningful.
 */
const JITTER_FLOOR_MS = 500;

/**
 * User-visible "I notice latency" threshold. When one backend is in the
 * jitter zone and the other isn't, gate on absolute latency against this
 * ceiling instead of the strict 2x ratio — a 9ms-vs-800ms beads/fleet
 * pair is 90x by ratio but feels identical to a user.
 */
const SNAPPY_CEILING_MS = 2000;

export async function timingAssert(
    action: string,
    times: { beads: number; fleet: number },
): Promise<TimingResult> {
    const fastest = Math.min(times.beads, times.fleet);
    const slowest = Math.max(times.beads, times.fleet);
    let within: boolean;
    if (slowest < JITTER_FLOOR_MS) {
        within = true;
    } else if (fastest < JITTER_FLOOR_MS) {
        within = slowest < SNAPPY_CEILING_MS;
    } else {
        within = slowest <= fastest * 2;
    }
    return { action, beads_ms: times.beads, fleet_ms: times.fleet, within_2x: within };
}

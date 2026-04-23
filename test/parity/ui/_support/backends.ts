/**
 * Dual-backend drivers: helpers to open both beads (:8081) and fleet (:8082)
 * tabs in lockstep, reset state, and snapshot raw state from each backend.
 *
 * The two tabs share a Playwright `browser` but live in separate contexts
 * so cookies/storage don't bleed — and the fleet tab can have its own HAR
 * recorder + request intercept without polluting the beads tab.
 */
import { Browser, BrowserContext, Page, expect } from "@playwright/test";
import * as fs from "node:fs/promises";
import * as path from "node:path";
import { execSync } from "node:child_process";
import { PARITY_URLS, ARTIFACTS_DIR } from "../playwright.config";

export type Backend = "beads" | "fleet";

export interface DualTabs {
    beads: Page;
    fleet: Page;
    beadsCtx: BrowserContext;
    fleetCtx: BrowserContext;
    /** Called in afterEach to dump HAR + close. */
    close: () => Promise<void>;
    /** Test identifier for artifact paths. */
    testId: string;
}

export interface BackendState {
    backend: Backend;
    captured_at: string;
    issues: any[];
    stats: any;
    config: any;
}

const REPO_ROOT = path.resolve(__dirname, "../../../..");

function urlFor(b: Backend): string {
    return b === "beads" ? PARITY_URLS.beads : PARITY_URLS.fleet;
}

/**
 * Open both tabs. Each tab gets its own HAR capture so routing proofs can
 * reason about per-action network traffic without cross-tab noise.
 */
export async function openDualTabs(browser: Browser, testId: string): Promise<DualTabs> {
    const safeId = testId.replace(/[^a-zA-Z0-9_.-]/g, "_");
    const harDir = path.join(ARTIFACTS_DIR, "network-traces", safeId);
    await fs.mkdir(harDir, { recursive: true });

    const beadsCtx = await browser.newContext({
        recordHar: { path: path.join(harDir, "beads.har"), mode: "full" },
        viewport: { width: 1280, height: 720 },
        deviceScaleFactor: 2,
    });
    const fleetCtx = await browser.newContext({
        recordHar: { path: path.join(harDir, "fleet.har"), mode: "full" },
        viewport: { width: 1280, height: 720 },
        deviceScaleFactor: 2,
    });
    const beads = await beadsCtx.newPage();
    const fleet = await fleetCtx.newPage();

    return {
        beads,
        fleet,
        beadsCtx,
        fleetCtx,
        testId: safeId,
        close: async () => {
            await Promise.allSettled([beadsCtx.close(), fleetCtx.close()]);
        },
    };
}

export async function gotoBoth(tabs: DualTabs, relPath: string): Promise<void> {
    // Navigate in parallel — both tabs should hit the same path on their
    // respective base URLs.
    await Promise.all([
        tabs.beads.goto(`${urlFor("beads")}${relPath}`),
        tabs.fleet.goto(`${urlFor("fleet")}${relPath}`),
    ]);
}

/**
 * Go to the parity workspace's kanban/table/etc. page on both tabs and
 * wait for it to settle. When ws IDs aren't passed, discover them at
 * runtime — loom uses UUID workspace IDs, not literal names, so hardcoded
 * strings like "default"/"PARITY" never match an actual workspace.
 */
export async function gotoViews(
    tabs: DualTabs,
    view: "kanban" | "table" | "graph" | "monitor" | "settings",
    wsBeads?: string,
    wsFleet?: string,
): Promise<void> {
    const [b, f] = await Promise.all([
        wsBeads ?? discoverWorkspaceId(urlFor("beads")),
        wsFleet ?? discoverWorkspaceId(urlFor("fleet")),
    ]);
    await Promise.all([
        tabs.beads.goto(`${urlFor("beads")}/ws/${b}/${view}`),
        tabs.fleet.goto(`${urlFor("fleet")}/ws/${f}/${view}`),
    ]);
    await Promise.all([
        tabs.beads.waitForLoadState("networkidle").catch(() => undefined),
        tabs.fleet.waitForLoadState("networkidle").catch(() => undefined),
    ]);
}

export async function gotoIssueDetail(
    tabs: DualTabs,
    issueIdBeads: string,
    issueIdFleet: string,
    wsBeads?: string,
    wsFleet?: string,
): Promise<void> {
    const [b, f] = await Promise.all([
        wsBeads ?? discoverWorkspaceId(urlFor("beads")),
        wsFleet ?? discoverWorkspaceId(urlFor("fleet")),
    ]);
    await Promise.all([
        tabs.beads.goto(`${urlFor("beads")}/ws/${b}/issues/${issueIdBeads}`),
        tabs.fleet.goto(`${urlFor("fleet")}/ws/${f}/issues/${issueIdFleet}`),
    ]);
}

/**
 * Truncate both backends' state and re-run the seed script. Serialized so
 * concurrent tests never race.
 *
 * Called from beforeEach. Implementation: POST /api/issues (list) + iterate
 * delete on both sides, then re-run /seed.sh via docker compose. The
 * `force-clean` endpoint doesn't exist on either loom; we use the DELETE
 * verb per-issue through the same API the UI uses.
 */
let resetInFlight: Promise<void> | null = null;
export async function resetBothBackends(opts: { reseed?: boolean } = {}): Promise<void> {
    if (resetInFlight) await resetInFlight;
    resetInFlight = (async () => {
        const [beadsWs, fleetWs] = await Promise.all([
            discoverWorkspaceId(PARITY_URLS.beads),
            discoverWorkspaceId(PARITY_URLS.fleet),
        ]);
        await Promise.all([
            deleteAllIssues(PARITY_URLS.beads, beadsWs),
            deleteAllIssues(PARITY_URLS.fleet, fleetWs),
        ]);
        if (opts.reseed !== false) {
            await runSeedScript();
        }
    })();
    try {
        await resetInFlight;
    } finally {
        resetInFlight = null;
    }
}

async function deleteAllIssues(baseUrl: string, workspace: string): Promise<void> {
    try {
        const r = await fetch(`${baseUrl}/api/workspaces/${workspace}/issues`);
        if (!r.ok) return;
        const j = await r.json();
        const issues: any[] = j?.data ?? [];
        // DELETE in parallel, ignore missing.
        await Promise.all(
            issues.map((i) => {
                const id = i.id ?? i.ID ?? i.issue_id;
                if (!id) return Promise.resolve();
                return fetch(`${baseUrl}/api/workspaces/${workspace}/issues/${id}`, {
                    method: "DELETE",
                }).catch(() => undefined);
            }),
        );
    } catch {
        // Backend may not support delete; surface later in assertions.
    }
}

async function runSeedScript(): Promise<void> {
    try {
        execSync(
            `docker compose -f test/parity/docker-compose.parity.yml run --rm parity-seed`,
            {
                cwd: REPO_ROOT,
                encoding: "utf-8",
                timeout: 60_000,
                stdio: ["ignore", "pipe", "pipe"],
            },
        );
    } catch (e: any) {
        // eslint-disable-next-line no-console
        console.warn(`[backends] reseed failed: ${e?.message ?? e}`);
    }
}

/**
 * Read raw backend state so tests can prove the UI is actually reflecting
 * what the backend returned rather than a stale cache.
 */
export async function snapshotState(label: "before" | "after", testId: string): Promise<{
    beads: BackendState;
    fleet: BackendState;
    label: "before" | "after";
}> {
    const [beads, fleet] = await Promise.all([
        captureOne("beads"),
        captureOne("fleet"),
    ]);
    const outDir = path.join(ARTIFACTS_DIR, "forensics", testId);
    await fs.mkdir(outDir, { recursive: true });
    await fs.writeFile(
        path.join(outDir, `backend-state-${label}.json`),
        JSON.stringify({ beads, fleet }, null, 2),
    );
    return { beads, fleet, label };
}

async function captureOne(b: Backend): Promise<BackendState> {
    const base = urlFor(b);
    const ws = await discoverWorkspaceId(base);
    const [issuesR, statsR, configR] = await Promise.allSettled([
        fetch(`${base}/api/workspaces/${ws}/issues`).then((r) => (r.ok ? r.json() : null)),
        fetch(`${base}/api/workspaces/${ws}/stats`).then((r) => (r.ok ? r.json() : null)),
        fetch(`${base}/api/config`).then((r) => (r.ok ? r.json() : null)),
    ]);
    return {
        backend: b,
        captured_at: new Date().toISOString(),
        issues: pick(issuesR)?.data ?? [],
        stats: pick(statsR)?.data ?? pick(statsR),
        config: pick(configR),
    };
}

function pick<T>(s: PromiseSettledResult<T>): T | null {
    return s.status === "fulfilled" ? s.value : null;
}

/**
 * Discover the active workspace ID on a loom instance via /api/workspaces.
 * Loom auto-creates UUID-keyed workspaces on startup; callers can't assume
 * a literal "default" or "PARITY" string in the URL — the actual ID is
 * the UUID in the first row of `/api/workspaces`.
 *
 * Cached per base URL for the process lifetime — IDs don't change across
 * a single test run. Skips invalid responses (returns null) so specs can
 * surface a clean "couldn't find workspace" error instead of crashing on
 * a malformed response.
 */
const workspaceIdCache: Record<string, string> = {};
export async function discoverWorkspaceId(baseUrl: string): Promise<string> {
    if (workspaceIdCache[baseUrl]) return workspaceIdCache[baseUrl];

    // Primary: /api/workspaces (loom-beads populates this correctly).
    const r = await fetch(`${baseUrl}/api/workspaces`);
    if (r.ok) {
        const j = await r.json();
        const workspaces: any[] = j.workspaces ?? j.data ?? [];
        const active = workspaces.find((w) => w.active) ?? workspaces[0];
        if (active?.id) {
            workspaceIdCache[baseUrl] = active.id;
            return active.id;
        }
    }

    // Fallback: /api/monitor/workspaces. When loom runs with a fleet backend,
    // the public /api/workspaces list can be empty even though the internal
    // fleet store has the workspace registered. The monitor endpoint surfaces
    // the workspace name (e.g. "PARITY"). We accept that as the workspace id
    // — loom treats it as an alias keyed by name in URLs.
    const m = await fetch(`${baseUrl}/api/monitor/workspaces`);
    if (m.ok) {
        const j = await m.json();
        // {mode, default, workspaces: {NAME: {...}}, ...}
        const names = Object.keys(j?.workspaces ?? {});
        const pick = j?.default && names.includes(j.default) ? j.default : names[0];
        if (pick) {
            workspaceIdCache[baseUrl] = pick;
            return pick;
        }
    }

    throw new Error(
        `discoverWorkspaceId(${baseUrl}): neither /api/workspaces nor /api/monitor/workspaces yielded a workspace`,
    );
}

/**
 * Look up a fleet-side issue by its title and return enough identity to
 * drive a follow-up write. Throws with the seed's full title list if no
 * match — specs surface "seed drift" clearly rather than crashing later
 * on a missing ID. Write-flow specs previously maintained hand-rolled
 * copies of this lookup; consolidating it here keeps their flow uniform.
 */
export async function findFleetIssueByTitle(
    title: string,
): Promise<{ id: string; issue: any }> {
    const wsId = await discoverWorkspaceId(PARITY_URLS.fleet);
    const r = await fetch(
        `${PARITY_URLS.fleet}/api/workspaces/${wsId}/issues`,
    );
    if (!r.ok) {
        throw new Error(
            `findFleetIssueByTitle(${title}): list request failed ${r.status}`,
        );
    }
    const j = await r.json();
    const issues: any[] = j.data ?? [];
    const match = issues.find((i: any) => i.title === title);
    if (!match) {
        const titles = issues.map((i: any) => i.title).join(", ");
        throw new Error(
            `findFleetIssueByTitle(${title}): no seed match; titles=[${titles}]`,
        );
    }
    return { id: match.id, issue: match };
}

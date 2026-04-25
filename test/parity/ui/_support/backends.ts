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
 *
 * Installs a per-context rewrite for the bundled SPA's hard-coded
 * `VITE_API_BASE_URL` (http://localhost:8085). The checked-in dist was built
 * with a placeholder base URL that doesn't match the Caddy ports; without
 * rewriting, every fetch/XHR from the SPA fails (net::ERR_CONNECTION_REFUSED)
 * and the UI renders "Workspace unavailable" / "Failed to load data". Each
 * tab rewrites the bogus origin to its own Caddy origin so same-origin
 * proxying via Caddyfile still applies. See SPA bundle `getApiOrigin()`
 * and test/parity/Caddyfile for the intended path.
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
    // Three test-layer shims used to live here:
    //   - installApiBaseRewrite (rewrote baked-in localhost:8085 → Caddy origin)
    //   - installWorkspaceLookupFallback (404 on /api/workspaces/{uuid} → /active)
    //   - installEmptyListFallbacks (503 on /ready + /blocked → empty list)
    //
    // All three were upstream-fixed:
    //   - frontend/src/api/common/client.ts now resolves baseURL at runtime
    //     (window.location.origin) instead of compile-time substitution.
    //   - workspace_impl.go#GetWorkspace falls back to config-based lookup
    //     when multiPool is empty (fleet mode).
    //   - fleet-db gained /ready + /blocked workspace-root aliases AND
    //     loomcli's HandleReady/HandleBlocked got backend-fallback variants.
    //
    // If any test starts failing on the symptoms above, the right answer is
    // to fix the corresponding upstream — not to reintroduce a shim here.
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
    // domcontentloaded, NOT networkidle: with the runtime-baseURL frontend
    // (no more rewrite shim) and the SSE event stream now wired through the
    // backend-fallback Ready/Blocked handlers, the SPA never goes quiet —
    // it polls + maintains an open EventSource. networkidle therefore
    // never resolves and burns the 30 s default timeout per tab, which
    // pushed several specs (05 settings, 09 SSE) past their 90 s test
    // budget. domcontentloaded fires when the SPA shell is mounted and
    // can read location.href / start submitting requests.
    await Promise.all([
        tabs.beads.waitForLoadState("domcontentloaded").catch(() => undefined),
        tabs.fleet.waitForLoadState("domcontentloaded").catch(() => undefined),
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

        // Issue per-id DELETEs in capped batches. Parallel-everything
        // hammered bd's SQLite WAL hard enough that ~30% of DELETEs
        // failed silently (caught) under the prior `Promise.all(map)`
        // pattern, leaving beads with a growing residue across the
        // suite — which then blew the 90 s test budget on later
        // beforeEach hooks (the next reset had to delete 30+ issues).
        // Batches of 4 are roughly the sweet spot: enough concurrency
        // to keep wall time short, low enough to keep the lock
        // contention rate at zero in practice.
        const batchSize = 4;
        for (let i = 0; i < issues.length; i += batchSize) {
            const slice = issues.slice(i, i + batchSize);
            await Promise.all(
                slice.map((it) => {
                    const id = it.id ?? it.ID ?? it.issue_id;
                    if (!id) return Promise.resolve();
                    return fetch(
                        `${baseUrl}/api/workspaces/${workspace}/issues/${id}`,
                        { method: "DELETE" },
                    ).catch(() => undefined);
                }),
            );
        }
    } catch {
        // Backend may not support delete; surface later in assertions.
    }
}

async function runSeedScript(): Promise<void> {
    // Try both compose tools — environments use whichever is installed,
    // and `docker` may be a podman alias on Linux setups but not all of
    // them. Surfacing the failure is critical because tests that depend
    // on seeded fixtures (titles like "Refactor auth middleware") will
    // otherwise report cryptic "no seed match" errors.
    const cmds = [
        `podman compose -f test/parity/docker-compose.parity.yml run --rm parity-seed`,
        `docker compose -f test/parity/docker-compose.parity.yml run --rm parity-seed`,
    ];
    let lastErr: unknown;
    for (const cmd of cmds) {
        try {
            execSync(cmd, {
                cwd: REPO_ROOT,
                encoding: "utf-8",
                timeout: 90_000,
                stdio: ["ignore", "pipe", "pipe"],
            });
            return;
        } catch (e) {
            lastErr = e;
        }
    }
    // eslint-disable-next-line no-console
    console.warn(
        `[backends] reseed failed via both compose tools: ${(lastErr as Error)?.message ?? lastErr}`,
    );
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

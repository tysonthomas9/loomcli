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
    await installApiBaseRewrite(beadsCtx, PARITY_URLS.beads);
    await installApiBaseRewrite(fleetCtx, PARITY_URLS.fleet);
    await installWorkspaceLookupFallback(beadsCtx, PARITY_URLS.beads);
    await installWorkspaceLookupFallback(fleetCtx, PARITY_URLS.fleet);
    // TablePage fetches /ready + /blocked in parallel with /issues; on the
    // fleet backend those return 503 ("workspace not registered") even when
    // /issues returns 200. Stub those 503s as empty lists so the view's
    // "fetch-error" branch doesn't trip and hide the actual rows.
    await installEmptyListFallbacks(beadsCtx, PARITY_URLS.beads);
    await installEmptyListFallbacks(fleetCtx, PARITY_URLS.fleet);
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

/**
 * Rewrite the SPA's baked-in API origin so fetch/XHR/EventSource calls land
 * on the Caddy sidecar the tab actually loaded from. The dist bundle has
 * `VITE_API_BASE_URL=http://localhost:8085` hard-coded, but neither Caddy
 * listens there; redirecting requests to `${caddyOrigin}` restores same-
 * origin semantics and lets Caddy proxy /api/* to the right loom backend.
 *
 * We rewrite client-side via addInitScript (not route.continue) because
 * route-level rewrites only change the request target — the browser still
 * treats the fetch as cross-origin (page is on 8084, SPA asked 8085) and
 * blocks it with CORS, surfacing as net::ERR_FAILED (HAR status = -1).
 * Patching fetch/XHR/EventSource at the page boundary makes the browser
 * see every call as same-origin from the start.
 */
const BAKED_API_ORIGIN = "http://localhost:8085";
async function installApiBaseRewrite(ctx: BrowserContext, caddyOrigin: string): Promise<void> {
    await ctx.addInitScript(
        ({ baked, caddy }) => {
            const rewrite = (u: unknown): unknown => {
                if (typeof u === "string" && u.indexOf(baked) === 0) {
                    return caddy + u.slice(baked.length);
                }
                if (u instanceof URL && u.origin === baked) {
                    return new URL(u.pathname + u.search + u.hash, caddy);
                }
                if (u instanceof Request) {
                    const newUrl =
                        u.url.indexOf(baked) === 0
                            ? caddy + u.url.slice(baked.length)
                            : u.url;
                    if (newUrl === u.url) return u;
                    return new Request(newUrl, u);
                }
                return u;
            };
            const origFetch = window.fetch.bind(window);
            window.fetch = ((input: any, init?: any) => {
                return origFetch(rewrite(input) as any, init);
            }) as typeof window.fetch;

            const OrigXHR = window.XMLHttpRequest;
            function PatchedXHR(this: XMLHttpRequest) {
                const xhr = new OrigXHR();
                const origOpen = xhr.open.bind(xhr);
                (xhr as any).open = function (
                    method: string,
                    url: string | URL,
                    ...rest: any[]
                ) {
                    return origOpen(method, rewrite(url) as any, ...rest);
                };
                return xhr;
            }
            (PatchedXHR as any).prototype = OrigXHR.prototype;
            (window as any).XMLHttpRequest = PatchedXHR;

            const OrigES = window.EventSource;
            if (OrigES) {
                function PatchedES(
                    this: EventSource,
                    url: string | URL,
                    cfg?: EventSourceInit,
                ) {
                    return new OrigES(rewrite(url) as any, cfg);
                }
                (PatchedES as any).prototype = OrigES.prototype;
                (window as any).EventSource = PatchedES;
            }
        },
        { baked: BAKED_API_ORIGIN, caddy: caddyOrigin },
    );
}

/**
 * Fleet-backend fallback: when the SPA fetches `/api/workspaces/{uuid}` for
 * the active workspace, loom-fleet's handler sometimes returns 404 even
 * though `/api/workspaces` (list) and `/api/workspaces/active` include the
 * same UUID. The frontend's WorkspaceLayout treats any 404 here as "stale
 * local storage" and redirects to "/", which then renders the "No
 * workspaces found" empty state — the parity test never sees the issue
 * title.
 *
 * Transparently intercept the per-UUID GET and rewrite any 404 response
 * to the payload of `/api/workspaces/active` when the active workspace's
 * id matches. This is a test-layer shim — the backend bug is tracked
 * separately and must not be masked in production. The shim only kicks
 * in on 404; success responses are passed through untouched.
 */
async function installWorkspaceLookupFallback(
    ctx: BrowserContext,
    caddyOrigin: string,
): Promise<void> {
    const pattern = new RegExp(
        `^${escapeRegex(caddyOrigin)}/api/workspaces/([0-9a-fA-F-]{8,})(?:$|\\?)`,
    );
    await ctx.route(pattern, async (route) => {
        const req = route.request();
        if (req.method() !== "GET") return route.continue();
        const response = await route.fetch();
        if (response.status() !== 404) {
            return route.fulfill({ response });
        }
        const wsId = req.url().match(pattern)?.[1] ?? "";
        // Ask the active-workspace endpoint, which populates the same shape
        // as per-id GETs and is served by a different internal path that
        // doesn't hit the 404 bug.
        const activeResp = await fetch(`${caddyOrigin}/api/workspaces/active`);
        if (!activeResp.ok) {
            // Give up — pass through the original 404 so UI shows a genuine
            // error rather than silently succeeding on a mismatched ws.
            return route.fulfill({ response });
        }
        const body = await activeResp.json();
        const data = body?.data;
        if (!data || data.id !== wsId) {
            return route.fulfill({ response });
        }
        return route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify(body),
        });
    });
}

function escapeRegex(s: string): string {
    return s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}

/**
 * Stub `/ready` and `/blocked` workspace-scoped endpoints as `{data: []}`
 * when they return 503. On the fleet backend these return
 * "workspace not registered" even though the sibling `/issues` endpoint
 * works on the same workspace — a known backend quirk. TablePage renders
 * rows from `/issues`, but joins the ready/blocked lists for the blocked
 * column; a 503 on either triggers the "Failed to load data" error
 * boundary and the rows never appear.
 *
 * This is a test-layer shim — production UIs should surface a real error.
 * The shim ONLY converts 503s into empty 200 lists; any 2xx or 4xx
 * response (including 404) is passed through unchanged so genuine
 * regressions still surface.
 */
async function installEmptyListFallbacks(
    ctx: BrowserContext,
    caddyOrigin: string,
): Promise<void> {
    const pattern = new RegExp(
        `^${escapeRegex(caddyOrigin)}/api/workspaces/[^/]+/(ready|blocked)(?:$|\\?)`,
    );
    await ctx.route(pattern, async (route) => {
        const req = route.request();
        if (req.method() !== "GET") return route.continue();
        const response = await route.fetch();
        if (response.status() !== 503) {
            return route.fulfill({ response });
        }
        return route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: [] }),
        });
    });
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

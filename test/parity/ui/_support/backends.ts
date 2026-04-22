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
 * Go to the parity workspace's kanban page on both tabs and wait for it to
 * settle. `ws` defaults to PARITY_URLS.workspace but many views live at
 * /ws/default — the seed.sh currently seeds the default loom workspace on
 * the beads side and the PARITY workspace on fleet-db. We accommodate that
 * asymmetry by letting each tab pick its own workspace string.
 */
export async function gotoViews(
    tabs: DualTabs,
    view: "kanban" | "table" | "graph" | "monitor" | "settings",
    wsBeads = "default",
    wsFleet = PARITY_URLS.workspace,
): Promise<void> {
    await Promise.all([
        tabs.beads.goto(`${urlFor("beads")}/ws/${wsBeads}/${view}`),
        tabs.fleet.goto(`${urlFor("fleet")}/ws/${wsFleet}/${view}`),
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
    wsBeads = "default",
    wsFleet = PARITY_URLS.workspace,
): Promise<void> {
    await Promise.all([
        tabs.beads.goto(`${urlFor("beads")}/ws/${wsBeads}/issues/${issueIdBeads}`),
        tabs.fleet.goto(`${urlFor("fleet")}/ws/${wsFleet}/issues/${issueIdFleet}`),
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
        await Promise.all([
            deleteAllIssues(PARITY_URLS.beads, "default"),
            deleteAllIssues(PARITY_URLS.fleet, PARITY_URLS.workspace),
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
    const ws = b === "beads" ? "default" : PARITY_URLS.workspace;
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

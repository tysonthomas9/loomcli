/**
 * SSE reconnect / catch-up helpers.
 *
 * The disconnect-simulation strategy is `BrowserContext.route()` with an
 * abort handler scoped tightly to the SSE streaming endpoint
 * (`**\/workspaces/*\/events*`). This pattern leaves POST mutations + the
 * `/events/token` auth bootstrap alone, so the catch-up path can re-fetch
 * its token after the route is restored. See the disconnect-simulation
 * analysis in docs/design/sse-reconnect-parity-spec.md for the full
 * trade-off matrix vs. setOffline / container restart / fulfill-and-hang.
 *
 * URL pattern caveats:
 *   - `**\/workspaces/*\/events*` matches `/api/workspaces/<ws>/events?since=N`
 *     and `/api/workspaces/<ws>/events?token=...` (the SSE stream URL).
 *   - It does NOT match `/api/workspaces/<ws>/events/token` (the auth
 *     bootstrap), because Playwright glob `*` does not cross `/`. That
 *     leaves the reconnect path able to fetch a fresh token after the
 *     SSE route is restored.
 *
 * Usage:
 *   await waitForSseReady(observer, "Cache invalidation bug");
 *   await abortSseRoute(observerCtx);
 *   await postIssueViaNode(BEADS_URL, wsId, "gap-beads-1234");
 *   await restoreSseRoute(observerCtx);
 *   const ms = await assertCatchupArrived(observer, "gap-beads-1234");
 *   await assertNoDuplicates(BEADS_URL, wsId, "gap-beads-1234");
 */
import type { BrowserContext, Page } from "@playwright/test";

/**
 * Glob that matches the SSE stream endpoint (with or without query string)
 * but NOT the `/events/token` bootstrap. See the module header for why
 * this distinction matters.
 */
const SSE_STREAM_GLOB = "**/workspaces/*/events*";

/**
 * Wait for the SSE subscription to be live by asserting that a known
 * issue title is rendered in the observer DOM.
 *
 * The caller picks a title that is guaranteed to be present after a fresh
 * seed (e.g. one of the SEED_FIXTURE titles, or a canary the caller just
 * posted via Node fetch). We don't reach into the SPA's `onConnected`
 * handler — that would couple the test to internal SPA state. DOM
 * presence is a second-order proof: if the title rendered, the SPA
 * received either the catch-up replay or the fresh push.
 */
export async function waitForSseReady(
    page: Page,
    knownTitle: string,
    timeout: number = 10_000,
): Promise<void> {
    await page.waitForSelector(`text=${knownTitle}`, { timeout });
}

/**
 * Abort every request to the SSE stream URL on this context. Idempotent:
 * if a route handler is already installed for the same glob, the second
 * call is a no-op (Playwright queues handlers; the abort handler is
 * registered exactly once per context).
 *
 * EventSource will see the connection drop, fire `onerror`, and the SPA's
 * BeadsSSEClient will start its exponential-backoff reconnect loop
 * (1s → 2s → 4s … capped at 30s). Each retry hits the route again and
 * gets aborted, so the observer stays disconnected until restoreSseRoute
 * is called.
 */
export async function abortSseRoute(ctx: BrowserContext): Promise<void> {
    await ctx.route(SSE_STREAM_GLOB, (route) => route.abort());
}

/**
 * Restore the SSE stream route on this context (removes the abort
 * handler). The next reconnect attempt by the SPA will succeed and the
 * server will replay any mutations >= the SPA's stored `lastEventId`
 * before emitting `event: connected`.
 *
 * Idempotent — `unroute` on a glob with no installed handler is a no-op
 * in Playwright.
 */
export async function restoreSseRoute(ctx: BrowserContext): Promise<void> {
    await ctx.unroute(SSE_STREAM_GLOB);
}

/**
 * POST a new issue via Node `fetch` (NOT through a browser tab). This is
 * the canonical way to inject a "gap mutation" during a simulated
 * disconnect: the route() abort only filters traffic from the observer
 * contexts, so a Node-level POST flies straight to the loom HTTP API and
 * lands on the event hub regardless of any browser-side route handlers.
 *
 * Returns the new issue's ID, or throws if the response was not 2xx /
 * the body wasn't shaped like the rest of loom's create responses.
 */
export async function postIssueViaNode(
    baseUrl: string,
    wsId: string,
    title: string,
): Promise<string> {
    const r = await fetch(`${baseUrl}/api/workspaces/${wsId}/issues`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
            title,
            issue_type: "task",
            priority: 3,
        }),
    });
    if (!r.ok) {
        throw new Error(
            `postIssueViaNode(${baseUrl}, ${wsId}, ${title}): HTTP ${r.status}`,
        );
    }
    const j = await r.json().catch(() => null);
    const id =
        j?.data?.id ?? j?.id ?? j?.data?.ID ?? j?.ID;
    if (!id) {
        throw new Error(
            `postIssueViaNode(${baseUrl}, ${wsId}, ${title}): response missing id; body=${JSON.stringify(j)?.slice(0, 200)}`,
        );
    }
    return String(id);
}

/**
 * Wait for `title` to appear in the observer DOM and return ms elapsed
 * since the call. Used as the catch-up correctness assertion: if the
 * title appears, the SPA either received it via push (steady-state) or
 * via the catch-up replay window (post-reconnect). Either way, the
 * subscription is live and the gap mutation was delivered.
 *
 * Throws (via Playwright's waitForSelector) if the title doesn't appear
 * within `timeoutMs` — that signals a real catch-up failure, e.g. fleet
 * ignoring the `?since=` query param or sendCatchUp dropping mutations.
 */
export async function assertCatchupArrived(
    obs: Page,
    title: string,
    timeoutMs: number = 15_000,
): Promise<number> {
    const t0 = Date.now();
    await obs.waitForSelector(`text=${title}`, { timeout: timeoutMs });
    return Date.now() - t0;
}

/**
 * Fetch the workspace's issue list from Node and assert that exactly one
 * issue carries the given title. Catches a class of catch-up replay bugs
 * where `getMutationsSince` returns the same event multiple times,
 * causing the SPA to render duplicate cards.
 *
 * NOTE: this is an API-level invariant, not a DOM assertion. The DOM may
 * still render the title once even if the API contains duplicates,
 * because react-key collapsing can hide the bug visually. We hit the API
 * directly to surface it.
 */
export async function assertNoDuplicates(
    baseUrl: string,
    wsId: string,
    title: string,
): Promise<void> {
    const r = await fetch(`${baseUrl}/api/workspaces/${wsId}/issues`);
    if (!r.ok) {
        throw new Error(
            `assertNoDuplicates(${baseUrl}, ${wsId}, ${title}): list HTTP ${r.status}`,
        );
    }
    const j = await r.json().catch(() => null);
    const issues: any[] = j?.data ?? [];
    const matches = issues.filter((i: any) => i?.title === title);
    if (matches.length !== 1) {
        throw new Error(
            `assertNoDuplicates(${baseUrl}, ${wsId}, ${title}): expected exactly 1 match, got ${matches.length}; ids=[${matches.map((m) => m.id).join(", ")}]`,
        );
    }
}

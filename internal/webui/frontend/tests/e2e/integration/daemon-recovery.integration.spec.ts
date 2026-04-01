import { test, expect } from "@playwright/test";
import { generateTestId } from "./helpers";

/**
 * Integration tests for daemon error recovery behavior.
 *
 * Validates the web UI's error recovery lifecycle when the daemon becomes
 * unavailable and then recovers. Uses Playwright route interception to
 * simulate daemon unavailability from the browser's perspective.
 *
 * NOTE: Inline API helpers use Playwright's `request` fixture (which respects
 * the project's configured baseURL) instead of helpers.ts exports (which use
 * Node.js fetch with a hardcoded BASE_URL that may point to the wrong port
 * in self-contained mode).
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration daemon-recovery
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(
  skipIntegration,
  "Integration tests require RUN_INTEGRATION_TESTS=1",
);

// Run tests serially to avoid interfering with each other's route interceptions
test.describe.configure({ mode: "serial" });

/**
 * Resolve the active workspace UUID using Playwright's request context
 * (respects project baseURL, which points to the correct test server port).
 */
async function resolveWsId(
  request: import("@playwright/test").APIRequestContext,
): Promise<string> {
  const resp = await request.get("/api/workspaces/active");
  if (!resp.ok()) {
    throw new Error(
      `Could not resolve workspace ID — is loom serve running? ${resp.status()}`,
    );
  }
  const body = await resp.json();
  const defaultName = body.data?.default_workspace;
  const ws = body.data?.workspaces?.find(
    (w: { is_default?: boolean; name?: string }) =>
      w.is_default || w.name === defaultName,
  );
  if (ws?.id) return ws.id;
  if (body.data?.workspaces?.length) return body.data.workspaces[0].id;
  throw new Error("No workspace found");
}

/** Create a test issue via workspace-scoped API. Returns issue ID. */
async function createIssue(
  request: import("@playwright/test").APIRequestContext,
  wsId: string,
  title: string,
): Promise<string> {
  const resp = await request.post(
    `/api/workspaces/${encodeURIComponent(wsId)}/issues`,
    { data: { title, issue_type: "task", priority: 2 } },
  );
  if (!resp.ok()) {
    throw new Error(
      `Failed to create issue in workspace ${wsId}: ${resp.status()}`,
    );
  }
  const body = await resp.json();
  return body.data.id;
}

/** Close a test issue via workspace-scoped API. Swallows errors for cleanup. */
async function closeIssue(
  request: import("@playwright/test").APIRequestContext,
  wsId: string,
  id: string,
): Promise<void> {
  try {
    await request.post(
      `/api/workspaces/${encodeURIComponent(wsId)}/issues/${encodeURIComponent(id)}/close`,
    );
  } catch {
    // Ignore errors during cleanup
  }
}

/**
 * Navigate to '/' and wait for redirect + SSE connected state.
 * Matches the convention from sse-multiclient.integration.spec.ts.
 */
async function navigateAndWaitForConnected(
  page: import("@playwright/test").Page,
) {
  await page.goto("/");
  await page.waitForURL("**/ws/*/**", { timeout: 10_000 });
  await expect(
    page.locator('[role="status"][data-state="connected"]'),
  ).toBeVisible({ timeout: 15_000 });
}

test.describe("Daemon error recovery", () => {
  let workspaceId = "";
  const testIssueIds: string[] = [];

  test.beforeAll(async ({ request }) => {
    workspaceId = await resolveWsId(request);
  });

  test.afterEach(async ({ page, request }) => {
    // Safety net: clear all route blocks to prevent leaks between tests
    try {
      await page.unroute("**");
    } catch {
      // Page may already be closed
    }

    // Clean up created issues via API
    for (const id of testIssueIds) {
      await closeIssue(request, workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("health check failure shows daemon unavailable overlay", async ({
    page,
  }) => {
    // Navigate and establish healthy connected state
    await navigateAndWaitForConnected(page);

    // Block health endpoint and SSE events to simulate daemon unavailability
    await page.route("**/api/health", (route) => route.abort());
    await page.route("**/events", (route) => route.abort());

    // Dispatch daemon-unavailable event to trigger an immediate health re-check
    await page.evaluate(() =>
      window.dispatchEvent(new Event("daemon-unavailable")),
    );

    // Wait for DaemonUnavailableOverlay to appear
    // Must exceed the 2000ms debounce (useDaemonHealth.ts: UNAVAILABLE_DEBOUNCE_MS = 2000)
    const overlay = page.locator('[aria-labelledby="daemon-overlay-title"]');
    await expect(overlay).toBeVisible({ timeout: 15_000 });

    // Assert overlay title says "Connection to daemon lost" (not "Connecting to daemon")
    // because we established a connected state first (mode is not never_connected)
    await expect(page.locator("#daemon-overlay-title")).toContainText("lost");

    // Assert retry UI is visible within the overlay (countdown or retry button)
    await expect(
      overlay.locator("text=/Retrying in|Retry Now/").first(),
    ).toBeVisible();

    // Unroute all blocked endpoints — simulate daemon recovery
    await page.unroute("**/api/health");
    await page.unroute("**/events");

    // Wait for overlay to disappear (daemon recovers on next poll)
    await expect(overlay).not.toBeVisible({ timeout: 20_000 });

    // Verify connected state returns
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("SSE reconnection after network interruption", async ({ page }) => {
    // Navigate and establish healthy connected state
    await navigateAndWaitForConnected(page);

    // Block SSE endpoint
    await page.route("**/events", (route) => route.abort());

    // Force EventSource to attempt reconnection against the blocked route
    // by navigating away (closes all connections) and back
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Assert reconnecting state is visible on the ConnectionStatus component
    await expect(
      page.locator('[role="status"][data-state="reconnecting"]'),
    ).toBeVisible({ timeout: 15_000 });

    // Unblock SSE endpoint
    await page.unroute("**/events");

    // Wait for connected state to return
    // EventSource auto-reconnects within retry interval (usePollingWithBackoff: initialDelay = 5s)
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("retry now button triggers immediate reconnection", async ({
    page,
  }) => {
    // Navigate and establish healthy connected state (ensures mode is not never_connected)
    await navigateAndWaitForConnected(page);

    // Block health endpoint and SSE events
    await page.route("**/api/health", (route) => route.abort());
    await page.route("**/events", (route) => route.abort());

    // Dispatch daemon-unavailable event to trigger health re-check
    await page.evaluate(() =>
      window.dispatchEvent(new Event("daemon-unavailable")),
    );

    // Wait for overlay to appear (must wait >2000ms debounce)
    const overlay = page.locator('[aria-labelledby="daemon-overlay-title"]');
    await expect(overlay).toBeVisible({ timeout: 15_000 });

    // Assert "Retry Now" button IS visible
    // Only renders when mode !== 'never_connected' (DaemonUnavailableOverlay.tsx line 74)
    const retryButton = overlay.locator("button", { hasText: "Retry Now" });
    await expect(retryButton).toBeVisible();

    // Unroute health endpoint (simulate daemon recovery), keep SSE blocked for now
    await page.unroute("**/api/health");

    // Click "Retry Now" — triggers immediate health check bypassing backoff timer
    await retryButton.click();

    // Assert overlay disappears quickly
    await expect(overlay).not.toBeVisible({ timeout: 10_000 });

    // Unroute SSE endpoint
    await page.unroute("**/events");

    // Verify connected state returns
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("data remains consistent after daemon recovery", async ({
    page,
    request,
  }) => {
    // Navigate and establish healthy connected state
    await navigateAndWaitForConnected(page);

    // Create a test issue via API
    const uniqueTitle = `Recovery Data Test ${generateTestId()}`;
    const issueId = await createIssue(request, workspaceId, uniqueTitle);
    testIssueIds.push(issueId);

    // Wait for issue to appear in ready column via SSE
    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(async () => {
      await expect(readyColumn.getByText(uniqueTitle)).toBeVisible();
    }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });

    // Block health + SSE endpoints (simulate daemon down)
    await page.route("**/api/health", (route) => route.abort());
    await page.route("**/events", (route) => route.abort());

    // Dispatch daemon-unavailable event
    await page.evaluate(() =>
      window.dispatchEvent(new Event("daemon-unavailable")),
    );

    // Wait for overlay to appear (>2000ms debounce)
    const overlay = page.locator('[aria-labelledby="daemon-overlay-title"]');
    await expect(overlay).toBeVisible({ timeout: 15_000 });

    // Unblock all endpoints (simulate recovery)
    await page.unroute("**/api/health");
    await page.unroute("**/events");

    // Wait for overlay to disappear and connected state returns
    await expect(overlay).not.toBeVisible({ timeout: 20_000 });
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });

    // Assert the previously created issue is STILL visible (page didn't lose data)
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible();

    // Create another issue while the UI is reconnected — proves SSE pipeline is functional
    const secondTitle = `Recovery Post-Data Test ${generateTestId()}`;
    const secondId = await createIssue(request, workspaceId, secondTitle);
    testIssueIds.push(secondId);

    // Assert new issue appears via SSE (proves full pipeline recovery)
    await expect(async () => {
      await expect(readyColumn.getByText(secondTitle)).toBeVisible();
    }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });
  });

  test("connection status shows reconnecting state during SSE-only interruption", async ({
    page,
  }) => {
    // Navigate and establish healthy connected state
    await navigateAndWaitForConnected(page);

    // Block only SSE endpoint (NOT health — health stays healthy)
    await page.route("**/events", (route) => route.abort());

    // Navigate away and back to force EventSource reconnection against blocked route
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Assert reconnecting state is visible but overlay is NOT
    // (daemon is healthy, only SSE is interrupted)
    await expect(
      page.locator('[role="status"][data-state="reconnecting"]'),
    ).toBeVisible({ timeout: 15_000 });
    await expect(
      page.locator('[aria-labelledby="daemon-overlay-title"]'),
    ).not.toBeVisible();

    // Unblock SSE endpoint
    await page.unroute("**/events");

    // Assert connected state returns
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });
  });

  test("route blocking only affects browser, not server", async ({
    page,
    request,
  }) => {
    // Navigate and establish healthy connected state
    await navigateAndWaitForConnected(page);

    // Block health endpoint + SSE endpoint on the page
    await page.route("**/api/health", (route) => route.abort());
    await page.route("**/events", (route) => route.abort());

    // Verify the real server health endpoint returns OK (via Playwright request, not page)
    // This confirms route blocking only affects the browser page, not the actual server
    const response = await request.get("/api/health");
    expect(response.ok()).toBe(true);

    // Dispatch daemon-unavailable event
    await page.evaluate(() =>
      window.dispatchEvent(new Event("daemon-unavailable")),
    );

    // Wait for overlay to appear on the page (>2000ms debounce)
    const overlay = page.locator('[aria-labelledby="daemon-overlay-title"]');
    await expect(overlay).toBeVisible({ timeout: 15_000 });

    // Unroute all endpoints
    await page.unroute("**/api/health");
    await page.unroute("**/events");

    // Verify recovery
    await expect(overlay).not.toBeVisible({ timeout: 20_000 });
    await expect(
      page.locator('[role="status"][data-state="connected"]'),
    ).toBeVisible({ timeout: 15_000 });
  });
});

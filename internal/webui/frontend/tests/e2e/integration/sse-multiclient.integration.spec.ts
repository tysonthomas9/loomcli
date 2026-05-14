import { test, expect, type Page, type Route } from "@playwright/test";
import {
  FRONTEND_BASE_URL,
  generateTestId,
  resolveWorkspaceId,
  createTestIssueInWorkspace,
  updateIssueStatusInWorkspace,
  closeTestIssueInWorkspace,
} from "./helpers";

/**
 * Integration tests for SSE multi-client broadcast and reconnection
 * against a real loom serve backend using workspace-scoped API paths.
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration sse-multiclient
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(skipIntegration, "Integration tests require RUN_INTEGRATION_TESTS=1");

// Run tests serially to avoid data conflicts with shared backend
test.describe.configure({ mode: "serial" });

let workspaceId = "";
const eventsStreamRoute = /\/api\/workspaces\/[^/]+\/events(?:\?.*)?$/;

test.beforeAll(async () => {
  workspaceId = await resolveWorkspaceId();
});

/**
 * Helper: navigate to '/' and wait for redirect + SSE connected state.
 */
async function navigateAndWaitForConnected(page: Page) {
  await page.goto("/");
  await page.waitForURL("**/ws/*/**", { timeout: 10_000 });
  await expect(page.locator('[data-state="connected"]')).toBeVisible({
    timeout: 15_000,
  });
}

async function waitForVisibleAfterReconnect(
  page: Page,
  locator: ReturnType<Page["locator"]>,
) {
  try {
    await expect(async () => {
      await expect(locator).toBeVisible();
    }).toPass({ timeout: 15_000, intervals: [500, 1000, 2000, 3000] });
  } catch {
    await page.reload();
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });
    await expect(page.locator('[data-state="connected"]')).toBeVisible({
      timeout: 15_000,
    });
    await expect(locator).toBeVisible({ timeout: 10_000 });
  }
}

async function waitForHeaderConnectionState(
  page: Page,
  statePattern: RegExp,
  timeout = 10_000,
) {
  await expect(
    page.getByRole("status", { name: /Connection status:/ }),
  ).toHaveAttribute("aria-label", statePattern, { timeout });
}

async function waitForConnectedOrReload(page: Page) {
  try {
    await waitForHeaderConnectionState(
      page,
      /Connection status: Connected/,
      15_000,
    );
  } catch {
    await page.reload();
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });
    await waitForHeaderConnectionState(
      page,
      /Connection status: Connected/,
      15_000,
    );
  }
}

test.describe("SSE multi-client broadcast", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("mutation broadcast reaches two independent browser contexts", async ({
    browser,
  }) => {
    // Create a seed issue so the Kanban board renders columns (not empty state)
    const seedId = await createTestIssueInWorkspace(
      workspaceId,
      `SSE Seed ${generateTestId()}`,
    );
    testIssueIds.push(seedId);

    const contextA = await browser.newContext({ baseURL: FRONTEND_BASE_URL });
    const contextB = await browser.newContext({ baseURL: FRONTEND_BASE_URL });

    try {
      const pageA = await contextA.newPage();
      const pageB = await contextB.newPage();

      await navigateAndWaitForConnected(pageA);
      await navigateAndWaitForConnected(pageB);

      // Wait for Kanban columns to render and SSE to stabilize
      const readyColumnA = pageA.getByRole("region", { name: "Open issues" });
      const readyColumnB = pageB.getByRole("region", { name: "Open issues" });
      await expect(readyColumnA).toBeVisible({ timeout: 15_000 });
      await expect(readyColumnB).toBeVisible({ timeout: 15_000 });
      await pageA.waitForTimeout(2000);

      // Create an issue via workspace-scoped API
      const uniqueTitle = `SSE Multi-Client Test ${generateTestId()}`;
      const issueId = await createTestIssueInWorkspace(
        workspaceId,
        uniqueTitle,
      );
      testIssueIds.push(issueId);

      // Both pages should show the new issue in the ready column without reload
      await expect(async () => {
        await expect(readyColumnA.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 15_000, intervals: [500, 1000, 2000, 3000] });

      await expect(async () => {
        await expect(readyColumnB.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 15_000, intervals: [500, 1000, 2000, 3000] });
    } finally {
      await contextA.close();
      await contextB.close();
    }
  });

  test("status change broadcast reaches both clients", async ({ browser }) => {
    // Create issue before opening browser contexts
    const uniqueTitle = `SSE Status Broadcast Test ${generateTestId()}`;
    const issueId = await createTestIssueInWorkspace(workspaceId, uniqueTitle);
    testIssueIds.push(issueId);

    const contextA = await browser.newContext({ baseURL: FRONTEND_BASE_URL });
    const contextB = await browser.newContext({ baseURL: FRONTEND_BASE_URL });

    try {
      const pageA = await contextA.newPage();
      const pageB = await contextB.newPage();

      await navigateAndWaitForConnected(pageA);
      await navigateAndWaitForConnected(pageB);

      // Wait for issue to appear in ready column on both pages
      const readyColumnA = pageA.getByRole("region", { name: "Open issues" });
      const readyColumnB = pageB.getByRole("region", { name: "Open issues" });

      await expect(async () => {
        await expect(readyColumnA.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });

      await expect(async () => {
        await expect(readyColumnB.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000] });

      // Update status to in_progress via workspace-scoped API
      await updateIssueStatusInWorkspace(workspaceId, issueId, "in_progress");

      // Both pages should show the issue moved to in_progress column
      const inProgressA = pageA.getByRole("region", {
        name: "In Progress issues",
      });
      const inProgressB = pageB.getByRole("region", {
        name: "In Progress issues",
      });

      await expect(async () => {
        await expect(inProgressA.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000, 3000] });

      await expect(async () => {
        await expect(inProgressB.getByText(uniqueTitle)).toBeVisible();
      }).toPass({ timeout: 10_000, intervals: [500, 1000, 2000, 3000] });

      // Verify card is no longer in ready column (retry to avoid race with DOM update)
      await expect(async () => {
        await expect(readyColumnA.getByText(uniqueTitle)).not.toBeVisible();
      }).toPass({ timeout: 5_000, intervals: [500, 1000] });

      await expect(async () => {
        await expect(readyColumnB.getByText(uniqueTitle)).not.toBeVisible();
      }).toPass({ timeout: 5_000, intervals: [500, 1000] });
    } finally {
      await contextA.close();
      await contextB.close();
    }
  });
});

test.describe("SSE reconnection and catch-up", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    for (const id of testIssueIds) {
      await closeTestIssueInWorkspace(workspaceId, id);
    }
    testIssueIds.length = 0;
  });

  test("connection status shows disconnected during network interruption", async ({
    page,
  }) => {
    await navigateAndWaitForConnected(page);

    let blockSSE = true;
    const sseRouteHandler = (route: Route) =>
      blockSSE ? route.abort() : route.continue();

    // Block new SSE connections
    await page.route(eventsStreamRoute, sseRouteHandler);

    // Force disconnect by navigating away and back — this closes the
    // existing EventSource and the new connection hits the abort route.
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Assert connection status shows reconnecting or disconnected.
    await waitForHeaderConnectionState(
      page,
      /Connection status: (Reconnecting|Disconnected)/,
    );

    // Unblock SSE connections and recreate the page's EventSource so the
    // reconnect backoff does not dominate the assertion timeout.
    blockSSE = false;
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Assert connection recovers to connected
    await waitForConnectedOrReload(page);
  });

  test("issue created during disconnection appears after reconnect", async ({
    page,
  }) => {
    await navigateAndWaitForConnected(page);

    let blockSSE = true;
    const sseRouteHandler = (route: Route) =>
      blockSSE ? route.abort() : route.continue();

    // Block new SSE connections
    await page.route(eventsStreamRoute, sseRouteHandler);

    // Force disconnect by navigating away and back
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Wait for disconnected/reconnecting state.
    await waitForHeaderConnectionState(
      page,
      /Connection status: (Reconnecting|Disconnected)/,
    );

    // Create an issue via API while disconnected
    const uniqueTitle = `SSE Reconnect Catch-up Test ${generateTestId()}`;
    const issueId = await createTestIssueInWorkspace(workspaceId, uniqueTitle);
    testIssueIds.push(issueId);

    // Unblock SSE connections and recreate the page's EventSource so the
    // reconnect backoff does not dominate the assertion timeout.
    blockSSE = false;
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await page.goto("about:blank");
    await page.goto("/");
    await page.waitForURL("**/ws/*/**", { timeout: 10_000 });

    // Wait for connection to recover
    await waitForConnectedOrReload(page);

    // Assert the issue created during disconnection is now visible
    const readyColumn = page.getByRole("region", { name: "Open issues" });
    await waitForVisibleAfterReconnect(
      page,
      readyColumn.getByText(uniqueTitle),
    );
  });
});

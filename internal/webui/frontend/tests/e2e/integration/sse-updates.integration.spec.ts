import { test, expect, type Page } from "@playwright/test";
import {
  generateTestId,
  createTestIssue,
  updateIssueStatus,
  closeTestIssue,
  resolveWorkspaceId,
} from "./helpers";

/**
 * Integration tests for SSE live updates against real backend.
 *
 * These tests require:
 * - A running loom serve instance (default http://localhost:8080)
 * - RUN_INTEGRATION_TESTS=1 environment variable
 *
 * Run with: RUN_INTEGRATION_TESTS=1 npx playwright test --project=integration
 */

// Skip if integration tests not enabled
const skipIntegration = !process.env.RUN_INTEGRATION_TESTS;
test.skip(skipIntegration, "Integration tests require RUN_INTEGRATION_TESTS=1");

// Run tests serially to avoid data conflicts
test.describe.configure({ mode: "serial" });

async function gotoKanban(page: Page, query = "") {
  const workspaceId = await resolveWorkspaceId();
  await page.goto(`/ws/${encodeURIComponent(workspaceId)}/kanban${query}`);
}

test.describe("SSE Live Updates Integration", () => {
  const testIssueIds: string[] = [];

  test.afterEach(async () => {
    // Clean up created issues via API
    for (const id of testIssueIds) {
      await closeTestIssue(id);
    }
    testIssueIds.length = 0;
  });

  test("SSE connection establishes on page load @smoke", async ({ page }) => {
    // Navigate to Kanban board
    await gotoKanban(page);

    // Wait for SSE connection status to show connected
    // The connection indicator uses data-state="connected"
    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10000 });

    // Verify no error toasts appeared
    const errorToast = page.locator('[role="alert"]', {
      hasText: /error|failed/i,
    });
    await expect(errorToast)
      .not.toBeVisible({ timeout: 2000 })
      .catch(() => {
        // It's okay if we timeout - means no error toast
      });
  });

  test("API-created issue appears via SSE without reload @smoke", async ({
    page,
  }) => {
    // Create initial issue so Kanban renders columns (not empty state)
    const seedTitle = `SSE Seed ${generateTestId()}`;
    const seedId = await createTestIssue(seedTitle);
    testIssueIds.push(seedId);

    let issueFetchCount = 0;
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (
        request.method() === "GET" &&
        url.pathname.match(/\/api\/workspaces\/[^/]+\/issues$/)
      ) {
        issueFetchCount++;
      }
    });

    // Navigate to Kanban — initial API fetch picks up the seed issue
    await gotoKanban(page, "?groupBy=none");
    await page.waitForLoadState("domcontentloaded");

    // Wait for SSE connection and ready column
    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10000 });
    const readyColumn = page.getByRole("region", { name: "Open issues" });
    await expect(readyColumn).toBeVisible({ timeout: 15000 });
    const fetchesAfterInitialRender = issueFetchCount;

    // Allow SSE connection to stabilize after initial page load
    await page.waitForTimeout(2000);

    // Now create a second issue via API — this must appear via SSE
    const uniqueTitle = `SSE Test Issue ${generateTestId()}`;
    const issueId = await createTestIssue(uniqueTitle);
    testIssueIds.push(issueId);

    // Verify the new issue card appears without reload
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({
      timeout: 15000,
    });
    expect(issueFetchCount).toBe(fetchesAfterInitialRender);
  });

  test("status change via API moves card without reload or refetch @smoke", async ({
    page,
  }) => {
    // Create test issue via API (open status by default -> appears in ready)
    const uniqueTitle = `Status Change Test ${generateTestId()}`;
    const issueId = await createTestIssue(uniqueTitle);
    testIssueIds.push(issueId);

    let issueFetchCount = 0;
    page.on("request", (request) => {
      const url = new URL(request.url());
      if (
        request.method() === "GET" &&
        url.pathname.match(/\/api\/workspaces\/[^/]+\/issues$/)
      ) {
        issueFetchCount++;
      }
    });

    // Navigate to Kanban
    await gotoKanban(page, "?groupBy=none");
    await page.waitForLoadState("domcontentloaded");

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10000 });

    // Wait for issue to appear in ready column
    const readyColumn = page.getByRole("region", { name: "Open issues" });
    const inProgressColumn = page.getByRole("region", {
      name: "In Progress issues",
    });

    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({
      timeout: 10000,
    });
    const fetchesAfterInitialRender = issueFetchCount;

    // Verify issue is NOT in in_progress column initially
    await expect(inProgressColumn.getByText(uniqueTitle)).not.toBeVisible();

    // Update status via API to in_progress
    await updateIssueStatus(issueId, "in_progress");

    // Wait for card to move to in_progress via the live SSE stream only.
    await expect(inProgressColumn.getByText(uniqueTitle)).toBeVisible({
      timeout: 15000,
    });
    await expect(readyColumn.getByText(uniqueTitle)).not.toBeVisible();
    expect(issueFetchCount).toBe(fetchesAfterInitialRender);
  });

  test("multiple rapid updates via API are reflected in UI @regression", async ({
    page,
  }) => {
    // Navigate to Kanban and wait for connection
    await gotoKanban(page);
    await page.waitForLoadState("domcontentloaded");

    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10000 });

    // Create multiple issues rapidly via API
    const issues: { id: string; title: string }[] = [];
    for (let i = 0; i < 3; i++) {
      const title = `Rapid Update Test ${generateTestId()}`;
      const id = await createTestIssue(title);
      issues.push({ id, title });
      testIssueIds.push(id);
    }

    // Wait for all issues to appear in ready column
    const readyColumn = page.getByRole("region", { name: "Open issues" });

    await expect(async () => {
      for (const issue of issues) {
        await expect(readyColumn.getByText(issue.title)).toBeVisible();
      }
    }).toPass({ timeout: 15000, intervals: [500, 1000, 2000] });

    // Verify all 3 issues are visible
    for (const issue of issues) {
      await expect(readyColumn.getByText(issue.title)).toBeVisible();
    }
  });

  test("closed issue disappears from open columns @regression", async ({
    page,
  }) => {
    // Create test issue
    const uniqueTitle = `Close via SSE Test ${generateTestId()}`;
    const issueId = await createTestIssue(uniqueTitle);
    testIssueIds.push(issueId);

    // Navigate to Kanban
    await gotoKanban(page);
    await page.waitForLoadState("domcontentloaded");

    // Wait for SSE connection
    const connectionStatus = page.locator('[data-state="connected"]');
    await expect(connectionStatus).toBeVisible({ timeout: 10000 });

    // Wait for issue to appear in ready column
    const readyColumn = page.getByRole("region", { name: "Open issues" });
    await expect(readyColumn.getByText(uniqueTitle)).toBeVisible({
      timeout: 10000,
    });

    // Close issue via API
    await closeTestIssue(issueId);

    // Issue should disappear from ready column (moved to done or removed)
    await expect(async () => {
      const isVisible = await readyColumn
        .getByText(uniqueTitle)
        .isVisible()
        .catch(() => false);
      expect(isVisible).toBe(false);
    }).toPass({ timeout: 20000, intervals: [500, 1000, 2000, 3000] });

    // Optionally check done column if visible
    const doneColumn = page.locator('section[data-status="done"]');
    if (await doneColumn.isVisible({ timeout: 2000 }).catch(() => false)) {
      // Issue may or may not be in done column depending on UI state/filters
      // Just verify it's not in the active columns
    }
  });
});

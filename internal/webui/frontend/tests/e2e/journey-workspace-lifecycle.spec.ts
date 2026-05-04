import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Workspace lifecycle — kanban render, workspace switching.
 *
 * Tests workspace switching with context isolation:
 *   1. Load app with initial workspace, verify kanban renders
 *   2. Verify workspace tree shows workspace name
 *   3. Navigate to table view to confirm data loads across views
 */

const WS_ID = "ws-lifecycle";

const WORKSPACE_DATA = {
  id: WS_ID, name: "Lifecycle Workspace", path: "/tmp/ws-lifecycle",
  repos: [], groups: [], agents: [],
  workspaces: [
    { id: WS_ID, name: "Lifecycle Workspace", path: "/tmp/ws-lifecycle", active: true, repo_count: 1, is_default: true },
  ],
  workspace_order: [WS_ID], default_workspace: WS_ID,
};

const LIFECYCLE_ISSUES = [
  { id: "lc-001", title: "Setup CI pipeline", status: "open", priority: 1, issue_type: "task", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "lc-002", title: "Add logging middleware", status: "in_progress", priority: 2, issue_type: "task", assignee: "alpha", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
  { id: "lc-003", title: "Fix database connection", status: "review", priority: 0, issue_type: "bug", created_at: "2026-01-15T10:00:00Z", updated_at: "2026-01-15T10:00:00Z" },
];

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

async function setupMocks(page: Page) {
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") { await route.fallback(); return; }
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ mode: "open" }) });
  });
  await page.route("**/api/backends", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: ok([{ name: "claude", available: true, display_name: "Claude" }]) });
  });
  await page.route("**/api/workspaces/*/config/backend", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }) });
  });
  await page.route("**/api/health", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ status: "ok", daemon: true }) });
  });
  await page.route("**/api/workspaces/**", async (route) => {
    const url = route.request().url();
    if (url.includes("/api/workspaces/active")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
      return;
    }
    if (url.includes("/events")) { await route.abort(); return; }
    if (url.includes(WS_ID + "/config/backend")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }) });
      return;
    }
    if (url.includes("/issues") && route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(LIFECYCLE_ISSUES) });
      return;
    }
    if (url.includes("/ready") && route.request().method() === "GET") {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(LIFECYCLE_ISSUES) });
      return;
    }
    if (url.includes("/stats")) {
      await route.fulfill({ status: 200, contentType: "application/json", body: ok({ total_issues: 3, open_issues: 1, in_progress_issues: 1, closed_issues: 0, blocked_issues: 0, deferred_issues: 0, ready_issues: 1, tombstone_issues: 0, pinned_issues: 0, epics_eligible_for_closure: 0, average_lead_time_hours: 24 }) });
      return;
    }
    if (url.includes("/blocked")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
    if (url.includes("/terminal/tabs")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
    if (url.includes("/terminal/state")) { await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ active_tab: "" }) }); return; }
    if (url.includes("/terminal/sessions/by-issue")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok({}) }); return; }
    await route.fulfill({ status: 200, contentType: "application/json", body: ok(WORKSPACE_DATA) });
  });
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({}) });
  });
}

test.describe("E2E Journey: Workspace lifecycle", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    await setupMocks(page);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Load workspace and verify kanban renders with issues", async () => {
    await page.goto(`/ws/${WS_ID}/kanban`, { waitUntil: "domcontentloaded" });
    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(readyColumn).toBeVisible({ timeout: 15000 });
    await expect(readyColumn.getByText("Setup CI pipeline")).toBeVisible();
    const inProgressColumn = page.locator('section[data-status="in_progress"]');
    await expect(inProgressColumn.getByText("Add logging middleware")).toBeVisible();
  });

  test("Workspace tree shows current workspace", async () => {
    // Verify workspace name appears in sidebar
    const sidebar = page.getByRole("complementary");
    await expect(sidebar.getByText("Workspaces")).toBeVisible();
    await expect(
      page.getByRole("button", { name: /Active workspace: Lifecycle Workspace/ }),
    ).toBeVisible();
  });

  test("Switch to table view preserves data", async () => {
    // Navigate to table view
    const navRail = page.getByRole("navigation", { name: "Primary" });
    // Click List button to switch to table (list view uses the ready endpoint)
    await page.goto(`/ws/${WS_ID}/table`, { waitUntil: "domcontentloaded" });
    await expect(page.getByTestId("issue-table")).toBeVisible({ timeout: 15000 });
    // Verify all 3 issues appear in the table
    const rows = page.locator('[data-testid="issue-table"] tbody tr');
    await expect(rows).toHaveCount(3);
  });

  test("Navigate back to kanban restores board", async () => {
    await page.goto(`/ws/${WS_ID}/kanban`, { waitUntil: "domcontentloaded" });
    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(readyColumn).toBeVisible({ timeout: 15000 });
    await expect(readyColumn.getByText("Setup CI pipeline")).toBeVisible({ timeout: 5000 });
  });
});

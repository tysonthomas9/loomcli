import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for NavRail component.
 *
 * Tests the icon-only vertical navigation rail for primary view switching
 * (Kanban, List, Terminal, Settings), session count badges, unread indicators,
 * tooltips, keyboard accessibility, and responsive layout (mobile bottom bar).
 */

// -- Mock data --

const mockWorkspaceData = {
  id: "ws-nav",
  name: "nav-test",
  path: "/workspaces/nav-test",
  repos: [
    {
      name: "repo-one",
      path: "/workspaces/nav-test/repo-one",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "ws-nav",
      name: "nav-test",
      path: "/workspaces/nav-test",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: ["ws-nav"],
  default_workspace: "ws-nav",
};

const mockIssue = {
  id: "nav-test-1",
  title: "Nav Rail Test Issue",
  status: "open",
  priority: 2,
  issue_type: "task",
  created_at: "2026-01-15T10:00:00Z",
  updated_at: "2026-01-15T10:00:00Z",
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Setup --

async function setupMocks(page: Page) {
  // Workspace API handler
  await page.route("**/api/workspaces/**", async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;

    if (pathname === "/api/workspaces/active") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
      return;
    }

    if (
      pathname.match(/^\/api\/workspaces\/[^/]+\/?$/) &&
      !pathname.includes("/api/workspaces/active")
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
      return;
    }

    if (pathname.match(/\/api\/workspaces\/[^/]+\/stats$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({
          total_issues: 1,
          open_issues: 1,
          in_progress_issues: 0,
          closed_issues: 0,
          blocked_issues: 0,
          deferred_issues: 0,
          ready_issues: 1,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
          average_lead_time_hours: 0,
        }),
      });
      return;
    }

    if (pathname.match(/\/api\/workspaces\/[^/]+\/blocked/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
      return;
    }

    if (pathname.match(/\/terminal\/tabs(\/|$)/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
      return;
    }

    if (pathname.includes("/terminal/sessions/by-issue")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({}),
      });
      return;
    }

    if (pathname.match(/\/terminal\/state$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      });
      return;
    }

    // /api/workspaces/{id}/ready
    if (pathname.match(/\/api\/workspaces\/[^/]+\/ready$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([mockIssue]),
      });
      return;
    }

    // /api/workspaces/{id}/issues
    if (pathname.match(/\/api\/workspaces\/[^/]+\/issues$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([mockIssue]),
      });
      return;
    }

    // /api/workspaces/{id}/issues/graph
    if (pathname.match(/\/api\/workspaces\/[^/]+\/issues\/graph$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([mockIssue]),
      });
      return;
    }

    if (pathname.includes("/config/backend")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({ backends: [] }),
      });
      return;
    }

    if (pathname.match(/\/api\/workspaces\/[^/]+\/events/)) {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
        body: 'event: connected\ndata: {"message":"connected"}\n\n',
      });
      return;
    }

    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "not found" }),
    });
  });

  // Health
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  // Global backend config
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        backend: "shell",
        source: "default",
        available: ["shell"],
        agents: [],
      }),
    });
  });

  // Health endpoint
  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  // Monitor endpoints
  await page.route("**/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });
}

/** Navigate to workspace and wait for data load. */
async function navigateAndWait(page: Page) {
  await page.goto("/ws/ws-nav/", { waitUntil: "domcontentloaded" });
  await page.waitForResponse(
    (res) =>
      res.url().includes("/api/workspaces/ws-nav") &&
      !res.url().includes("/events") &&
      res.status() === 200,
    { timeout: 10000 },
  );
  // Wait for React to render with workspace data
  await page.waitForTimeout(500);
}

/** Get the nav rail element. */
function getNavRail(page: Page) {
  return page.locator('nav[aria-label="Primary"]');
}

/** Get a nav button by its aria-label. */
function getNavButton(page: Page, label: string) {
  return getNavRail(page).getByRole("button", { name: label });
}

// -- Tests --

test.describe("NavRail rendering", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("renders all four navigation buttons", async ({ page }) => {
    const nav = getNavRail(page);
    await expect(nav).toBeVisible();

    const buttons = nav.getByRole("button");
    await expect(buttons).toHaveCount(4);

    await expect(getNavButton(page, "Kanban")).toBeVisible();
    await expect(getNavButton(page, "List")).toBeVisible();
    await expect(getNavButton(page, "Terminal")).toBeVisible();
    await expect(getNavButton(page, "Settings")).toBeVisible();
  });

  test("renders buttons in correct order", async ({ page }) => {
    const nav = getNavRail(page);
    const buttons = nav.getByRole("button");

    // Verify DOM order: Kanban, List, Terminal, Settings
    await expect(buttons.nth(0)).toHaveAttribute("aria-label", "Kanban");
    await expect(buttons.nth(1)).toHaveAttribute("aria-label", "List");
    await expect(buttons.nth(2)).toHaveAttribute("aria-label", "Terminal");
    await expect(buttons.nth(3)).toHaveAttribute("aria-label", "Settings");
  });

  test("Kanban button is active by default", async ({ page }) => {
    await expect(getNavButton(page, "Kanban")).toHaveAttribute(
      "data-active",
      "true",
    );
    // Other buttons should not be active
    await expect(getNavButton(page, "List")).not.toHaveAttribute("data-active");
    await expect(getNavButton(page, "Terminal")).not.toHaveAttribute(
      "data-active",
    );
    await expect(getNavButton(page, "Settings")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("renders navigation landmark", async ({ page }) => {
    const nav = getNavRail(page);
    await expect(nav).toBeVisible();
    await expect(nav).toHaveAttribute("aria-label", "Primary");
  });

  test("all buttons have aria-labels", async ({ page }) => {
    await expect(getNavButton(page, "Kanban")).toHaveAttribute(
      "aria-label",
      "Kanban",
    );
    await expect(getNavButton(page, "List")).toHaveAttribute(
      "aria-label",
      "List",
    );
    await expect(getNavButton(page, "Terminal")).toHaveAttribute(
      "aria-label",
      "Terminal",
    );
    await expect(getNavButton(page, "Settings")).toHaveAttribute(
      "aria-label",
      "Settings",
    );
  });

  test("all buttons have title attributes", async ({ page }) => {
    await expect(getNavButton(page, "Kanban")).toHaveAttribute(
      "title",
      "Kanban",
    );
    await expect(getNavButton(page, "List")).toHaveAttribute("title", "List");
    await expect(getNavButton(page, "Terminal")).toHaveAttribute(
      "title",
      "Terminal",
    );
    await expect(getNavButton(page, "Settings")).toHaveAttribute(
      "title",
      "Settings",
    );
  });
});

test.describe("NavRail view switching", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("clicking List switches to table view", async ({ page }) => {
    const listBtn = getNavButton(page, "List");
    await listBtn.click();

    await expect(listBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Kanban")).not.toHaveAttribute(
      "data-active",
    );

    const issueTable = page.getByTestId("issue-table");
    await expect(issueTable).toBeVisible({ timeout: 5000 });
  });

  test("clicking Terminal switches to terminal view", async ({ page }) => {
    const terminalBtn = getNavButton(page, "Terminal");
    await terminalBtn.click();

    await expect(terminalBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Kanban")).not.toHaveAttribute(
      "data-active",
    );

    // Terminal view uses display: contents when active (stays mounted)
    const terminalWrapper = page.locator('[style*="display: contents"]');
    await expect(terminalWrapper).toBeVisible({ timeout: 10000 });
  });

  test("clicking Settings switches to settings view", async ({ page }) => {
    const settingsBtn = getNavButton(page, "Settings");
    await settingsBtn.click();

    await expect(settingsBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Kanban")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("clicking Kanban returns to kanban view", async ({ page }) => {
    // Switch to List first
    const listBtn = getNavButton(page, "List");
    await listBtn.click();
    await expect(listBtn).toHaveAttribute("data-active", "true");

    // Switch back to Kanban
    const kanbanBtn = getNavButton(page, "Kanban");
    await kanbanBtn.click();

    await expect(kanbanBtn).toHaveAttribute("data-active", "true");
    await expect(listBtn).not.toHaveAttribute("data-active");

    // Kanban columns should be visible
    const kanbanColumn = page.locator('section[data-status="ready"]');
    await expect(kanbanColumn).toBeVisible({ timeout: 5000 });
  });

  test("clicking already-active button keeps view", async ({ page }) => {
    const kanbanBtn = getNavButton(page, "Kanban");
    await expect(kanbanBtn).toHaveAttribute("data-active", "true");

    // Click Kanban again
    await kanbanBtn.click();

    // Still active, no errors, no other button became active
    await expect(kanbanBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "List")).not.toHaveAttribute("data-active");
    await expect(getNavButton(page, "Terminal")).not.toHaveAttribute(
      "data-active",
    );
    await expect(getNavButton(page, "Settings")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("rapid view switching works correctly", async ({ page }) => {
    // Click List, then Terminal, then Settings rapidly
    await getNavButton(page, "List").click();
    await getNavButton(page, "Terminal").click();
    await getNavButton(page, "Settings").click();

    // Only Settings should be active at the end
    await expect(getNavButton(page, "Settings")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(getNavButton(page, "Kanban")).not.toHaveAttribute(
      "data-active",
    );
    await expect(getNavButton(page, "List")).not.toHaveAttribute("data-active");
    await expect(getNavButton(page, "Terminal")).not.toHaveAttribute(
      "data-active",
    );
  });
});

test.describe("NavRail session count badge", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("badge not shown when sessionCount is 0 or absent", async ({ page }) => {
    const terminalBtn = getNavButton(page, "Terminal");
    await expect(terminalBtn).toBeVisible();

    // No badge element should exist on the Terminal button
    const badge = terminalBtn.locator('[aria-label*="active sessions"]');
    await expect(badge).toHaveCount(0);
  });
});

test.describe("NavRail unread indicator", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("unread indicator not shown by default", async ({ page }) => {
    const nav = getNavRail(page);
    const unreadDots = nav.locator('[aria-label="has unread output"]');
    await expect(unreadDots).toHaveCount(0);
  });

  test("unread indicator not shown on active view", async ({ page }) => {
    // Switch to terminal (the only view that can have unread)
    await getNavButton(page, "Terminal").click();
    await expect(getNavButton(page, "Terminal")).toHaveAttribute(
      "data-active",
      "true",
    );

    // Even if there were terminal activity, the active view shouldn't show unread
    const terminalBtn = getNavButton(page, "Terminal");
    const unreadDot = terminalBtn.locator('[aria-label="has unread output"]');
    await expect(unreadDot).toHaveCount(0);
  });
});

test.describe("NavRail tooltips", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("tooltips appear on hover", async ({ page }) => {
    const labels = ["Kanban", "List", "Terminal", "Settings"];

    for (const label of labels) {
      const button = getNavButton(page, label);
      // Tooltip span is inside the button
      const tooltip = button.locator("span").filter({ hasText: label }).last();

      // Before hover, tooltip should exist but be invisible (opacity: 0)
      await expect(tooltip).toBeAttached();

      // Hover to show tooltip
      await button.hover();

      // Tooltip should become visible
      await expect(tooltip).toBeVisible();
    }
  });

  test("tooltips show correct labels", async ({ page }) => {
    const expectedLabels = ["Kanban", "List", "Terminal", "Settings"];

    for (const label of expectedLabels) {
      const button = getNavButton(page, label);
      await button.hover();

      // Verify tooltip text matches label
      const tooltip = button.locator("span").filter({ hasText: label }).last();
      await expect(tooltip).toHaveText(label);
    }
  });
});

test.describe("NavRail responsive layout", () => {
  test("renders as horizontal bottom bar on mobile", async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 375, height: 667 });
    await navigateAndWait(page);

    const nav = getNavRail(page);
    await expect(nav).toBeVisible();

    // On mobile, the nav should be positioned as a bottom bar
    const boundingBox = await nav.boundingBox();
    expect(boundingBox).not.toBeNull();
    // Bottom bar should be near the bottom of the viewport
    expect(boundingBox!.y + boundingBox!.height).toBeGreaterThan(600);
  });

  test("all four buttons visible on mobile", async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 375, height: 667 });
    await navigateAndWait(page);

    await expect(getNavButton(page, "Kanban")).toBeVisible();
    await expect(getNavButton(page, "List")).toBeVisible();
    await expect(getNavButton(page, "Terminal")).toBeVisible();
    await expect(getNavButton(page, "Settings")).toBeVisible();
  });

  test("tooltips hidden on mobile", async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 375, height: 667 });
    await navigateAndWait(page);

    const kanbanBtn = getNavButton(page, "Kanban");
    await kanbanBtn.hover();

    // CSS sets .tooltip { display: none } on mobile - tooltip span should not be visible
    const tooltip = kanbanBtn
      .locator("span")
      .filter({ hasText: "Kanban" })
      .last();
    await expect(tooltip).not.toBeVisible();
  });
});

test.describe("NavRail accessibility", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("navigation landmark is present", async ({ page }) => {
    const nav = page.locator('nav[aria-label="Primary"]');
    await expect(nav).toBeVisible();
  });

  test("buttons are keyboard focusable", async ({ page }) => {
    const kanbanBtn = getNavButton(page, "Kanban");

    // Focus the first button
    await kanbanBtn.focus();
    await expect(kanbanBtn).toBeFocused();

    // Tab to next button
    await page.keyboard.press("Tab");
    await expect(getNavButton(page, "List")).toBeFocused();

    // Continue tabbing
    await page.keyboard.press("Tab");
    await expect(getNavButton(page, "Terminal")).toBeFocused();

    await page.keyboard.press("Tab");
    await expect(getNavButton(page, "Settings")).toBeFocused();
  });

  test("active state communicated via data-active", async ({ page }) => {
    // Default: Kanban is active
    await expect(getNavButton(page, "Kanban")).toHaveAttribute(
      "data-active",
      "true",
    );

    // Switch to List
    await getNavButton(page, "List").click();
    await expect(getNavButton(page, "List")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(getNavButton(page, "Kanban")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("SVG icons are hidden from screen readers", async ({ page }) => {
    const nav = getNavRail(page);
    const svgs = nav.locator("svg");

    const count = await svgs.count();
    expect(count).toBeGreaterThan(0);

    for (let i = 0; i < count; i++) {
      await expect(svgs.nth(i)).toHaveAttribute("aria-hidden", "true");
    }
  });
});

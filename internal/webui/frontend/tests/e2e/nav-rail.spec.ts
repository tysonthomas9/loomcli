import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for NavRail component.
 *
 * Tests the icon-only vertical navigation rail for primary view switching
 * (Workspaces, Monitor, Settings), session count badges, unread indicators,
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
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/backends", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([
        {
          name: "shell",
          available: true,
          display_name: "Shell",
          configured: true,
        },
      ]),
    });
  });

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
        body: ok({
          backend: "shell",
          source: "workspace",
          available: ["shell"],
          agents: [],
        }),
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
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });
}

/** Navigate to workspace and wait for data load. */
async function navigateAndWait(page: Page) {
  const responsePromise = page.waitForResponse(
    (res) =>
      res.url().includes("/api/workspaces/ws-nav") &&
      !res.url().includes("/events") &&
      res.status() === 200,
    { timeout: 10000 },
  );
  await page.goto("/ws/ws-nav/kanban", { waitUntil: "domcontentloaded" });
  await responsePromise;
  await expect(getNavRail(page)).toBeVisible();
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

  test("renders all primary navigation buttons", async ({ page }) => {
    const nav = getNavRail(page);
    await expect(nav).toBeVisible();

    const buttons = nav.getByRole("button");
    await expect(buttons).toHaveCount(3);

    await expect(getNavButton(page, "Workspaces")).toBeVisible();
    await expect(getNavButton(page, "Monitor")).toBeVisible();
    await expect(getNavButton(page, "Settings")).toBeVisible();
  });

  test("renders buttons in correct order", async ({ page }) => {
    const nav = getNavRail(page);
    const buttons = nav.getByRole("button");

    // Verify DOM order: Workspaces, Monitor, Settings
    await expect(buttons.nth(0)).toHaveAttribute("aria-label", "Workspaces");
    await expect(buttons.nth(1)).toHaveAttribute("aria-label", "Monitor");
    await expect(buttons.nth(2)).toHaveAttribute("aria-label", "Settings");
  });

  test("Workspaces button is active by default", async ({ page }) => {
    await expect(getNavButton(page, "Workspaces")).toHaveAttribute(
      "data-active",
      "true",
    );
    // Other buttons should not be active
    await expect(getNavButton(page, "Monitor")).not.toHaveAttribute(
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
    await expect(getNavButton(page, "Workspaces")).toHaveAttribute(
      "aria-label",
      "Workspaces",
    );
    await expect(getNavButton(page, "Monitor")).toHaveAttribute(
      "aria-label",
      "Monitor",
    );
    await expect(getNavButton(page, "Settings")).toHaveAttribute(
      "aria-label",
      "Settings",
    );
  });

  test("all buttons have title attributes", async ({ page }) => {
    await expect(getNavButton(page, "Workspaces")).toHaveAttribute(
      "title",
      "Workspaces",
    );
    await expect(getNavButton(page, "Monitor")).toHaveAttribute(
      "title",
      "Monitor",
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

  test("clicking Monitor switches to terminal-backed monitor view", async ({
    page,
  }) => {
    const monitorBtn = getNavButton(page, "Monitor");
    await monitorBtn.click();

    await expect(monitorBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Workspaces")).not.toHaveAttribute(
      "data-active",
    );

    await expect(page).toHaveURL(/\/ws\/ws-nav\/terminal/);

    // Terminal view uses display: contents when active (stays mounted)
    const terminalWrapper = page.locator('[style*="display: contents"]');
    await expect(terminalWrapper).toBeVisible({ timeout: 10000 });
  });

  test("clicking Settings switches to settings view", async ({ page }) => {
    const settingsBtn = getNavButton(page, "Settings");
    await settingsBtn.click();

    await expect(settingsBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Workspaces")).not.toHaveAttribute(
      "data-active",
    );
    await expect(page).toHaveURL(/\/ws\/ws-nav\/settings/);
  });

  test("clicking Workspaces returns to kanban view", async ({ page }) => {
    // Switch to Settings first
    const settingsBtn = getNavButton(page, "Settings");
    await settingsBtn.click();
    await expect(settingsBtn).toHaveAttribute("data-active", "true");

    // Switch back to Workspaces
    const workspacesBtn = getNavButton(page, "Workspaces");
    await workspacesBtn.click();

    await expect(workspacesBtn).toHaveAttribute("data-active", "true");
    await expect(settingsBtn).not.toHaveAttribute("data-active");
    await expect(page).toHaveURL(/\/ws\/ws-nav\/kanban/);

    // Kanban columns should be visible
    const kanbanColumn = page.locator('section[data-status="ready"]');
    await expect(kanbanColumn).toBeVisible({ timeout: 5000 });
  });

  test("clicking already-active button keeps view", async ({ page }) => {
    const workspacesBtn = getNavButton(page, "Workspaces");
    await expect(workspacesBtn).toHaveAttribute("data-active", "true");

    // Click Workspaces again
    await workspacesBtn.click();

    // Still active, no errors, no other button became active
    await expect(workspacesBtn).toHaveAttribute("data-active", "true");
    await expect(getNavButton(page, "Monitor")).not.toHaveAttribute(
      "data-active",
    );
    await expect(getNavButton(page, "Settings")).not.toHaveAttribute(
      "data-active",
    );
  });

  test("rapid view switching works correctly", async ({ page }) => {
    // Click Monitor, Workspaces, then Settings rapidly
    await getNavButton(page, "Monitor").click();
    await getNavButton(page, "Workspaces").click();
    await getNavButton(page, "Settings").click();

    // Only Settings should be active at the end
    await expect(getNavButton(page, "Settings")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(getNavButton(page, "Workspaces")).not.toHaveAttribute(
      "data-active",
    );
    await expect(getNavButton(page, "Monitor")).not.toHaveAttribute(
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
    const monitorBtn = getNavButton(page, "Monitor");
    await expect(monitorBtn).toBeVisible();

    // No badge element should exist on the Monitor button
    const badge = monitorBtn.locator('[aria-label*="active sessions"]');
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
    // Switch to monitor/terminal (the only primary view that can have unread)
    await getNavButton(page, "Monitor").click();
    await expect(getNavButton(page, "Monitor")).toHaveAttribute(
      "data-active",
      "true",
    );

    // Even if there were terminal activity, the active view shouldn't show unread
    const monitorBtn = getNavButton(page, "Monitor");
    const unreadDot = monitorBtn.locator('[aria-label="has unread output"]');
    await expect(unreadDot).toHaveCount(0);
  });
});

test.describe("NavRail tooltips", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page);
  });

  test("tooltips appear on hover", async ({ page }) => {
    const labels = ["Workspaces", "Monitor", "Settings"];

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
    const expectedLabels = ["Workspaces", "Monitor", "Settings"];

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

  test("all primary buttons visible on mobile", async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 375, height: 667 });
    await navigateAndWait(page);

    await expect(getNavButton(page, "Workspaces")).toBeVisible();
    await expect(getNavButton(page, "Monitor")).toBeVisible();
    await expect(getNavButton(page, "Settings")).toBeVisible();
  });

  test("tooltips hidden on mobile", async ({ page }) => {
    await setupMocks(page);
    await page.setViewportSize({ width: 375, height: 667 });
    await navigateAndWait(page);

    const workspacesBtn = getNavButton(page, "Workspaces");
    await workspacesBtn.hover();

    // CSS sets .tooltip { display: none } on mobile - tooltip span should not be visible
    const tooltip = workspacesBtn
      .locator("span")
      .filter({ hasText: "Workspaces" })
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
    const workspacesBtn = getNavButton(page, "Workspaces");

    // Focus the first button
    await workspacesBtn.focus();
    await expect(workspacesBtn).toBeFocused();

    // Tab to next button
    await page.keyboard.press("Tab");
    await expect(getNavButton(page, "Monitor")).toBeFocused();

    // Continue tabbing
    await page.keyboard.press("Tab");
    await expect(getNavButton(page, "Settings")).toBeFocused();
  });

  test("active state communicated via data-active", async ({ page }) => {
    // Default: Workspaces is active
    await expect(getNavButton(page, "Workspaces")).toHaveAttribute(
      "data-active",
      "true",
    );

    // Switch to Monitor
    await getNavButton(page, "Monitor").click();
    await expect(getNavButton(page, "Monitor")).toHaveAttribute(
      "data-active",
      "true",
    );
    await expect(getNavButton(page, "Workspaces")).not.toHaveAttribute(
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

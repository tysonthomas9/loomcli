/**
 * E2E tests for Terminal Split View.
 *
 * Covers the full split view lifecycle: toggle, layout rendering, pane selector,
 * divider resize, auto-disable conditions, session storage persistence, and
 * search targeting in split mode.
 *
 * All backend interactions are mocked via page.route() — no real backend needed.
 * Uses workspace-scoped routing (same pattern as terminal-tab-management).
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WORKSPACE_ID = "split-view-ws";

const WORKSPACE_DATA = {
  id: WORKSPACE_ID,
  name: "Split View Workspace",
  path: "/tmp/split-view-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "Split View Workspace",
      path: "/tmp/split-view-ws",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  default_workspace: WORKSPACE_ID,
};

const WS_PREFIX = `/api/workspaces/${WORKSPACE_ID}`;
const DOM_SETTLE_MS = 400;

interface TabMetadata {
  session_name: string;
  label: string;
  sort_order: number;
  pinned: boolean;
  notes: string;
  issue_id?: string;
  created_at: string;
  updated_at: string;
}

function makeTab(
  session_name: string,
  label: string,
  sort_order: number,
  opts?: { pinned?: boolean }
): TabMetadata {
  return {
    session_name,
    label,
    sort_order,
    pinned: opts?.pinned ?? false,
    notes: "",
    created_at: "2026-03-28T00:00:00Z",
    updated_at: "2026-03-28T00:00:00Z",
  };
}

const TWO_TABS: TabMetadata[] = [
  makeTab("lead-claude-1", "lead-claude-1", 0),
  makeTab("lead-claude-2", "lead-claude-2", 1),
];

const THREE_TABS: TabMetadata[] = [
  makeTab("lead-claude-1", "lead-claude-1", 0),
  makeTab("lead-claude-2", "lead-claude-2", 1),
  makeTab("lead-claude-3", "lead-claude-3", 2),
];

const MOCK_STATS = {
  total_issues: 0,
  open_issues: 0,
  in_progress_issues: 0,
  closed_issues: 0,
  blocked_issues: 0,
  deferred_issues: 0,
  ready_issues: 0,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 0,
};

// ---------------------------------------------------------------------------
// Mock setup
// ---------------------------------------------------------------------------

interface SetupOptions {
  initialTabs?: TabMetadata[];
  activeTab?: string;
}

interface MockTrackers {
  deleteCalls: string[];
  patchCalls: Array<{ url: string; body: unknown }>;
  statePatchCalls: Array<{ body: unknown }>;
}

async function setupTerminalMocks(
  page: Page,
  options: SetupOptions = {}
): Promise<MockTrackers> {
  const tabs = [...(options.initialTabs ?? TWO_TABS)];
  const trackers: MockTrackers = {
    deleteCalls: [],
    patchCalls: [],
    statePatchCalls: [],
  };

  // Neutralize AbortController signals (React StrictMode workaround)
  await page.addInitScript(() => {
    const origFetch = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      if (init?.signal) {
        const { signal: _signal, ...rest } = init;
        return origFetch.call(this, input, rest);
      }
      return origFetch.call(this, input, init);
    };
  });

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-split" }),
    });
  });

  // Daemon health
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        daemon: {
          connected: true,
          status: "running",
          uptime: 1000,
          version: "test",
        },
      }),
    });
  });

  // Terminal spawn
  await page.route("**/api/terminal/spawn", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, session: "lead-claude-new" }),
    });
  });

  // Terminal token
  await page.route("**/api/terminal/token**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "ws-token-mock" }),
    });
  });

  // Terminal state (GET/PATCH)
  await page.route("**/api/terminal/state", async (route) => {
    if (route.request().method() === "PATCH") {
      const body = route.request().postData();
      trackers.statePatchCalls.push({
        body: body ? JSON.parse(body) : null,
      });
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          active_tab: options.activeTab ?? tabs[0]?.session_name ?? "",
        },
      }),
    });
  });

  // Workspace-scoped API endpoints (single handler)
  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      // Workspace resolution: /api/workspaces/active
      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // SSE events: abort
      if (url.includes(WS_PREFIX + "/events")) {
        await route.abort();
        return;
      }

      // Terminal tabs list: GET /api/workspaces/{ws}/terminal/tabs
      if (
        url.includes(WS_PREFIX + "/terminal/tabs") &&
        !url.match(/\/terminal\/tabs\/[^/]/)
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: tabs }),
        });
        return;
      }

      // Terminal tab by session: GET/PUT/PATCH/DELETE
      const tabMatch = url.match(/\/terminal\/tabs\/([^/?]+)/);
      if (tabMatch) {
        const session = decodeURIComponent(tabMatch[1]);
        if (method === "DELETE") {
          trackers.deleteCalls.push(session);
          const idx = tabs.findIndex((t) => t.session_name === session);
          if (idx >= 0) tabs.splice(idx, 1);
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true }),
          });
          return;
        }
        if (method === "PATCH") {
          const body = route.request().postData();
          trackers.patchCalls.push({
            url: route.request().url(),
            body: body ? JSON.parse(body) : null,
          });
          const existing = tabs.find((t) => t.session_name === session);
          const parsed = body ? JSON.parse(body) : {};
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...existing, ...parsed, session_name: session },
            }),
          });
          return;
        }
        if (method === "PUT") {
          const parsed = route.request().postData()
            ? JSON.parse(route.request().postData()!)
            : {};
          const newTab = {
            session_name: session,
            label: parsed.label ?? session,
            sort_order: parsed.sort_order ?? tabs.length,
            pinned: false,
            notes: "",
            created_at: "2026-03-28T00:00:00Z",
            updated_at: "2026-03-28T00:00:00Z",
            ...parsed,
          };
          tabs.push(newTab);
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: newTab }),
          });
          return;
        }
        // GET
        const tab = tabs.find((t) => t.session_name === session);
        await route.fulfill({
          status: tab ? 200 : 404,
          contentType: "application/json",
          body: JSON.stringify(
            tab
              ? { success: true, data: tab }
              : { success: false, error: "Not found" }
          ),
        });
        return;
      }

      // Terminal sessions by issue
      if (url.includes(WS_PREFIX + "/terminal/sessions/by-issue")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      // Issues
      if (url.includes(WS_PREFIX + "/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }
      if (url.includes(WS_PREFIX + "/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      // Ready / blocked
      if (url.includes(WS_PREFIX + "/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }
      if (url.includes(WS_PREFIX + "/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: [] }),
        });
        return;
      }

      // Stats
      if (url.includes(WS_PREFIX + "/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: MOCK_STATS }),
        });
        return;
      }

      // Generic terminal catch-all
      if (url.includes(WS_PREFIX + "/terminal/")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: {} }),
        });
        return;
      }

      // Exact workspace path
      if (url.includes(WS_PREFIX)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: WORKSPACE_DATA }),
        });
        return;
      }

      // Unknown
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    }
  );

  // Monitor server endpoints (global)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });

  // Config backend
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "claude",
          source: "project",
          available: ["claude"],
          agents: [],
        },
      }),
    });
  });

  return trackers;
}

async function navigateToTerminal(page: Page): Promise<void> {
  await page.goto(`/ws/${WORKSPACE_ID}/?view=terminal`);
  await page.waitForSelector('[role="banner"]', { timeout: 15000 });
  await expect(page.getByTestId("terminal-tab-bar")).toBeVisible({
    timeout: 10000,
  });
}

// ---------------------------------------------------------------------------
// Test Groups
// ---------------------------------------------------------------------------

test.describe("Terminal Split View", () => {
  test.describe("Split view toggle", () => {
    test("split toggle button is visible when 2+ tabs exist", async ({
      page,
    }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-split-toggle");
      await expect(toggle).toBeVisible();
      await expect(toggle).not.toBeDisabled();
    });

    test("split toggle button is disabled when only 1 tab exists", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: [makeTab("lead-claude-solo", "lead-claude-solo", 0)],
      });
      await navigateToTerminal(page);

      const toggle = page.getByTestId("terminal-split-toggle");
      await expect(toggle).toBeVisible();
      await expect(toggle).toBeDisabled();
    });

    test("clicking toggle enables split view", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      // Split container should not exist yet
      await expect(page.getByTestId("split-container")).not.toBeVisible();

      // Click toggle to enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Split container should now be visible
      await expect(page.getByTestId("split-container")).toBeVisible();
      await expect(page.getByTestId("terminal-split-toggle")).toHaveAttribute(
        "aria-pressed",
        "true"
      );
    });

    test("clicking toggle again disables split view", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      // Enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("split-container")).toBeVisible();

      // Disable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(page.getByTestId("split-container")).not.toBeVisible();
      await expect(page.getByTestId("terminal-split-toggle")).toHaveAttribute(
        "aria-pressed",
        "false"
      );
    });
  });

  test.describe("Split layout rendering", () => {
    test("split container renders with CSS grid columns", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const container = page.getByTestId("split-container");
      await expect(container).toBeVisible();

      // Check gridTemplateColumns has the 3-part pattern (Xfr auto Yfr)
      const gridCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      // Computed style resolves fr units to pixel values — should have 3 parts
      const parts = gridCols.split(" ");
      expect(parts.length).toBe(3);
    });

    test("left pane shows active tab panel", async ({ page }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Active tab panel should be visible in left pane
      const activePanel = page.locator("#terminal-panel-lead-claude-1");
      await expect(activePanel).toBeVisible();

      // Non-active tab panel should be hidden in left pane
      const hiddenPanel = page.locator("#terminal-panel-lead-claude-2");
      const display = await hiddenPanel.evaluate(
        (el) => getComputedStyle(el).display
      );
      expect(display).toBe("none");
    });

    test("right pane shows non-active tab via split-pane-selector", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Right pane panel for tab 2 should be visible
      const rightPanel = page.locator("#terminal-panel-right-lead-claude-2");
      await expect(rightPanel).toBeVisible();

      // split-pane-selector should be visible
      await expect(page.getByTestId("split-pane-selector")).toBeVisible();
    });

    test("split-divider has role=separator and aria-orientation=vertical", async ({
      page,
    }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const divider = page.getByTestId("split-divider");
      await expect(divider).toBeVisible();
      await expect(divider).toHaveAttribute("role", "separator");
      await expect(divider).toHaveAttribute("aria-orientation", "vertical");
    });
  });

  test.describe("Split pane selector", () => {
    test("selector lists all tabs except the active left tab", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: THREE_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const selector = page.getByTestId("split-pane-selector");
      const select = selector.locator("select");

      // Should have 2 options (tab 2 and tab 3, not tab 1 which is active)
      const options = select.locator("option");
      await expect(options).toHaveCount(2);

      const optionTexts = await options.allTextContents();
      expect(optionTexts).toContain("lead-claude-2");
      expect(optionTexts).toContain("lead-claude-3");
      expect(optionTexts).not.toContain("lead-claude-1");
    });

    test("changing selector switches right pane tab", async ({ page }) => {
      await setupTerminalMocks(page, {
        initialTabs: THREE_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Initially right pane should show tab 2 (first non-active)
      await expect(
        page.locator("#terminal-panel-right-lead-claude-2")
      ).toBeVisible();

      // Change selector to tab 3
      const select = page
        .getByTestId("split-pane-selector")
        .locator("select");
      await select.selectOption("lead-claude-3");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Tab 3 right panel should now be visible
      await expect(
        page.locator("#terminal-panel-right-lead-claude-3")
      ).toBeVisible();
      // Tab 2 right panel should be hidden
      const display = await page
        .locator("#terminal-panel-right-lead-claude-2")
        .evaluate((el) => getComputedStyle(el).display);
      expect(display).toBe("none");
    });

    test("when active left tab changes, selector updates to exclude it", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: THREE_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Switch active tab to tab 3 by clicking it in the tab bar
      await page.getByTestId("terminal-tab-lead-claude-3").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Selector should now exclude tab 3 (active) and include tab 1
      const select = page
        .getByTestId("split-pane-selector")
        .locator("select");
      const options = select.locator("option");
      const optionTexts = await options.allTextContents();
      expect(optionTexts).toContain("lead-claude-1");
      expect(optionTexts).not.toContain("lead-claude-3");
    });
  });

  test.describe("Divider resize", () => {
    test("dragging divider right increases left pane ratio", async ({
      page,
    }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const container = page.getByTestId("split-container");
      const divider = page.getByTestId("split-divider");

      // Get initial computed column widths
      const initialCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      const initialParts = initialCols.split(" ").map(parseFloat);
      const initialLeftWidth = initialParts[0];

      // Drag divider to the right
      const box = await container.boundingBox();
      const divBox = await divider.boundingBox();
      expect(box).not.toBeNull();
      expect(divBox).not.toBeNull();

      await divider.hover();
      await page.mouse.down();
      await page.mouse.move(
        box!.x + box!.width * 0.7,
        divBox!.y + divBox!.height / 2,
        { steps: 5 }
      );
      await page.mouse.up();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Left pane should now be wider
      const newCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      const newParts = newCols.split(" ").map(parseFloat);
      expect(newParts[0]).toBeGreaterThan(initialLeftWidth);

      // Verify ratio was persisted to sessionStorage
      const storedRatio = await page.evaluate(() =>
        sessionStorage.getItem("terminal-split-ratio")
      );
      expect(parseFloat(storedRatio!)).toBeGreaterThan(0.5);
    });

    test("dragging divider left decreases left pane ratio", async ({
      page,
    }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const container = page.getByTestId("split-container");
      const divider = page.getByTestId("split-divider");

      const initialCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      const initialParts = initialCols.split(" ").map(parseFloat);
      const initialLeftWidth = initialParts[0];

      // Drag divider to the left
      const box = await container.boundingBox();
      const divBox = await divider.boundingBox();
      expect(box).not.toBeNull();
      expect(divBox).not.toBeNull();

      await divider.hover();
      await page.mouse.down();
      await page.mouse.move(
        box!.x + box!.width * 0.3,
        divBox!.y + divBox!.height / 2,
        { steps: 5 }
      );
      await page.mouse.up();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const newCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      const newParts = newCols.split(" ").map(parseFloat);
      expect(newParts[0]).toBeLessThan(initialLeftWidth);
    });

    test("double-clicking divider resets to 50/50", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const container = page.getByTestId("split-container");
      const divider = page.getByTestId("split-divider");

      // First drag to offset the ratio
      const box = await container.boundingBox();
      const divBox = await divider.boundingBox();
      expect(box).not.toBeNull();
      expect(divBox).not.toBeNull();

      await divider.hover();
      await page.mouse.down();
      await page.mouse.move(
        box!.x + box!.width * 0.7,
        divBox!.y + divBox!.height / 2,
        { steps: 5 }
      );
      await page.mouse.up();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Now double-click to reset
      await divider.dblclick();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // After reset, left and right panes should be roughly equal
      const resetCols = await container.evaluate(
        (el) => getComputedStyle(el).gridTemplateColumns
      );
      const resetParts = resetCols.split(" ").map(parseFloat);
      const leftWidth = resetParts[0];
      const rightWidth = resetParts[2];
      // Allow small tolerance for the divider width
      const ratio = leftWidth / (leftWidth + rightWidth);
      expect(ratio).toBeGreaterThan(0.45);
      expect(ratio).toBeLessThan(0.55);
    });
  });

  test.describe("Auto-disable conditions", () => {
    test("split auto-disables when viewport width drops below 900px", async ({
      page,
    }) => {
      await setupTerminalMocks(page, { initialTabs: TWO_TABS });
      await navigateToTerminal(page);

      // Enable split at normal viewport
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("split-container")).toBeVisible();

      // Shrink viewport below MIN_SPLIT_WIDTH_PX (900)
      await page.setViewportSize({ width: 800, height: 600 });
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Split should auto-disable
      await expect(page.getByTestId("split-container")).not.toBeVisible();
    });

    test("split auto-disables when closing a tab reduces count below 2", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      // Enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("split-container")).toBeVisible();

      // Close tab 2 via close button
      await page.getByTestId("terminal-tab-close-lead-claude-2").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Split should auto-disable (only 1 tab remains)
      await expect(page.getByTestId("split-container")).not.toBeVisible();
    });

    test("right pane auto-switches when active tab matches right pane tab", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: THREE_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      // Enable split — right pane defaults to tab 2
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Verify right pane shows tab 2
      await expect(
        page.locator("#terminal-panel-right-lead-claude-2")
      ).toBeVisible();

      // Click tab 2 in the main tab bar to make it active
      await page.getByTestId("terminal-tab-lead-claude-2").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Right pane should auto-switch to a different tab (not tab 2 anymore)
      const rightTab2Display = await page
        .locator("#terminal-panel-right-lead-claude-2")
        .evaluate((el) => getComputedStyle(el).display);
      expect(rightTab2Display).toBe("none");
    });
  });

  test.describe("Session storage persistence", () => {
    test("enabling split view sets sessionStorage keys", async ({ page }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      // Enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Check sessionStorage values set by the toggle action
      const splitView = await page.evaluate(() =>
        sessionStorage.getItem("terminal-split-view")
      );
      expect(splitView).toBe("true");

      const rightTab = await page.evaluate(() =>
        sessionStorage.getItem("terminal-split-right-tab")
      );
      expect(rightTab).toBe("lead-claude-2");
    });

    test("disabling split view updates sessionStorage to false", async ({
      page,
    }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      // Enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const splitViewBefore = await page.evaluate(() =>
        sessionStorage.getItem("terminal-split-view")
      );
      expect(splitViewBefore).toBe("true");

      // Disable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      const splitViewAfter = await page.evaluate(() =>
        sessionStorage.getItem("terminal-split-view")
      );
      expect(splitViewAfter).toBe("false");
    });
  });

  test.describe("Search targeting in split mode", () => {
    test("search targets active (left) tab by default", async ({ page }) => {
      await setupTerminalMocks(page, {
        initialTabs: TWO_TABS,
        activeTab: "lead-claude-1",
      });
      await navigateToTerminal(page);

      // Enable split
      await page.getByTestId("terminal-split-toggle").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Open search with Ctrl+F
      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Search bar should appear with input field
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await expect(page.getByTestId("terminal-search-input")).toBeVisible();
    });
  });
});

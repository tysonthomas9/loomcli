/**
 * E2E tests for Terminal Keyboard Shortcuts.
 *
 * Covers tab cycling (Ctrl+Tab, Ctrl+Shift+Tab, Alt+Arrow),
 * tab switching by index (Ctrl+1-9), tab creation (Ctrl+T),
 * tab closing (Ctrl+W), search toggle (Ctrl+F), help popover,
 * and Escape key layered behavior.
 *
 * All backend interactions are mocked via page.route() — no real backend needed.
 * Uses workspace-scoped routing (same pattern as terminal-tab-management).
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WORKSPACE_ID = "kbd-shortcuts-ws";

const WORKSPACE_DATA = {
  id: WORKSPACE_ID,
  name: "Keyboard Shortcuts Workspace",
  path: "/tmp/kbd-shortcuts-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "Keyboard Shortcuts Workspace",
      path: "/tmp/kbd-shortcuts-ws",
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
  sort_order: number
): TabMetadata {
  return {
    session_name,
    label,
    sort_order,
    pinned: false,
    notes: "",
    created_at: "2026-03-28T00:00:00Z",
    updated_at: "2026-03-28T00:00:00Z",
  };
}

const THREE_TABS: TabMetadata[] = [
  makeTab("lead-claude-1", "lead-claude-1", 0),
  makeTab("lead-claude-2", "lead-claude-2", 1),
  makeTab("lead-claude-3", "lead-claude-3", 2),
];

const SINGLE_TAB: TabMetadata[] = [
  makeTab("lead-claude-1", "lead-claude-1", 0),
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

async function setupTerminalMocks(
  page: Page,
  options: SetupOptions = {}
): Promise<void> {
  const tabs = [...(options.initialTabs ?? THREE_TABS)];

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
      body: JSON.stringify({ token: "test-token-kbd" }),
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
      body: JSON.stringify({
        success: true,
        session: `lead-claude-${tabs.length + 1}`,
      }),
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

      // Terminal tabs list
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

      // Terminal tab by session
      const tabMatch = url.match(/\/terminal\/tabs\/([^/?]+)/);
      if (tabMatch) {
        const session = decodeURIComponent(tabMatch[1]);
        if (method === "DELETE") {
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
          const existing = tabs.find((t) => t.session_name === session);
          const parsed = body ? JSON.parse(body) : {};
          const updated = { ...existing, ...parsed, session_name: session };
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ success: true, data: updated }),
          });
          return;
        }
        if (method === "PUT") {
          const body = route.request().postData();
          const parsed = body ? JSON.parse(body) : {};
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

      // Issues / graph
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
}

// ---------------------------------------------------------------------------
// Keyboard dispatch helper
// ---------------------------------------------------------------------------

/**
 * Dispatch a keyboard event directly to the document, bypassing browser-level
 * shortcut interception (e.g. Alt+Left triggers "back" in Chromium).
 */
async function dispatchKey(
  page: Page,
  key: string,
  opts: { ctrlKey?: boolean; shiftKey?: boolean; altKey?: boolean; metaKey?: boolean } = {}
): Promise<void> {
  await page.evaluate(
    ({ key, opts }) => {
      const code =
        key.startsWith("Arrow") || key === "Escape" || key === "Tab"
          ? key
          : `Key${key.charAt(0).toUpperCase()}${key.slice(1)}`;
      document.dispatchEvent(
        new KeyboardEvent("keydown", {
          key,
          code,
          bubbles: true,
          cancelable: true,
          ...opts,
        })
      );
    },
    { key, opts }
  );
}

// ---------------------------------------------------------------------------
// Navigation helper
// ---------------------------------------------------------------------------

async function navigateToTerminal(page: Page): Promise<void> {
  // Dismiss welcome banner so Escape handler fires properly
  await page.addInitScript(() => {
    localStorage.setItem("terminal-onboarding-dismissed", "1");
  });
  await page.goto(`/ws/${WORKSPACE_ID}/?view=terminal`);
  await page.waitForSelector('[role="banner"]', { timeout: 15000 });
  await expect(page.getByTestId("terminal-tab-bar")).toBeVisible({
    timeout: 10000,
  });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Terminal keyboard shortcuts", () => {
  // -----------------------------------------------------------------------
  // Tab cycling
  // -----------------------------------------------------------------------
  test.describe("Tab cycling", () => {
    test("Ctrl+Tab cycles to next tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // First tab is active by default
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+Tab");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "false");
    });

    test("Ctrl+Shift+Tab cycles to previous tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Click second tab to make it active
      await page.getByTestId("terminal-tab-lead-claude-2").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+Shift+Tab");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+Tab wraps from last to first tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Click third tab to make it active
      await page.getByTestId("terminal-tab-lead-claude-3").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-tab-lead-claude-3")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+Tab");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+Shift+Tab wraps from first to last tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // First tab is active by default
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+Shift+Tab");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-3")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Alt+ArrowRight cycles to next tab (Firefox fallback)", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Use dispatchKey to bypass Chromium's Alt+Right = browser-forward behavior
      await dispatchKey(page, "ArrowRight", { altKey: true });
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Alt+ArrowLeft cycles to previous tab (Firefox fallback)", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Click second tab to make it active
      await page.getByTestId("terminal-tab-lead-claude-2").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");

      // Use dispatchKey to bypass Chromium's Alt+Left = browser-back behavior
      await dispatchKey(page, "ArrowLeft", { altKey: true });
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+Tab is no-op with single tab", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: SINGLE_TAB });
      await navigateToTerminal(page);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+Tab");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Single tab remains active, no errors
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });
  });

  // -----------------------------------------------------------------------
  // Tab switching by index
  // -----------------------------------------------------------------------
  test.describe("Tab switching by index", () => {
    test("Ctrl+1 switches to first tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Click second tab to make it active
      await page.getByTestId("terminal-tab-lead-claude-2").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");

      await page.keyboard.press("Control+1");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+2 switches to second tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+2");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-2")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+3 switches to third tab", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+3");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-tab-lead-claude-3")
      ).toHaveAttribute("aria-selected", "true");
    });

    test("Ctrl+4 does nothing when only 3 tabs exist", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+4");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Active tab unchanged
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toHaveAttribute("aria-selected", "true");
    });
  });

  // -----------------------------------------------------------------------
  // Tab creation and closing
  // -----------------------------------------------------------------------
  test.describe("Tab creation and closing", () => {
    test("Ctrl+T opens backend picker prompt", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+t");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("backend-picker-prompt-overlay")
      ).toBeVisible();
    });

    test("Ctrl+W closes active tab when multiple tabs exist", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      const initialTabCount = await page
        .getByTestId("terminal-tab-bar")
        .locator('[role="tab"]')
        .count();
      expect(initialTabCount).toBe(3);

      await page.keyboard.press("Control+w");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // First tab should be removed, second becomes active
      await expect
        .poll(
          () =>
            page
              .getByTestId("terminal-tab-bar")
              .locator('[role="tab"]')
              .count(),
          { timeout: 5000 }
        )
        .toBe(2);
    });

    test("Ctrl+W does nothing when only one tab exists", async ({ page }) => {
      await setupTerminalMocks(page, { initialTabs: SINGLE_TAB });
      await navigateToTerminal(page);

      await page.keyboard.press("Control+w");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Tab still exists
      await expect(
        page.getByTestId("terminal-tab-lead-claude-1")
      ).toBeVisible();
      const count = await page
        .getByTestId("terminal-tab-bar")
        .locator('[role="tab"]')
        .count();
      expect(count).toBe(1);
    });
  });

  // -----------------------------------------------------------------------
  // Search shortcuts
  // -----------------------------------------------------------------------
  test.describe("Search shortcuts", () => {
    test("Ctrl+F opens search bar", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();
      await expect(page.getByTestId("terminal-search-input")).toBeFocused();
    });

    test("Ctrl+F toggles search bar closed when open", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Open
      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();

      // Close
      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-search-bar")
      ).not.toBeVisible();
    });

    test("Escape closes search bar when open", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Open search
      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();

      // Escape closes search, but terminal view stays
      await page.keyboard.press("Escape");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-search-bar")
      ).not.toBeVisible();
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Enter in search bar retains focus", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);

      const searchInput = page.getByTestId("terminal-search-input");
      await searchInput.fill("test query");
      await searchInput.press("Enter");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Search input retains focus (search remains open and active)
      await expect(searchInput).toBeFocused();
    });

    test("Shift+Enter in search bar retains focus", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);

      const searchInput = page.getByTestId("terminal-search-input");
      await searchInput.fill("test query");
      await searchInput.press("Shift+Enter");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(searchInput).toBeFocused();
    });
  });

  // -----------------------------------------------------------------------
  // Help popover
  // -----------------------------------------------------------------------
  test.describe("Help popover", () => {
    test("Help button opens help popover", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-help-button").click();
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(page.getByTestId("terminal-help-popover")).toBeVisible();
    });

    // HelpPopover closes on Escape via its own keydown listener.
    // TerminalView's Escape handler also fires (doesn't guard on isHelpOpen)
    // but onEscape is a no-op due to loomcli-ynv4y.15 (previousViewRef bug).
    test("Escape closes help popover", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-help-button").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("terminal-help-popover")).toBeVisible();

      await page.keyboard.press("Escape");
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-help-popover")
      ).not.toBeVisible();
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Clicking outside closes help popover", async ({ page }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.getByTestId("terminal-help-button").click();
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("terminal-help-popover")).toBeVisible();

      // Click on the tab bar area (outside the popover)
      await page.getByTestId("terminal-tab-bar").click({ force: true });
      await page.waitForTimeout(DOM_SETTLE_MS);

      await expect(
        page.getByTestId("terminal-help-popover")
      ).not.toBeVisible();
    });
  });

  // -----------------------------------------------------------------------
  // Escape key behavior
  // -----------------------------------------------------------------------
  test.describe("Escape key behavior", () => {
    test("Escape is no-op when no overlays are open", async ({ page }) => {
      // NOTE: Escape-returns-to-previous-view is broken because
      // App.tsx previousViewRef tracks the current view, not the prior one.
      // See loomcli-ynv4y.15.
      // This test verifies Escape doesn't crash when no overlays are open.
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      await page.keyboard.press("Escape");
      await page.waitForTimeout(DOM_SETTLE_MS);

      // Terminal view remains visible (Escape fires onEscape but it's a no-op)
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Escape closes search bar first, leaving terminal visible", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Open search
      await page.keyboard.press("Control+f");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(page.getByTestId("terminal-search-bar")).toBeVisible();

      // Escape: closes search, terminal stays
      await page.keyboard.press("Escape");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("terminal-search-bar")
      ).not.toBeVisible();
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });

    test("Escape closes backend picker prompt first, leaving terminal visible", async ({
      page,
    }) => {
      await setupTerminalMocks(page);
      await navigateToTerminal(page);

      // Open backend picker prompt
      await page.keyboard.press("Control+t");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("backend-picker-prompt-overlay")
      ).toBeVisible();

      // Escape: closes prompt, terminal stays
      await page.keyboard.press("Escape");
      await page.waitForTimeout(DOM_SETTLE_MS);
      await expect(
        page.getByTestId("backend-picker-prompt-overlay")
      ).not.toBeVisible();
      await expect(page.getByTestId("terminal-view")).toBeVisible();
    });
  });
});

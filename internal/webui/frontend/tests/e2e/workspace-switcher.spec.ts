import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for WorkspaceSwitcher dropdown component.
 *
 * Tests rendering, search filtering, keyboard navigation, mouse interactions,
 * workspace selection, overlay dismiss behavior, and edge cases — all against
 * mocked API data in a Chromium browser.
 */

// -- Mock data --

const mockWorkspaceData = {
  id: "ws-alpha",
  name: "alpha",
  path: "/workspaces/alpha",
  repos: [
    {
      name: "repo-one",
      path: "/workspaces/alpha/repo-one",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "ws-alpha",
      name: "alpha",
      path: "/workspaces/alpha",
      active: true,
      repo_count: 3,
      is_default: true,
    },
    {
      id: "ws-beta",
      name: "beta",
      path: "/workspaces/beta",
      active: false,
      repo_count: 1,
      is_default: false,
    },
    {
      id: "ws-gamma",
      name: "gamma",
      path: "/workspaces/gamma",
      active: false,
      repo_count: 2,
      is_default: false,
    },
  ],
  workspace_order: ["ws-alpha", "ws-beta", "ws-gamma"],
  default_workspace: "ws-alpha",
};

const mockIssue = {
  id: "switcher-test-1",
  title: "Placeholder Issue",
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
  // Consolidated workspace API handler
  await page.route("**/api/workspaces/**", async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;

    // /api/workspaces/active
    if (pathname === "/api/workspaces/active") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
      return;
    }

    // /api/workspaces/{id} (exact)
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

    // /api/workspaces/{id}/stats
    if (pathname.match(/\/api\/workspaces\/[^/]+\/stats$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({
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
        }),
      });
      return;
    }

    // /api/workspaces/{id}/blocked
    if (pathname.match(/\/api\/workspaces\/[^/]+\/blocked/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
      return;
    }

    // /api/workspaces/{id}/terminal/tabs
    if (pathname.match(/\/terminal\/tabs(\/|$)/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
      return;
    }

    // /api/workspaces/{id}/terminal/sessions/by-issue
    if (pathname.includes("/terminal/sessions/by-issue")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({}),
      });
      return;
    }

    // /api/workspaces/{id}/terminal/state
    if (pathname.match(/\/terminal\/state$/)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      });
      return;
    }

    // /api/workspaces/{id}/config/backend
    if (pathname.includes("/config/backend")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({ backends: [] }),
      });
      return;
    }

    // /api/workspaces/{id}/events (SSE)
    if (pathname.match(/\/api\/workspaces\/[^/]+\/events/)) {
      await route.fulfill({
        status: 200,
        contentType: "text/event-stream",
        headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
        body: 'event: connected\ndata: {"message":"connected"}\n\n',
      });
      return;
    }

    // Default: 404 for unhandled workspace sub-paths
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

  // Loom endpoints
  await page.route("**/api/loom/**", async (route) => {
    if (route.request().url().includes("/health")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok" }),
      });
    } else {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({}),
      });
    }
  });

  // Issues via addInitScript to survive StrictMode
  await page.addInitScript((issue: typeof mockIssue) => {
    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      const url = typeof input === "string" ? input : input.toString();
      const method = init?.method ?? "GET";
      if (
        /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) &&
        method === "GET"
      ) {
        return Promise.resolve(
          new Response(JSON.stringify({ success: true, data: [issue] }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      return originalFetch(input, init);
    };
  }, mockIssue);
}

/** Navigate to workspace and wait for workspace API response. */
async function navigateAndWait(page: Page) {
  await page.goto("/ws/ws-alpha/", { waitUntil: "domcontentloaded" });
  // Wait for workspace data to load (needed for isMultiRepo → Ctrl+K handler)
  await page.waitForResponse(
    (res) =>
      res.url().includes("/api/workspaces/ws-alpha") &&
      !res.url().includes("/events") &&
      res.status() === 200,
    { timeout: 10000 },
  );
  // Let React re-render with workspace data
  await page.waitForTimeout(500);
}

/** Open the workspace switcher via Ctrl+K. */
async function openSwitcher(page: Page) {
  // Ensure focus isn't trapped in a search input
  await page.locator("main").click();
  await page.keyboard.press("Control+k");
  const dialog = page.getByRole("dialog", { name: "Switch workspace" });
  await expect(dialog).toBeVisible({ timeout: 5000 });
  return dialog;
}

// -- Tests --

test.describe("Workspace Switcher Dropdown", () => {
  test.describe("Display Tests", () => {
    test.beforeEach(async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);
    });

    test("opens via Ctrl+K shortcut", async ({ page }) => {
      await page.locator("main").click();
      await page.keyboard.press("Control+k");
      const dialog = page.getByRole("dialog", { name: "Switch workspace" });
      await expect(dialog).toBeVisible({ timeout: 5000 });
    });

    test("renders dialog with correct aria attributes", async ({ page }) => {
      const dialog = await openSwitcher(page);
      await expect(dialog).toHaveAttribute("aria-modal", "true");
      await expect(dialog).toHaveAttribute("aria-label", "Switch workspace");
    });

    test("displays all workspace names, paths, and repo counts", async ({
      page,
    }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");
      await expect(items).toHaveCount(3);

      // Names
      await expect(dialog.getByText("alpha").first()).toBeVisible();
      await expect(dialog.getByText("beta").first()).toBeVisible();
      await expect(dialog.getByText("gamma").first()).toBeVisible();

      // Paths
      await expect(dialog.getByText("/workspaces/alpha")).toBeVisible();
      await expect(dialog.getByText("/workspaces/beta")).toBeVisible();
      await expect(dialog.getByText("/workspaces/gamma")).toBeVisible();

      // Repo counts
      await expect(dialog.getByText("3 repos").first()).toBeVisible();
      await expect(dialog.getByText("1 repo").first()).toBeVisible();
      await expect(dialog.getByText("2 repos").first()).toBeVisible();
    });

    test("shows active indicator (checkmark) for current workspace", async ({
      page,
    }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // First item (alpha) is the active workspace — should have checkmark
      await expect(items.nth(0).getByText("✓")).toBeVisible();

      // Other items should not have checkmark
      await expect(items.nth(1).getByText("✓")).not.toBeVisible();
      await expect(items.nth(2).getByText("✓")).not.toBeVisible();
    });

    test("shows shortcut hints for first 9 workspaces", async ({ page }) => {
      const dialog = await openSwitcher(page);
      // Since tests run on Linux, modifier is "Ctrl+"
      await expect(dialog.getByText("Ctrl+Shift+1")).toBeVisible();
      await expect(dialog.getByText("Ctrl+Shift+2")).toBeVisible();
      await expect(dialog.getByText("Ctrl+Shift+3")).toBeVisible();
    });

    test("auto-focuses search input on open", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const searchInput = dialog.getByTestId("search-input-field");
      await expect(searchInput).toBeFocused();
    });
  });

  test.describe("Search Filtering", () => {
    test.beforeEach(async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);
    });

    test("filters workspaces by name substring", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      await dialog.getByTestId("search-input-field").fill("bet");
      await expect(items).toHaveCount(1);
      await expect(dialog.getByText("beta").first()).toBeVisible();
    });

    test("filters workspaces by path substring", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      await dialog.getByTestId("search-input-field").fill("/workspaces/gamma");
      await expect(items).toHaveCount(1);
      await expect(dialog.getByText("gamma").first()).toBeVisible();
    });

    test("search is case-insensitive", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      await dialog.getByTestId("search-input-field").fill("ALPHA");
      await expect(items).toHaveCount(1);
      await expect(dialog.getByText("alpha").first()).toBeVisible();
    });

    test("shows 'No workspaces found' when nothing matches", async ({
      page,
    }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      await dialog.getByTestId("search-input-field").fill("nonexistent");
      await expect(items).toHaveCount(0);
      await expect(dialog.getByText("No workspaces found")).toBeVisible();
    });

    test("clearing search shows all workspaces again", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // Filter down
      const searchInput = dialog.getByTestId("search-input-field");
      await searchInput.fill("beta");
      await expect(items).toHaveCount(1);

      // Clear via the clear button
      const clearBtn = dialog.getByTestId("search-input-clear");
      await clearBtn.click();

      // All workspaces should be visible again
      await expect(items).toHaveCount(3);
    });
  });

  test.describe("Keyboard Navigation", () => {
    test.beforeEach(async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);
    });

    test("ArrowDown moves highlight to next item", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // Initial highlight is on index 0 (alpha)
      // Press ArrowDown to move to index 1 (beta)
      await page.keyboard.press("ArrowDown");

      // Verify by pressing Enter — should select beta
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-beta/**", { timeout: 5000 });
    });

    test("ArrowUp moves highlight to previous item", async ({ page }) => {
      const dialog = await openSwitcher(page);

      // Move down to index 2 (gamma), then up to index 1 (beta)
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowUp");

      // Verify by pressing Enter — should select beta
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-beta/**", { timeout: 5000 });
    });

    test("ArrowDown wraps from last to first", async ({ page }) => {
      const dialog = await openSwitcher(page);

      // Move to last item (index 2 = gamma)
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");

      // One more ArrowDown should wrap to index 0 (alpha)
      await page.keyboard.press("ArrowDown");

      // Verify by pressing Enter — should select alpha
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-alpha/**", { timeout: 5000 });
    });

    test("ArrowUp wraps from first to last", async ({ page }) => {
      const dialog = await openSwitcher(page);

      // ArrowUp from index 0 should wrap to index 2 (gamma)
      await page.keyboard.press("ArrowUp");

      // Verify by pressing Enter — should select gamma
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-gamma/**", { timeout: 5000 });
    });

    test("Enter selects the highlighted workspace", async ({ page }) => {
      const dialog = await openSwitcher(page);

      // Default highlight is on index 0 (alpha)
      await page.keyboard.press("Enter");

      // Should select alpha and close dialog
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-alpha/**", { timeout: 5000 });
    });

    test("Enter with empty filtered results does nothing", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const currentUrl = page.url();

      // Filter to no results
      await dialog.getByTestId("search-input-field").fill("nonexistent");
      await expect(dialog.getByText("No workspaces found")).toBeVisible();

      // Press Enter — dialog should stay open, URL unchanged
      await page.keyboard.press("Enter");
      await expect(dialog).toBeVisible();
      expect(page.url()).toBe(currentUrl);
    });
  });

  test.describe("Mouse Interactions", () => {
    test.beforeEach(async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);
    });

    test("clicking a workspace item selects it and closes dialog", async ({
      page,
    }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // Click the second item (beta)
      await items.nth(1).click();

      // Dialog should close and navigate to beta
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-beta/**", { timeout: 5000 });
    });

    test("hovering a workspace item highlights it", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // Hover over the third item (gamma)
      await items.nth(2).hover();

      // Verify highlight moved by pressing Enter — should select gamma
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-gamma/**", { timeout: 5000 });
    });

    test("clicking the overlay backdrop closes without selecting", async ({
      page,
    }) => {
      const dialog = await openSwitcher(page);
      const currentUrl = page.url();

      // Click outside the dialog on the overlay
      // The overlay is the parent of the dialog. onMouseDown on overlay
      // fires only when e.target === e.currentTarget (i.e., clicked directly
      // on the overlay, not on the dialog). Click at top-left corner of viewport.
      await page.mouse.click(5, 5);

      // Dialog should close, URL unchanged
      await expect(dialog).not.toBeVisible();
      expect(page.url()).toBe(currentUrl);
    });
  });

  test.describe("Dismiss Behavior", () => {
    test.beforeEach(async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);
    });

    test("Escape closes the switcher", async ({ page }) => {
      const dialog = await openSwitcher(page);
      const currentUrl = page.url();

      await page.keyboard.press("Escape");

      await expect(dialog).not.toBeVisible();
      expect(page.url()).toBe(currentUrl);
    });

    test("reopening resets search term", async ({ page }) => {
      let dialog = await openSwitcher(page);

      // Type a search term
      await dialog.getByTestId("search-input-field").fill("beta");
      await expect(dialog.locator("[data-workspace-item]")).toHaveCount(1);

      // Close and reopen
      await page.keyboard.press("Escape");
      await expect(dialog).not.toBeVisible();

      dialog = await openSwitcher(page);
      const searchInput = dialog.getByTestId("search-input-field");
      await expect(searchInput).toHaveValue("");
      await expect(dialog.locator("[data-workspace-item]")).toHaveCount(3);
    });

    test("reopening resets highlight to first item", async ({ page }) => {
      let dialog = await openSwitcher(page);

      // Move highlight down to gamma (index 2)
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");

      // Close
      await page.mouse.click(5, 5);
      await expect(dialog).not.toBeVisible();

      dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");
      await expect(items).toHaveCount(3);

      // The component unmounts when closed (returns null when !isOpen) and
      // remounts with fresh useState(0) for highlightIndex. Verify by
      // checking that the first item has the highlighted class applied
      // (its className count differs from item at index 1).
      const firstClassCount = await items
        .nth(0)
        .evaluate((el) => el.className.split(" ").length);
      const secondClassCount = await items
        .nth(1)
        .evaluate((el) => el.className.split(" ").length);
      // First item should have more classes (item + highlighted + active)
      // than second item (item only)
      expect(firstClassCount).toBeGreaterThan(secondClassCount);
    });
  });

  test.describe("Edge Cases", () => {
    test("works with a single workspace", async ({ page }) => {
      const singleWsData = {
        ...mockWorkspaceData,
        workspaces: [
          {
            id: "ws-solo",
            name: "solo",
            path: "/workspaces/solo",
            active: true,
            repo_count: 1,
            is_default: true,
          },
        ],
        workspace_order: ["ws-solo"],
      };

      // Override workspace mock with single workspace
      await page.route("**/api/workspaces/**", async (route) => {
        const url = new URL(route.request().url());
        const pathname = url.pathname;

        if (
          pathname === "/api/workspaces/active" ||
          pathname.match(/^\/api\/workspaces\/[^/]+\/?$/)
        ) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok(singleWsData),
          });
          return;
        }

        if (pathname.match(/\/api\/workspaces\/[^/]+\/stats$/)) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok({
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

      // Set up remaining mocks (health, auth, loom, issues)
      await page.route("**/api/health", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ status: "ok", daemon: true }),
        });
      });

      await page.route("**/api/auth/token", async (route) => {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "test-token-e2e" }),
        });
      });

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

      await page.route("**/api/loom/**", async (route) => {
        if (route.request().url().includes("/health")) {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ status: "ok" }),
          });
        } else {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({}),
          });
        }
      });

      await page.addInitScript((issue: typeof mockIssue) => {
        const originalFetch = window.fetch.bind(window);
        window.fetch = function (
          input: RequestInfo | URL,
          init?: RequestInit,
        ): Promise<Response> {
          const url = typeof input === "string" ? input : input.toString();
          if (
            /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) &&
            (init?.method ?? "GET") === "GET"
          ) {
            return Promise.resolve(
              new Response(JSON.stringify({ success: true, data: [issue] }), {
                status: 200,
                headers: { "Content-Type": "application/json" },
              }),
            );
          }
          return originalFetch(input, init);
        };
      }, mockIssue);

      await page.goto("/ws/ws-solo/", { waitUntil: "domcontentloaded" });
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/workspaces/ws-solo") &&
          !res.url().includes("/events") &&
          res.status() === 200,
        { timeout: 10000 },
      );
      await page.waitForTimeout(500);

      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      await expect(items).toHaveCount(1);
      await expect(dialog.getByText("solo").first()).toBeVisible();
      await expect(items.nth(0).getByText("✓")).toBeVisible();

      // Select the single workspace
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
    });

    test("keyboard nav after filtering adjusts to filtered list size", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const dialog = await openSwitcher(page);
      const items = dialog.locator("[data-workspace-item]");

      // Move highlight to index 2 (gamma)
      await page.keyboard.press("ArrowDown");
      await page.keyboard.press("ArrowDown");

      // Filter to only "alpha" — highlight should clamp to index 0
      await dialog.getByTestId("search-input-field").fill("alpha");
      await expect(items).toHaveCount(1);

      // ArrowDown should wrap back to index 0 (only 1 item)
      await page.keyboard.press("ArrowDown");

      // Press Enter — should select alpha
      await page.keyboard.press("Enter");
      await expect(dialog).not.toBeVisible();
      await page.waitForURL("**/ws/ws-alpha/**", { timeout: 5000 });
    });
  });
});

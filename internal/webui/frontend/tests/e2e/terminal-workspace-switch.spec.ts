/**
 * E2E regression test for terminal tabs surviving a workspace round trip.
 *
 * Switching workspaces and coming back used to drop the tab bar to a single
 * stray `lead-{backend}-1` session: TerminalView remounts with the Terminal
 * view inactive, and when the user opened Terminal the readiness flag was read
 * stale in that same commit, so the "empty workspace -> auto-create a default
 * tab" branch ran against a tab list that had not been fetched yet.
 *
 * The switch is driven through the in-app workspace switcher (Ctrl+K), not a
 * page.goto: a full page load is the path that always worked, so navigating
 * that way would not exercise the bug at all.
 *
 * All backend interactions are mocked via page.route() — no real backend.
 */

import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Mock data — two workspaces, each with its own terminal tab metadata
// ---------------------------------------------------------------------------

const WS_A = "ws-alpha";
const WS_A_NAME = "alpha";
const WS_B = "ws-beta";
const WS_B_NAME = "beta";

/** How many times to repeat the round trip — the bug reproduced ~2 in 3. */
const ROUND_TRIPS = 5;

interface TabMetadata {
  session_name: string;
  label: string;
  sort_order: number;
  pinned: boolean;
  notes: string;
  created_at: string;
  updated_at: string;
  pty_alive: boolean;
  attached_clients: number;
}

function makeTab(sessionName: string, sortOrder: number): TabMetadata {
  return {
    session_name: sessionName,
    label: sessionName,
    sort_order: sortOrder,
    pinned: false,
    notes: "",
    created_at: "2026-03-28T00:00:00Z",
    updated_at: "2026-03-28T00:00:00Z",
    pty_alive: true,
    attached_clients: 0,
  };
}

/** Server-side truth: workspace A has two live sessions, B has one. */
function initialTabs(): Record<string, TabMetadata[]> {
  return {
    [WS_A]: [makeTab("lead-claude-1", 0), makeTab("lead-claude-2", 1)],
    [WS_B]: [makeTab("lead-claude-9", 0)],
  };
}

function workspaceList() {
  return [
    {
      id: WS_A,
      name: WS_A_NAME,
      path: `/workspaces/${WS_A_NAME}`,
      active: true,
      repo_count: 0,
      is_default: true,
    },
    {
      id: WS_B,
      name: WS_B_NAME,
      path: `/workspaces/${WS_B_NAME}`,
      active: false,
      repo_count: 0,
      is_default: false,
    },
  ];
}

function workspaceData(id: string) {
  const name = id === WS_A ? WS_A_NAME : WS_B_NAME;
  return {
    id,
    name,
    path: `/workspaces/${name}`,
    repos: [],
    groups: [],
    agents: [],
    workspaces: workspaceList(),
    workspace_order: [WS_A, WS_B],
    default_workspace: WS_A_NAME,
  };
}

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

interface Trackers {
  /** Every PUT to /terminal/tabs/{session} — i.e. every tab the UI persisted. */
  tabPuts: string[];
}

// ---------------------------------------------------------------------------
// Mock setup
// ---------------------------------------------------------------------------

async function setupMocks(page: Page): Promise<Trackers> {
  const tabsByWorkspace = initialTabs();
  const trackers: Trackers = { tabPuts: [] };

  // Neutralize AbortController signals (React StrictMode workaround).
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

  // fetchAppConfig() runs before anything else on boot.
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
      body: ok([{ name: "claude", available: true, display_name: "Claude" }]),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-ws-switch" }),
    });
  });

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

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });

  await page.route(
    (url) => url.pathname.startsWith("/api/workspaces"),
    async (route) => {
      const url = new URL(route.request().url());
      const pathname = url.pathname;
      const method = route.request().method();

      if (pathname.includes("/events")) {
        await route.abort();
        return;
      }

      if (pathname === "/api/workspaces/active") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData(WS_A)),
        });
        return;
      }

      const wsMatch = pathname.match(/^\/api\/workspaces\/([^/]+)/);
      const workspaceId = wsMatch?.[1] ?? WS_A;

      // GET /api/workspaces/{ws}/terminal/tabs
      if (pathname.endsWith("/terminal/tabs")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(tabsByWorkspace[workspaceId] ?? []),
        });
        return;
      }

      // {GET,PUT,PATCH,DELETE} /api/workspaces/{ws}/terminal/tabs/{session}
      const sessionMatch = pathname.match(/\/terminal\/tabs\/([^/]+)$/);
      if (sessionMatch) {
        const session = decodeURIComponent(sessionMatch[1]);
        if (method === "PUT") {
          trackers.tabPuts.push(`${workspaceId}:${session}`);
          const tab = makeTab(session, 0);
          (tabsByWorkspace[workspaceId] ??= []).push(tab);
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok(tab),
          });
          return;
        }
        const existing = (tabsByWorkspace[workspaceId] ?? []).find(
          (t) => t.session_name === session,
        );
        await route.fulfill({
          status: existing ? 200 : 404,
          contentType: "application/json",
          body: existing
            ? ok(existing)
            : JSON.stringify({ success: false, error: "Not found" }),
        });
        return;
      }

      if (pathname.endsWith("/config/backend")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            backend: "claude",
            source: "project",
            available: ["claude"],
            agents: [],
          }),
        });
        return;
      }

      if (pathname.endsWith("/terminal/state")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ active_tab: "" }),
        });
        return;
      }

      if (pathname.endsWith("/terminal/token")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ token: "ws-token-mock" }),
        });
        return;
      }

      if (pathname.includes("/terminal/")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({}),
        });
        return;
      }

      if (pathname.endsWith("/stats")) {
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

      if (pathname.endsWith("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok([]),
        });
        return;
      }

      // /api/workspaces/{id}
      if (pathname.match(/^\/api\/workspaces\/[^/]+\/?$/)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData(workspaceId)),
        });
        return;
      }

      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  return trackers;
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function tabBar(page: Page) {
  return page.getByTestId("terminal-tab-bar");
}

/** Switch workspace through the in-app switcher (Ctrl+K), not a page load. */
async function switchWorkspace(
  page: Page,
  name: string,
  id: string,
): Promise<void> {
  // Click the sidebar's active-workspace button rather than pressing Ctrl+K:
  // on the Terminal view the keystroke is captured by the terminal.
  await page
    .getByRole("button", { name: /Active workspace:.*Click to switch\./ })
    .click();
  const dialog = page.getByRole("dialog", { name: "Switch workspace" });
  await expect(dialog).toBeVisible({ timeout: 5000 });
  await dialog.getByTestId("search-input-field").fill(name);
  const item = dialog.locator("[data-workspace-item]").first();
  await expect(item).toBeVisible({ timeout: 5000 });
  await item.click();
  await expect(dialog).toBeHidden({ timeout: 5000 });
  await page.waitForURL(`**/ws/${id}/**`, { timeout: 10000 });
}

async function openTerminalView(page: Page): Promise<void> {
  await page
    .locator('nav[aria-label="Primary"]')
    .getByRole("button", { name: "Terminal" })
    .click();
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Terminal tabs across workspace switches", () => {
  test("keeps the workspace's tabs and mints no stray session", async ({
    page,
  }) => {
    const trackers = await setupMocks(page);

    await page.goto(`/ws/${WS_A}/terminal`);
    await page.waitForSelector('[role="banner"]', { timeout: 15000 });
    await expect(tabBar(page)).toBeVisible({ timeout: 10000 });
    await expect(page.getByTestId("terminal-tab-lead-claude-1")).toBeVisible();
    await expect(page.getByTestId("terminal-tab-lead-claude-2")).toBeVisible();

    for (let i = 0; i < ROUND_TRIPS; i++) {
      await switchWorkspace(page, WS_B_NAME, WS_B);
      await openTerminalView(page);
      await expect(page.getByTestId("terminal-tab-lead-claude-9")).toBeVisible({
        timeout: 10000,
      });

      await switchWorkspace(page, WS_A_NAME, WS_A);
      await openTerminalView(page);

      // Round trip ${i}: both of workspace A's sessions are still listed.
      await expect(page.getByTestId("terminal-tab-lead-claude-1")).toBeVisible({
        timeout: 10000,
      });
      await expect(page.getByTestId("terminal-tab-lead-claude-2")).toBeVisible();
    }

    // Nothing was auto-created on the way back: the regression persisted a
    // `lead-{backend}-1` tab through PUT /terminal/tabs/{session}.
    expect(trackers.tabPuts).toEqual([]);
  });
});

/**
 * Shared helpers for keyboard E2E tests.
 *
 * Provides bootApp() to set up a fully mocked app with optional multi-workspace
 * support, plus focus assertion helpers and mock workspace data factories.
 */

import type { Page } from "@playwright/test";
import type { Issue } from "../../src/types/issue";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface WorkspaceMock {
  id: string;
  name: string;
  path: string;
  active: boolean;
  repo_count: number;
  is_default: boolean;
}

export interface BootAppOptions {
  /** Enable multi-workspace mode (default: false). */
  multiWorkspace?: boolean;
  /** Custom workspace list. If omitted, defaults are generated. */
  workspaces?: WorkspaceMock[];
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

var WORKSPACE_ID = "kbd-test-ws";

var DEFAULT_SINGLE_WORKSPACE: WorkspaceMock[] = [
  {
    id: WORKSPACE_ID,
    name: "Keyboard Test Workspace",
    path: "/tmp/kbd-test-ws",
    active: true,
    repo_count: 1,
    is_default: true,
  },
];

var MOCK_ISSUES = [
  {
    id: "kbd-001",
    title: "Implement auth flow",
    status: "open",
    priority: 0,
    issue_type: "feature",
    assignee: "alice",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
    depends_on: [],
  },
  {
    id: "kbd-002",
    title: "Add test coverage",
    status: "open",
    priority: 1,
    issue_type: "task",
    assignee: "bob",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
    depends_on: [],
  },
  {
    id: "kbd-003",
    title: "Build API endpoints",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    assignee: "alpha",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
    depends_on: [],
  },
  {
    id: "kbd-004",
    title: "Fix navigation bug",
    status: "closed",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-24T14:00:00Z",
    updated_at: "2026-01-24T14:00:00Z",
    depends_on: [],
  },
];

var MOCK_STATS = {
  total_issues: 4,
  open_issues: 2,
  in_progress_issues: 1,
  closed_issues: 1,
  blocked_issues: 0,
  deferred_issues: 0,
  ready_issues: 2,
  tombstone_issues: 0,
  pinned_issues: 0,
  epics_eligible_for_closure: 0,
  average_lead_time_hours: 24,
};

// ---------------------------------------------------------------------------
// Factory helpers
// ---------------------------------------------------------------------------

/**
 * Generate N mock workspaces for WorkspaceSwitcher tests.
 * The first workspace is always active/default; the rest are inactive.
 */
export function createMockWorkspaces(count: number): WorkspaceMock[] {
  return Array.from({ length: count }, function (_, i) {
    return {
      id: "ws-" + (i + 1),
      name: "Workspace " + (i + 1),
      path: "/tmp/ws-" + (i + 1),
      active: i === 0,
      repo_count: i + 1,
      is_default: i === 0,
    };
  });
}

// ---------------------------------------------------------------------------
// Boot helper
// ---------------------------------------------------------------------------

/**
 * Set up API mocks and navigate to the app.
 *
 * Follows the exact same pattern as journey-project-status.spec.ts which is known to work.
 * Uses direct page.route() calls without the ApiMockHandler or SSEMockController fixtures,
 * avoiding the glob pattern that matches Vite source files.
 */
export async function bootApp(
  page: Page,
  _mockApi: unknown,
  options: BootAppOptions = {},
): Promise<Page> {
  var multiWorkspace = options.multiWorkspace ?? false;
  var wsList = options.workspaces ??
    (multiWorkspace ? createMockWorkspaces(3) : DEFAULT_SINGLE_WORKSPACE);
  var activeId = wsList.find(function (ws) { return ws.active; })?.id ?? wsList[0]?.id ?? WORKSPACE_ID;
  var wsData = {
    id: activeId,
    name: wsList.find(function (ws) { return ws.id === activeId; })?.name ?? "Test",
    path: wsList.find(function (ws) { return ws.id === activeId; })?.path ?? "/tmp/test",
    repos: multiWorkspace ? [{ name: "test-repo", path: "/tmp/test-repo", default_branch: "main", remote: "origin", groups: [] }] : [],
    groups: [],
    agents: [],
    workspaces: wsList,
    default_workspace: wsList.find(function (ws) { return ws.is_default; })?.id ?? activeId,
  };
  var wsUrl = "/ws/" + activeId + "/";
  var wsPrefix = "/api/workspaces/" + activeId;

  await page.route("**/api/config", async function (route) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/backends", async function (route) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: [{ name: "claude", available: true, display_name: "Claude" }],
      }),
    });
  });

  // Auth token
  await page.route("**/api/auth/token", async function (route) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-kbd" }),
    });
  });

  // Daemon health
  await page.route("**/api/health", async function (route) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        status: "ok",
        daemon: { connected: true, status: "running", uptime: 1000, version: "test" },
      }),
    });
  });

  // Single handler for ALL workspace-scoped API endpoints (same pattern as journey test)
  await page.route(function (url) {
    var s = url.toString();
    return s.includes("/api/workspaces/") && !s.includes("/src/");
  }, async function (route) {
    var url = route.request().url();

    // Workspace resolution: /api/workspaces/active
    if (url.includes("/api/workspaces/active")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: wsData }),
      });
      return;
    }

    // SSE events: abort to prevent networkidle timeout
    if (url.includes(wsPrefix + "/events")) {
      await route.abort();
      return;
    }

    if (url.includes(wsPrefix + "/config/backend")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            backend: "claude",
            source: "workspace",
            available: ["claude"],
            agents: [],
          },
        }),
      });
      return;
    }

    // Issues graph
    if (url.includes(wsPrefix + "/issues/graph")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: MOCK_ISSUES }),
      });
      return;
    }

    // Issues list (kanban mode)
    if (url.includes(wsPrefix + "/issues")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: MOCK_ISSUES }),
      });
      return;
    }

    // Ready endpoint
    if (url.includes(wsPrefix + "/ready")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: MOCK_ISSUES }),
      });
      return;
    }

    // Blocked issues
    if (url.includes(wsPrefix + "/blocked")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
      return;
    }

    // Stats
    if (url.includes(wsPrefix + "/stats")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: MOCK_STATS }),
      });
      return;
    }

    if (url.includes(wsPrefix + "/terminal/tabs")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
      return;
    }

    if (url.includes(wsPrefix + "/terminal/state")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      });
      return;
    }

    // Exact workspace path (validation by WorkspaceLayout)
    if (url.includes(wsPrefix)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: wsData }),
      });
      return;
    }

    // Unknown workspace endpoint
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    });
  });

  // Monitor server endpoints (global)
  await page.route("**/api/monitor/**", async function (route) {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });

  await page.addInitScript(function () {
    window.localStorage.clear();
    window.sessionStorage.clear();
  });

  // Navigate to workspace settings; it renders the global shortcut provider
  // without depending on board layout details.
  await page.goto(wsUrl + "settings");
  await page.waitForSelector('[role="banner"]', { timeout: 15000 });

  // Blur any focused input so keyboard shortcuts aren't suppressed
  await page.evaluate(function () {
    var el = document.activeElement as HTMLElement | null;
    if (el && el !== document.body) el.blur();
  });

  return page;
}

// ---------------------------------------------------------------------------
// Focus assertion helpers
// ---------------------------------------------------------------------------

/**
 * Returns the data-testid attribute of the currently focused element,
 * or the tagName as fallback.
 */
export async function focusedTestId(page: Page): Promise<string> {
  return page.evaluate(function () {
    var el = document.activeElement;
    if (!el) return "null";
    return el.getAttribute("data-testid") ?? el.tagName.toLowerCase();
  });
}

/**
 * Returns a description of the currently focused element.
 */
export async function focusedSelector(page: Page): Promise<string> {
  return page.evaluate(function () {
    var el = document.activeElement;
    if (!el) return "null";
    var testId = el.getAttribute("data-testid");
    if (testId) return "[data-testid=" + JSON.stringify(testId) + "]";
    var tag = el.tagName.toLowerCase();
    if (el.id) return tag + "#" + el.id;
    return tag;
  });
}

/**
 * Assert that the currently focused element has the given data-testid.
 * Uses expect.poll for async focus changes.
 */
export async function expectFocused(
  page: Page,
  testId: string,
  options?: { timeout?: number },
): Promise<void> {
  var pw = await import("@playwright/test");
  await pw.expect
    .poll(function () { return focusedTestId(page); }, {
      message: "Expected focused element to have data-testid=" + JSON.stringify(testId),
      timeout: options?.timeout ?? 5000,
    })
    .toBe(testId);
}

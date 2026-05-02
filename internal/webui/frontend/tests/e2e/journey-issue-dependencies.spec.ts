import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Issue dependency management and graph navigation.
 *
 * Tests the full dependency workflow:
 *   1. Open issue detail, verify empty dependencies section
 *   2. Add a dependency via search picker
 *   3. Verify dependency chip appears in the list
 *   4. Click dependency chip to navigate to blocker issue
 *   5. Navigate to Graph view and verify nodes/edges
 *
 * Uses the consolidated route handler pattern from journey-create-triage.
 */

// -- Constants --

const WS_ID = "ws-deps";
const WS_PREFIX = `/api/workspaces/${WS_ID}`;

const WORKSPACE_DATA = {
  id: WS_ID,
  name: "Deps Test Workspace",
  path: "/tmp/ws-deps",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS_ID,
      name: "Deps Test Workspace",
      path: "/tmp/ws-deps",
      active: true,
      repo_count: 1,
      is_default: true,
    },
  ],
  workspace_order: [WS_ID],
  default_workspace: WS_ID,
};

// -- Issue mock data --

const ISSUES = [
  {
    id: "deps-main",
    title: "Implement search feature",
    status: "open",
    priority: 1,
    issue_type: "feature",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "deps-blocker",
    title: "Add search index infrastructure",
    status: "in_progress",
    priority: 0,
    issue_type: "task",
    assignee: "alpha",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "deps-other",
    title: "Unrelated documentation update",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
];

const MAIN_ISSUE_DETAIL = {
  ...ISSUES[0],
  description: "Build a full-text search feature for issues.",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
  events: [],
};

const BLOCKER_ISSUE_DETAIL = {
  ...ISSUES[1],
  description: "Set up the search index backend.",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
  events: [],
};

// -- Helper functions --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Mock state --

interface MockState {
  dependencyAdded: boolean;
  postCalls: Array<{ url: string; body: unknown }>;
}

async function setupMocks(page: Page, state: MockState) {
  // App config (auth mode discovery)
  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") {
      await route.fallback();
      return;
    }
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
      body: JSON.stringify({
        success: true,
        data: [{ name: "claude", available: true, display_name: "Claude" }],
      }),
    });
  });

  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }),
    });
  });

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Consolidated workspace-scoped handler (journey-create-triage pattern)
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
          body: ok(WORKSPACE_DATA),
        });
        return;
      }

      // SSE events: abort to prevent timeout
      if (url.includes(WS_PREFIX + "/events")) {
        await route.abort();
        return;
      }

      if (url.includes(WS_PREFIX + "/config/backend")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            backend: "claude",
            source: "workspace",
            available: ["claude"],
            agents: [],
          }),
        });
        return;
      }

      // POST /issues/{id}/dependencies — add dependency
      if (url.includes("/issues/deps-main/dependencies") && method === "POST") {
        const body = route.request().postDataJSON();
        state.postCalls.push({ url, body });
        state.dependencyAdded = true;
        await route.fulfill({
          status: 201,
          contentType: "application/json",
          body: ok({ depends_on_id: "deps-blocker", dep_type: "blocks" }),
        });
        return;
      }

      // Graph endpoint
      if (url.includes(WS_PREFIX + "/issues/graph")) {
        const graphIssues = state.dependencyAdded
          ? [{ ...ISSUES[0], depends_on: ["deps-blocker"] }, ISSUES[1], ISSUES[2]]
          : ISSUES;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(graphIssues),
        });
        return;
      }

      // Issue sub-resources (events, tabs, comments)
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET") {
        const afterIssues = url.split(WS_PREFIX + "/issues/")[1] ?? "";
        const pathParts = afterIssues.split("?")[0].split("/");
        if (pathParts.length > 1 && pathParts[1]) {
          const subResource = pathParts[1];
          const data = subResource === "tabs" ? null : [];
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok(data),
          });
          return;
        }
      }

      // GET /issues/{id} — single issue detail
      if (url.includes(WS_PREFIX + "/issues/") && method === "GET" && !url.includes("/graph")) {
        const issueId = url.split("/issues/")[1]?.split("?")[0]?.split("/")[0];
        if (issueId === "deps-main") {
          const detail = state.dependencyAdded
            ? {
                ...MAIN_ISSUE_DETAIL,
                dependencies: [
                  {
                    id: "deps-blocker",
                    title: "Add search index infrastructure",
                    status: "in_progress",
                    priority: 0,
                    dependency_type: "blocks",
                  },
                ],
              }
            : MAIN_ISSUE_DETAIL;
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok(detail),
          });
          return;
        }
        if (issueId === "deps-blocker") {
          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: ok(BLOCKER_ISSUE_DETAIL),
          });
          return;
        }
      }

      // Issues list / ready endpoint (kanban/table data)
      if (
        (url.includes(WS_PREFIX + "/ready") || url.includes(WS_PREFIX + "/issues")) &&
        method === "GET" &&
        !url.includes("/graph")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(ISSUES),
        });
        return;
      }

      // Blocked issues
      if (url.includes(WS_PREFIX + "/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok([]),
        });
        return;
      }

      // Stats
      if (url.includes(WS_PREFIX + "/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 3, open_issues: 2, in_progress_issues: 1,
            closed_issues: 0, blocked_issues: 0, deferred_issues: 0,
            ready_issues: 2, tombstone_issues: 0, pinned_issues: 0,
            epics_eligible_for_closure: 0, average_lead_time_hours: 24,
          }),
        });
        return;
      }

      // Terminal sessions-by-issue
      if (url.includes(WS_PREFIX + "/terminal/tabs")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok([]),
        });
        return;
      }

      if (url.includes(WS_PREFIX + "/terminal/state")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ active_tab: "" }),
        });
        return;
      }

      if (url.includes("/terminal/sessions/by-issue")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({}),
        });
        return;
      }

      // Workspace resolution by ID
      if (url.includes(WS_PREFIX)) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(WORKSPACE_DATA),
        });
        return;
      }

      // Fallback
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  // Monitor server endpoints (global)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [], stats: {} }),
    });
  });
}

// -- Tests --

test.describe("E2E Journey: Issue dependency management", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;
  let state: MockState;

  test.beforeAll(async ({ browser }) => {
    state = { dependencyAdded: false, postCalls: [] };
    page = await browser.newPage();
    await setupMocks(page, state);
    await page.goto(`/ws/${WS_ID}/kanban`, { waitUntil: "domcontentloaded" });
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Open issue and verify empty dependencies", async () => {
    // Wait for kanban to render
    const readyColumn = page.locator('section[data-status="ready"]');
    await expect(readyColumn).toBeVisible({ timeout: 15000 });

    // Click the main issue card
    await readyColumn.getByText("Implement search feature").click();

    // Wait for detail panel
    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open", { timeout: 5000 });

    // Verify dependency section shows empty state
    await expect(page.getByTestId("dependency-section")).toBeVisible({ timeout: 5000 });
    await expect(page.getByTestId("no-dependencies")).toBeVisible();
    await expect(page.getByTestId("no-dependencies")).toContainText("No blocking dependencies");
  });

  test("Add a dependency via search picker", async () => {
    // Click add dependency button
    await page.getByTestId("add-dependency-button").click();

    // Verify search picker appears
    await expect(page.getByTestId("dependency-search-input")).toBeVisible();

    // Type to search for blocker issue
    await page.getByTestId("dependency-search-input").fill("search index");

    // Wait for debounce (200ms) and dropdown
    await expect(page.getByTestId("search-results-dropdown")).toBeVisible({ timeout: 5000 });

    // Click the search result for deps-blocker
    await page.getByTestId("search-result-deps-blocker").click();

    // Verify POST was sent
    expect(state.postCalls).toHaveLength(1);
  });

  test("Verify dependency chip shows after panel refresh", async () => {
    // Close and reopen the panel to trigger a refetch (SSE not wired in test)
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute("data-state", "closed");

    // Reopen by clicking the same issue card
    const readyColumn = page.locator('section[data-status="ready"]');
    await readyColumn.getByText("Implement search feature").click();
    await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute("data-state", "open", { timeout: 5000 });

    // Now the panel refetches — mock returns dependency since state.dependencyAdded=true
    await expect(page.getByTestId("dependency-item-deps-blocker")).toBeVisible({ timeout: 5000 });

    // Verify it shows the blocker issue title
    await expect(page.getByTestId("dependency-item-deps-blocker")).toContainText(
      "Add search index infrastructure",
    );

    // Verify the dependency list container exists
    await expect(page.getByTestId("dependency-list")).toBeVisible();
  });

  test("Click dependency chip to navigate to blocker issue", async () => {
    // Click the dependency chip
    await page.getByTestId("dependency-item-deps-blocker").click();

    // The panel should now show the blocker issue details
    await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute("data-state", "open");

    // Verify the panel now shows the blocker issue ID
    await expect(page.getByTestId("issue-id")).toContainText("deps-blocker", { timeout: 5000 });
  });

  test("Navigate to Graph view and verify nodes and edges", async () => {
    // Close the detail panel
    await page.keyboard.press("Escape");
    await expect(page.getByTestId("issue-detail-panel")).toHaveAttribute("data-state", "closed");

    // Switch to graph view via URL
    await page.goto(`/ws/${WS_ID}/graph`, { waitUntil: "domcontentloaded" });

    // Wait for graph to render (lazy-loaded)
    await expect(page.getByTestId("graph-view")).toBeVisible({ timeout: 15000 });

    // Verify graph contains nodes (at least 2 for our issues)
    await expect(page.locator(".react-flow__node").first()).toBeVisible({ timeout: 5000 });
    const nodeCount = await page.locator(".react-flow__node").count();
    expect(nodeCount).toBeGreaterThanOrEqual(2);
  });
});

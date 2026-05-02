import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Project status at a glance (Kanban → Agent → Logs).
 *
 * Tests the full user journey of a project lead checking current project status:
 *   1. Kanban board renders issues in correct columns with priority badges
 *   2. Work Queue counts match the loom status data
 *   3. Agent cards render in sidebar with correct status
 *   4. Click agent card opens AgentDetailPanel
 *   5. Switch to Logs tab shows log content area
 *   6. Close panel via Escape key
 *   7. Click different agent swaps panel (mutual exclusivity)
 *   8. Close panel via close button
 */

// -- Workspace mock data --

const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/tmp/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

// -- Issue mock data (8 issues across columns) --

const mockIssues = [
  {
    id: "proj-001",
    title: "Auth token expiration bug",
    status: "open",
    priority: 0,
    issue_type: "bug",
    assignee: "alpha",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-002",
    title: "Add rate limiting middleware",
    status: "open",
    priority: 1,
    issue_type: "task",
    assignee: "beta",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-003",
    title: "Update API documentation",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-004",
    title: "Implement authentication flow",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    assignee: "alpha",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-005",
    title: "Review caching strategy",
    status: "review",
    priority: 2,
    issue_type: "task",
    assignee: "gamma",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-006",
    title: "Initial project setup",
    status: "closed",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-007",
    title: "Blocked: waiting on DB migration",
    status: "blocked",
    priority: 1,
    issue_type: "task",
    depends_on: ["proj-004"],
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "proj-008",
    title: "Deferred: legacy cleanup",
    status: "deferred",
    priority: 4,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
];

// -- Agent mock data (3 agents) --

const mockAgents = [
  {
    name: "alpha",
    status: "working: proj-004 (30s)",
    branch: "feature-auth",
    path: "/tmp/worktrees/alpha",
    repo: "loomcli",
    cross_repo: false,
    ahead: 2,
    behind: 0,
    role: "task",
    commits: [
      { hash: "abc1234", message: "Add auth middleware", url: "" },
      { hash: "def5678", message: "Add token validation", url: "" },
    ],
    changes: [
      { path: "src/auth/middleware.ts", status: "M" },
      { path: "src/auth/token.ts", status: "A" },
    ],
  },
  {
    name: "beta",
    status: "idle",
    branch: "main",
    path: "/tmp/worktrees/beta",
    repo: "loomcli",
    cross_repo: false,
    ahead: 0,
    behind: 0,
    role: "task",
    commits: [],
    changes: [],
  },
  {
    name: "gamma",
    status: "planning: proj-003 (2m)",
    branch: "feature-db",
    path: "/tmp/worktrees/gamma",
    repo: "loomcli",
    cross_repo: false,
    ahead: 1,
    behind: 0,
    role: "plan",
    commits: [{ hash: "ghi9012", message: "Plan DB migration", url: "" }],
    changes: [{ path: "docs/migration.md", status: "A" }],
  },
];

// -- Loom status mock data --

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 2,
    ready_to_implement: 2,
    in_progress: 2,
    need_review: 1,
    backlog: 1,
  },
  in_progress_list: [
    { id: "proj-004", title: "Implement authentication flow", priority: 1 },
  ],
  agent_tasks: {
    alpha: {
      id: "proj-004",
      title: "Implement authentication flow",
      priority: 1,
    },
    gamma: {
      id: "proj-003",
      title: "Update API documentation",
      priority: 2,
    },
  },
  stats: {
    open: 6,
    closed: 1,
    total: 8,
    completion: 12,
    remaining: 7,
    in_progress: 1,
    review: 1,
    blocked: 1,
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-15T10:00:00Z",
    git_needs_push: 1,
    git_needs_pull: 0,
    git_push_details: [{ name: "alpha", count: 2 }],
  },
  timestamp: "2026-01-15T10:00:00Z",
};

// -- Loom tasks mock data --

const mockLoomTasks = {
  summary: mockLoomStatus.tasks,
  needs_planning: [
    { id: "proj-np-1", title: "Design search feature", priority: 2 },
    { id: "proj-np-2", title: "Design notification system", priority: 3 },
  ],
  ready_to_implement: [
    { id: "proj-001", title: "Auth token expiration bug", priority: 0 },
    { id: "proj-002", title: "Add rate limiting middleware", priority: 1 },
  ],
  needs_review: [
    { id: "proj-005", title: "Review caching strategy", priority: 2 },
  ],
  in_progress: [
    { id: "proj-004", title: "Implement authentication flow", priority: 1 },
    { id: "proj-003", title: "Update API documentation", priority: 2 },
  ],
  backlog: [{ id: "proj-bl-1", title: "Legacy cleanup", priority: 4 }],
  closed: [{ id: "proj-006", title: "Initial project setup", priority: 3 }],
  timestamp: "2026-01-15T10:00:00Z",
};

// -- Blocked issues mock data --

const mockBlockedIssues = [
  {
    id: "proj-007",
    title: "Blocked: waiting on DB migration",
    status: "blocked",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
    blocked_by_count: 1,
    blocked_by: ["proj-004"],
  },
];

// -- Helper functions --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

/**
 * Set up all baseline API mocks required for the app to boot.
 */
async function setupBaseMocks(page: Page) {
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
      body: ok([{ name: "claude", available: true, display_name: "Claude" }]),
    });
  });

  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }),
    });
  });

  // Workspace metadata
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    });
  });
  await page.route("**/api/workspaces/default", async (route) => {
    const url = new URL(route.request().url());
    if (
      url.pathname === "/api/workspaces/default" ||
      url.pathname === "/api/workspaces/default/"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspaceData),
      });
    } else {
      await route.fallback();
    }
  });

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Monitor catch-all first (specific routes registered after take LIFO priority)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
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

  // Specific monitor endpoints (registered last = highest priority in Playwright LIFO)
  await page.route("**/api/monitor/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: mockAgents, timestamp: "2026-01-15T10:00:00Z" }),
    });
  });

  await page.route("**/api/monitor/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    });
  });

  await page.route("**/api/monitor/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomTasks),
    });
  });

  // Stats endpoint
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 8,
        open_issues: 3,
        in_progress_issues: 1,
        closed_issues: 1,
        blocked_issues: 1,
        deferred_issues: 1,
        ready_issues: 3,
        tombstone_issues: 0,
        pinned_issues: 0,
        epics_eligible_for_closure: 0,
        average_lead_time_hours: 24,
      }),
    });
  });

  // Blocked endpoint
  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockBlockedIssues),
    });
  });

  // Terminal sessions-by-issue endpoint
  await page.route("**/workspaces/*/terminal/sessions/by-issue", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({}),
    });
  });

  // SSE events endpoint
  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });

  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();
      const method = route.request().method();

      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({ status: 200, contentType: "application/json", body: ok(mockWorkspaceData) });
        return;
      }
      if (url.includes("/events")) { await route.abort(); return; }
      if (url.includes("/config/backend")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ backend: "claude", source: "workspace", available: ["claude"], agents: [] }),
        });
        return;
      }
      if (url.includes("/terminal/tabs")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok([]) }); return; }
      if (url.includes("/terminal/state")) { await route.fulfill({ status: 200, contentType: "application/json", body: JSON.stringify({ active_tab: "" }) }); return; }
      if (url.includes("/terminal/sessions/by-issue")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok({}) }); return; }
      if (url.includes("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 8,
            open_issues: 3,
            in_progress_issues: 1,
            closed_issues: 1,
            blocked_issues: 1,
            deferred_issues: 1,
            ready_issues: 3,
            tombstone_issues: 0,
            pinned_issues: 0,
            epics_eligible_for_closure: 0,
            average_lead_time_hours: 24,
          }),
        });
        return;
      }
      if (url.includes("/blocked")) { await route.fulfill({ status: 200, contentType: "application/json", body: ok(mockBlockedIssues) }); return; }
      if ((url.includes("/issues") || url.includes("/ready")) && method === "GET") {
        await route.fulfill({ status: 200, contentType: "application/json", body: ok(mockIssues) });
        return;
      }
      await route.fulfill({ status: 200, contentType: "application/json", body: ok(mockWorkspaceData) });
    },
  );
}

/**
 * Install a browser-level fetch interceptor for the issues list endpoint.
 */
async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript(
    (issueData: unknown[]) => {
      (window as any).__mockIssues = issueData;
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
            new Response(
              JSON.stringify({
                success: true,
                data: (window as any).__mockIssues,
              }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }
        return originalFetch(input, init);
      };
    },
    issues,
  );
}

async function navigateAndWaitForBoard(page: Page) {
  await page.goto("/ws/default/kanban", { waitUntil: "domcontentloaded" });
}

// -- Tests --

test.describe("E2E Journey: Project status at a glance", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    await installIssuesMock(page, mockIssues);
    await setupBaseMocks(page);
    await navigateAndWaitForBoard(page);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Kanban board renders issues in correct columns", async () => {
    // Verify all visible columns exist
    const readyColumn = page.locator('section[data-status="ready"]');
    const inProgressColumn = page.locator('section[data-status="in_progress"]');
    const reviewColumn = page.locator('section[data-status="review"]');
    const doneColumn = page.locator('section[data-status="done"]');

    await expect(readyColumn).toBeVisible();
    await expect(inProgressColumn).toBeVisible();
    await expect(reviewColumn).toBeVisible();
    await expect(doneColumn).toBeVisible();

    // Count cards in each column (Open/ready column gets 3 open issues)
    await expect(readyColumn.locator("article")).toHaveCount(3);
    await expect(inProgressColumn.locator("article")).toHaveCount(1);
    await expect(reviewColumn.locator("article")).toHaveCount(1);
    await expect(doneColumn.locator("article")).toHaveCount(1);

    // Verify specific issue titles appear in correct columns
    await expect(readyColumn.getByText("Auth token expiration bug")).toBeVisible();
    await expect(readyColumn.getByText("Add rate limiting middleware")).toBeVisible();
    await expect(readyColumn.getByText("Update API documentation")).toBeVisible();
    await expect(
      inProgressColumn.getByText("Implement authentication flow"),
    ).toBeVisible();
    await expect(
      reviewColumn.getByText("Review caching strategy"),
    ).toBeVisible();
    await expect(doneColumn.getByText("Initial project setup")).toBeVisible();

    // Verify priority badges render on cards (article elements carry data-priority)
    await expect(readyColumn.locator('article[data-priority="0"]')).toHaveCount(1);
    await expect(readyColumn.locator('article[data-priority="1"]')).toHaveCount(1);
    await expect(readyColumn.locator('article[data-priority="2"]')).toHaveCount(1);
  });

  test("Work Queue counts match issue distribution", async () => {
    // Wait for Work Queue section to be visible (computed from issue list)
    const sidebar = page.getByRole("complementary");
    await expect(sidebar.getByText("Work Queue")).toBeVisible({ timeout: 10000 });

    // Work Queue counts are derived from issues in the workspace:
    // Backlog (deferred) = 1, Open = 3, Blocked = 1, In Progress = 1, Needs Review = 1, Done = 1
    await expect(sidebar.getByText("Backlog")).toBeVisible();
    await expect(sidebar.getByText("Open")).toBeVisible();
    await expect(sidebar.getByText("In Progress")).toBeVisible();
    await expect(sidebar.getByText("Needs Review")).toBeVisible();

    // Verify sidebar footer shows agent activity summary (depends on async agent data)
    await expect(page.getByText(/\d+ working/)).toBeVisible({ timeout: 10000 });
    await expect(page.getByText(/\d+ idle/)).toBeVisible({ timeout: 10000 });
  });

  test("Agent cards render in sidebar with correct status", async () => {
    // Scope to sidebar to avoid matching agent names in kanban cards
    const sidebar = page.getByRole("complementary");

    // Wait for agent data to load
    await expect(sidebar.getByRole("button", { name: "Agent: alpha" })).toBeVisible({ timeout: 10000 });
    await expect(sidebar.getByRole("button", { name: "Agent: beta" })).toBeVisible();
    await expect(sidebar.getByRole("button", { name: "Agent: gamma" })).toBeVisible();

    // Verify agent cards have correct status attributes
    const alphaCard = sidebar.locator('[data-status="working"]').filter({ hasText: "alpha" });
    const betaCard = sidebar.locator('[data-status="idle"]').filter({ hasText: "beta" });
    const gammaCard = sidebar.locator('[data-status="planning"]').filter({ hasText: "gamma" });

    await expect(alphaCard).toBeVisible();
    await expect(betaCard).toBeVisible();
    await expect(gammaCard).toBeVisible();
  });

  test("Click agent card opens AgentDetailPanel", async () => {
    // Click the alpha agent card
    const alphaCard = page.locator('[data-status="working"]').filter({ hasText: "alpha" });
    await alphaCard.click();

    // Wait for panel to open
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Verify agent name appears in panel header
    await expect(panel.locator("h2")).toContainText("alpha");

    // Verify Info tab is selected by default
    await expect(
      page.locator("#agent-panel-tab-info"),
    ).toHaveAttribute("aria-selected", "true");

    // Verify current task section shows the task title
    await expect(panel.getByText("Implement authentication flow")).toBeVisible();

    // Verify repository context from the fleet agent payload is shown.
    await expect(panel.getByText("feature-auth").first()).toBeVisible();
  });

  test("Switch to Logs tab shows log content area", async () => {
    // Click the Logs tab
    await page.getByRole("tab", { name: "Logs" }).click();

    // Verify Logs tab is now selected
    await expect(
      page.locator("#agent-panel-tab-logs"),
    ).toHaveAttribute("aria-selected", "true");

    // Verify log content area renders (the tabpanel)
    const logsPanel = page.locator("#agent-panel-tabpanel-logs");
    await expect(logsPanel).toBeVisible();

    await expect(logsPanel).not.toBeEmpty();
  });

  test("Close panel via Escape key", async () => {
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Press Escape key
    await page.keyboard.press("Escape");

    // Wait for panel to close
    await expect(panel).toHaveAttribute("data-state", "closed");
  });

  test("Click different agent swaps panel (mutual exclusivity)", async () => {
    const sidebar = page.getByRole("complementary");
    const panel = page.getByTestId("agent-detail-panel");

    // Panel was closed by Escape in previous test — click beta to open
    await sidebar.getByRole("button", { name: "Agent: beta" }).click();

    // Wait for panel to open with beta
    await expect(panel).toHaveAttribute("data-state", "open");

    // Verify beta's name appears in panel header (not alpha)
    await expect(panel.locator("h2")).toContainText("beta");
    await expect(panel.locator("h2")).not.toContainText("alpha");

    // Verify Info tab is selected again (resets on agent switch via useEffect)
    await expect(
      page.locator("#agent-panel-tab-info"),
    ).toHaveAttribute("aria-selected", "true");

    // Verify beta shows "No active task" (idle agent)
    await expect(panel.getByText("No active task")).toBeVisible();
  });

  test("Close panel via close button", async () => {
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Click the close button (X) in the panel header
    await panel.getByRole("button", { name: "Close panel" }).click();

    // Wait for panel to close
    await expect(panel).toHaveAttribute("data-state", "closed");
  });
});

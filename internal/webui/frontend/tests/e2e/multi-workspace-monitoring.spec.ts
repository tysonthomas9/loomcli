import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Multi-workspace monitoring and agent health.
 *
 * Tests workspace navigation, data isolation, WorkspaceSwitcher (Ctrl+K),
 * Monitor dashboard, and Observability dashboard across two workspaces.
 */

// -- Workspace definitions --

const WS_A = {
  id: "ws-alpha-001",
  name: "Frontend",
  path: "/repos/frontend",
  active: true,
  repo_count: 2,
  is_default: true,
};

const WS_B = {
  id: "ws-beta-002",
  name: "Backend",
  path: "/repos/backend",
  active: false,
  repo_count: 1,
  is_default: false,
};

const workspaceDataForA = {
  id: WS_A.id,
  name: WS_A.name,
  path: WS_A.path,
  repos: [
    {
      name: "web-app",
      path: "/repos/frontend/web-app",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
    {
      name: "design-system",
      path: "/repos/frontend/design-system",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [
    { name: "alpha", repos: ["web-app"], repo_groups: [], cross_repo: false },
    {
      name: "beta",
      repos: ["design-system"],
      repo_groups: [],
      cross_repo: false,
    },
  ],
  workspaces: [WS_A, WS_B],
  workspace_order: [WS_A.id, WS_B.id],
  default_workspace: WS_A.name,
};

const workspaceDataForB = {
  id: WS_B.id,
  name: WS_B.name,
  path: WS_B.path,
  repos: [
    {
      name: "api-server",
      path: "/repos/backend/api-server",
      default_branch: "main",
      remote: "origin",
      groups: [],
    },
  ],
  groups: [],
  agents: [
    {
      name: "gamma",
      repos: ["api-server"],
      repo_groups: [],
      cross_repo: false,
    },
  ],
  workspaces: [WS_A, WS_B],
  workspace_order: [WS_A.id, WS_B.id],
  default_workspace: WS_A.name,
};

// -- Per-workspace issue data --

const issuesA = [
  {
    id: "ws-a-001",
    title: "WS-A: Build login page",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "ws-a-002",
    title: "WS-A: Fix nav bug",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    created_at: "2026-01-15T11:00:00Z",
    updated_at: "2026-01-15T11:00:00Z",
  },
];

const issuesB = [
  {
    id: "ws-b-001",
    title: "WS-B: Add REST endpoint",
    status: "open",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "ws-b-002",
    title: "WS-B: DB migration",
    status: "review",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-15T11:00:00Z",
    updated_at: "2026-01-15T11:00:00Z",
  },
];

const issuesByWorkspace = {
  [WS_A.id]: issuesA,
  [WS_B.id]: issuesB,
};

// -- Per-workspace loom status --

const loomStatusA = {
  agents: [
    {
      name: "alpha",
      status: "working",
      branch: "feat/login",
      task: "loom-101",
      ahead: 1,
      behind: 0,
      last_seen: "2026-01-15T12:00:00Z",
    },
    {
      name: "beta",
      status: "idle",
      branch: "main",
      task: "",
      ahead: 0,
      behind: 0,
      last_seen: "2026-01-15T11:30:00Z",
    },
  ],
  tasks: {
    needs_planning: 1,
    ready_to_implement: 2,
    in_progress: 1,
    need_review: 0,
    blocked: 0,
  },
  agent_tasks: {
    alpha: { id: "loom-101", title: "WS-A: Build login page", priority: 2 },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-15T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: { open: 5, closed: 3, total: 8, completion: 37 },
  timestamp: "2026-01-15T12:00:00Z",
};

const loomStatusB = {
  agents: [
    {
      name: "gamma",
      status: "working",
      branch: "feat/api",
      task: "loom-201",
      ahead: 2,
      behind: 0,
      last_seen: "2026-01-15T12:00:00Z",
    },
  ],
  tasks: {
    needs_planning: 0,
    ready_to_implement: 1,
    in_progress: 1,
    need_review: 1,
    blocked: 0,
  },
  agent_tasks: {
    gamma: { id: "loom-201", title: "WS-B: Add REST endpoint", priority: 1 },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-15T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: { open: 3, closed: 7, total: 10, completion: 70 },
  timestamp: "2026-01-15T12:00:00Z",
};

// -- Loom tasks per workspace --

const loomTasksA = {
  needs_planning: [{ id: "loom-010", title: "Plan feature", priority: 2 }],
  ready_to_implement: [
    { id: "loom-020", title: "Implement login", priority: 1 },
    { id: "loom-021", title: "Add tests", priority: 2 },
  ],
  in_progress: [{ id: "loom-101", title: "WS-A: Build login page", priority: 2 }],
  needs_review: [],
  blocked: [],
};

const loomTasksB = {
  needs_planning: [],
  ready_to_implement: [{ id: "loom-030", title: "Add endpoint", priority: 1 }],
  in_progress: [
    { id: "loom-201", title: "WS-B: Add REST endpoint", priority: 1 },
  ],
  needs_review: [{ id: "loom-031", title: "Review migration", priority: 2 }],
  blocked: [],
};

// -- Observability metrics --

const mockMetrics = {
  success: true,
  data: {
    timestamp: "2026-01-15T12:00:00Z",
    tasks_completed_last_hour: 3,
    tasks_completed_24h: 12,
    avg_task_duration_sec: 1800,
    lines_changed_last_hour: 450,
    error_rate_pct: 2.5,
    restart_count_24h: 1,
    restarts_by_agent: { beta: 1 },
    agent_utilization: { alpha: 0.85, beta: 0.1, gamma: 0.72 },
    tasks_by_role: { task: 8, review: 2 },
    tasks_by_epic: { "Login Epic": 2, "API Epic": 6 },
    tasks_by_agent: { alpha: 5, beta: 1, gamma: 4 },
    hourly_completions: Array.from({ length: 24 }, (_, i) => ({
      hour: `${String(i).padStart(2, "0")}:00`,
      completed: Math.floor(Math.random() * 5),
      failed: 0,
      avg_duration: 1800,
    })),
    total_tasks_completed: 30,
    total_tasks_failed: 2,
    total_restarts: 1,
  },
};

// -- Helpers --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

/**
 * Set up all API mocks for multi-workspace tests.
 * Returns a `setActiveWorkspace` function to control which workspace's loom data is served.
 *
 * Uses a single catch-all route per API namespace (workspace, loom) to avoid
 * LIFO route ordering issues in Playwright.
 */
async function setupMocks(
  page: Page,
  issuesByWs: Record<string, typeof issuesA> = {},
): Promise<{ setActiveWorkspace: (id: string) => void }> {
  let currentWsId = WS_A.id;
  const setActiveWorkspace = (id: string) => {
    currentWsId = id;
  };

  const wsDataMap: Record<string, typeof workspaceDataForA> = {
    [WS_A.id]: workspaceDataForA,
    [WS_B.id]: workspaceDataForB,
  };

  // Consolidated workspace API handler: /api/workspaces/**
  await page.route("**/api/workspaces/**", async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;

    // /api/workspaces/active
    if (pathname === "/api/workspaces/active") {
      const data = wsDataMap[currentWsId] ?? workspaceDataForA;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(data),
      });
      return;
    }

    // /api/workspaces/{id} (exact, not sub-path)
    for (const [wsId, wsData] of Object.entries(wsDataMap)) {
      if (
        pathname === `/api/workspaces/${wsId}` ||
        pathname === `/api/workspaces/${wsId}/`
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(wsData),
        });
        return;
      }
    }

    const workspaceIssueListMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)\/issues$/,
    );
    if (workspaceIssueListMatch) {
      const issues = issuesByWs[workspaceIssueListMatch[1]] ?? [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(issues),
      });
      return;
    }

    const workspaceReadyMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)\/ready$/,
    );
    if (workspaceReadyMatch) {
      const issues = issuesByWs[workspaceReadyMatch[1]] ?? [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(issues.filter((issue) => issue.status === "open")),
      });
      return;
    }

    const workspaceGraphMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)\/issues\/graph$/,
    );
    if (workspaceGraphMatch) {
      const issues = issuesByWs[workspaceGraphMatch[1]] ?? [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(issues),
      });
      return;
    }

    const workspaceIssueDetailMatch = pathname.match(
      /^\/api\/workspaces\/([^/]+)\/issues\/([^/]+)$/,
    );
    if (workspaceIssueDetailMatch) {
      const [, wsId, issueId] = workspaceIssueDetailMatch;
      const issue = (issuesByWs[wsId] ?? []).find((item) => item.id === issueId);
      await route.fulfill({
        status: issue ? 200 : 404,
        contentType: "application/json",
        body: issue
          ? ok(issue)
          : JSON.stringify({ success: false, error: "Issue not found" }),
      });
      return;
    }

    // /api/workspaces/{id}/stats
    if (pathname.match(/\/api\/workspaces\/[^/]+\/stats$/)) {
      const wsId = pathname.split("/")[3];
      const issues = issuesByWs[wsId] ?? [];
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: issues.length,
          open_issues: issues.filter((issue) => issue.status === "open").length,
          in_progress_issues: issues.filter(
            (issue) => issue.status === "in_progress",
          ).length,
          closed_issues: issues.filter((issue) => issue.status === "closed")
            .length,
          blocked_issues: 0,
          deferred_issues: 0,
          ready_issues: issues.filter((issue) => issue.status === "open")
            .length,
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
        headers: {
          "Cache-Control": "no-cache",
          Connection: "keep-alive",
        },
        body: 'event: connected\ndata: {"message":"connected"}\n\n',
      });
      return;
    }

    // Default: 404 for unhandled workspace sub-paths (prevents null-poisoning)
    await route.fulfill({
      status: 404,
      contentType: "application/json",
      body: JSON.stringify({ error: "not found" }),
    });
  });

  // Health check
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
          display_name: "Shell",
          available: true,
        },
      ]),
    });
  });

  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Global backend config
  await page.route("**/api/workspaces/*/config/backend", async (route) => {
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

  // Consolidated monitor API handler: /api/monitor/**
  await page.route("**/api/monitor/**", async (route) => {
    const url = route.request().url();

    if (url.includes("/api/monitor/status")) {
      const status = currentWsId === WS_A.id ? loomStatusA : loomStatusB;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(status),
      });
    } else if (url.includes("/api/monitor/tasks")) {
      const tasks = currentWsId === WS_A.id ? loomTasksA : loomTasksB;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(tasks),
      });
    } else if (url.includes("/api/monitor/agents")) {
      const ws =
        currentWsId === WS_A.id ? workspaceDataForA : workspaceDataForB;
      const agents = ws.agents.map((a) => ({
        name: a.name,
        status: "ready",
        branch: "main",
        ahead: 0,
        behind: 0,
        role: "task",
        last_active: "2026-01-15T10:00:00Z",
      }));
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ agents }),
      });
    } else if (url.includes("/api/monitor/usage")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_input_tokens: 0,
          total_output_tokens: 0,
          total_cache_read_tokens: 0,
          total_cache_write_tokens: 0,
          total_cost: 0,
          session_count: 0,
          by_agent: [],
          by_backend: [],
          daily_costs: [],
          sessions: [],
          timestamp: "2026-01-15T12:00:00Z",
        }),
      });
    } else if (url.includes("/health")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ status: "ok" }),
      });
    } else {
      // Default: return empty object for unhandled loom endpoints
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({}),
      });
    }
  });

  await page.route("**/api/observability/metrics", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockMetrics),
    });
  });

  return { setActiveWorkspace };
}

test.describe("Multi-Workspace Monitoring Journey", () => {
  test.describe("Workspace Navigation", () => {
    test("workspace A loads with correct header and issues", async ({
      page,
    }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // Header shows workspace name
      await expect(
        page.getByText("Frontend", { exact: true }).first(),
      ).toBeVisible({
        timeout: 10000,
      });

      // WS_A issues render in kanban
      await expect(page.getByText("WS-A: Build login page")).toBeVisible({
        timeout: 10000,
      });
      await expect(page.getByText("WS-A: Fix nav bug")).toBeVisible();
    });

    test("navigating to workspace B via URL shows workspace B data", async ({
      page,
    }) => {
      const { setActiveWorkspace } = await setupMocks(page, issuesByWorkspace);

      // Navigate directly to workspace B
      setActiveWorkspace(WS_B.id);
      await page.goto(`/ws/${WS_B.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // WS_B issues should appear
      await expect(page.getByText("WS-B: Add REST endpoint")).toBeVisible({
        timeout: 10000,
      });
      await expect(page.getByText("WS-B: DB migration")).toBeVisible();

      // WS_A issues should NOT appear
      await expect(page.getByText("WS-A: Build login page")).not.toBeVisible();
    });
  });

  test.describe("WorkspaceSwitcher (Ctrl+K)", () => {
    test("Ctrl+K opens switcher, search filters, Enter switches workspace", async ({
      page,
    }) => {
      const { setActiveWorkspace } = await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // Wait for board to load and workspace data to resolve
      await expect(page.getByText("WS-A: Build login page")).toBeVisible({
        timeout: 10000,
      });
      await expect(
        page.getByRole("button", { name: /Active workspace: Frontend/ }),
      ).toBeVisible({ timeout: 10000 });
      // Let React re-render with workspace data
      await page.waitForTimeout(500);

      // Click the main content area to ensure focus isn't trapped in search input
      await page.locator("main").click();

      // Open workspace switcher with Ctrl+K
      await page.keyboard.press("Control+k");

      // The switcher dialog should be visible
      const dialog = page.getByRole("dialog", { name: "Switch workspace" });
      await expect(dialog).toBeVisible({ timeout: 5000 });

      // Both workspaces should be listed
      const wsItems = dialog.locator("[data-workspace-item]");
      await expect(wsItems).toHaveCount(2);

      // Type "Back" to filter — scope to dialog to avoid ambiguity with sidebar search
      await dialog.getByTestId("search-input-field").fill("Back");

      // Only Backend should remain
      await expect(wsItems).toHaveCount(1);
      await expect(dialog.getByText("Backend").first()).toBeVisible();

      // Press Enter to select
      setActiveWorkspace(WS_B.id);
      await page.keyboard.press("Enter");

      // Dialog should close and URL should update
      await expect(dialog).not.toBeVisible();
      await page.waitForURL(`**/ws/${WS_B.id}/**`, { timeout: 10000 });
    });
  });

  test.describe("Data Isolation", () => {
    test("switching workspace via URL clears previous workspace data", async ({
      page,
    }) => {
      const { setActiveWorkspace } = await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // Confirm WS_A data loaded
      await expect(page.getByText("WS-A: Build login page")).toBeVisible({
        timeout: 10000,
      });
      await expect(page.getByText("WS-A: Fix nav bug")).toBeVisible();

      // Switch to WS_B via URL navigation
      setActiveWorkspace(WS_B.id);
      await page.goto(`/ws/${WS_B.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // WS_B data present
      await expect(page.getByText("WS-B: Add REST endpoint")).toBeVisible({
        timeout: 10000,
      });
      await expect(page.getByText("WS-B: DB migration")).toBeVisible();

      // WS_A data gone
      await expect(page.getByText("WS-A: Build login page")).not.toBeVisible();
      await expect(page.getByText("WS-A: Fix nav bug")).not.toBeVisible();
    });
  });

  test.describe("Monitor View", () => {
    test("Monitor view renders dashboard with agent data", async ({ page }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/monitor`, {
        waitUntil: "domcontentloaded",
      });

      // Dashboard should be visible
      const dashboard = page.getByTestId("monitor-dashboard");
      await expect(dashboard).toBeVisible({ timeout: 15000 });

      // Agent Activity panel should show agents
      const agentPanel = page.getByTestId("agent-activity-panel");
      await expect(agentPanel).toBeVisible();

      // Wait for loom status to load
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 },
      );
      await page.waitForTimeout(500);

      // Summary should show active and idle labels
      await expect(agentPanel.getByText("active", { exact: true })).toBeVisible(
        { timeout: 10000 },
      );
      await expect(agentPanel.getByText("idle", { exact: true })).toBeVisible();
    });

    test("clicking agent opens AgentDetailPanel", async ({ page }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/monitor`, {
        waitUntil: "domcontentloaded",
      });

      // Wait for monitor dashboard
      const dashboard = page.getByTestId("monitor-dashboard");
      await expect(dashboard).toBeVisible({ timeout: 15000 });

      // Wait for loom data
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 },
      );
      await page.waitForTimeout(500);

      // Click the "alpha" agent in the activity panel
      const agentPanel = page.getByTestId("agent-activity-panel");
      await expect(agentPanel).toBeVisible();
      const alphaCard = agentPanel.getByText("alpha").first();
      await alphaCard.click();

      // AgentDetailPanel should open
      const detailPanel = page.getByTestId("agent-detail-panel");
      await expect(detailPanel).toBeVisible({ timeout: 5000 });
    });
  });

  test.describe("Observability View", () => {
    test("Observability view renders dashboard with metric panels", async ({
      page,
    }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/observability`, {
        waitUntil: "domcontentloaded",
      });

      // Wait for metrics to load
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/observability/metrics") &&
          res.status() === 200,
        { timeout: 15000 },
      );
      await page.waitForTimeout(500);

      // Verify panel sections render (sections use aria-label)
      await expect(
        page.getByRole("region", { name: "Task Timeline" }),
      ).toBeVisible({ timeout: 10000 });
      await expect(
        page.getByRole("region", { name: "Agent Utilization" }),
      ).toBeVisible();
      await expect(
        page.getByRole("region", { name: "Errors & Restarts" }),
      ).toBeVisible();
      await expect(
        page.getByRole("region", { name: "Epic Progress" }),
      ).toBeVisible();

      // Verify headings
      await expect(
        page.getByRole("heading", { name: "Hourly Completions (24h)" }),
      ).toBeVisible();
      await expect(
        page.getByRole("heading", { name: "Epic Progress" }),
      ).toBeVisible();
    });
  });

  test.describe("Screenshots", () => {
    test("captures workspace switcher screenshot", async ({ page }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/kanban`, {
        waitUntil: "domcontentloaded",
      });

      // Wait for board and workspace data
      await expect(page.getByText("WS-A: Build login page")).toBeVisible({
        timeout: 10000,
      });
      await expect(
        page.getByRole("button", { name: /Active workspace: Frontend/ }),
      ).toBeVisible({ timeout: 10000 });
      await page.waitForTimeout(500);
      await page.locator("main").click();

      // Open workspace switcher
      await page.keyboard.press("Control+k");
      const dialog = page.getByRole("dialog", { name: "Switch workspace" });
      await expect(dialog).toBeVisible({ timeout: 5000 });
      await page.screenshot({
        path: "tests/e2e/screenshots/workspace-switcher-modal.png",
      });
    });

    test("captures monitor dashboard screenshot", async ({ page }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/monitor`, {
        waitUntil: "domcontentloaded",
      });

      const dashboard = page.getByTestId("monitor-dashboard");
      await expect(dashboard).toBeVisible({ timeout: 15000 });
      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/monitor/status") && res.status() === 200,
        { timeout: 10000 },
      );
      await page.waitForTimeout(500);
      await page.screenshot({
        path: "tests/e2e/screenshots/monitor-dashboard.png",
      });
    });

    test("captures observability dashboard screenshot", async ({ page }) => {
      await setupMocks(page, issuesByWorkspace);
      await page.goto(`/ws/${WS_A.id}/observability`, {
        waitUntil: "domcontentloaded",
      });

      await page.waitForResponse(
        (res) =>
          res.url().includes("/api/observability/metrics") &&
          res.status() === 200,
        { timeout: 15000 },
      );
      await page.waitForTimeout(500);
      await page.screenshot({
        path: "tests/e2e/screenshots/observability-dashboard.png",
      });
    });
  });
});

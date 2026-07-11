/**
 * E2E: Agents sidebar rendered through WorkspaceTree.
 *
 * Tests agent rendering, card details, work queue, sidebar collapse/expand,
 * and connection states. All agent data flows through the production sidebar
 * (WorkspaceTree → AgentCard / WorkQueueSection), NOT the standalone
 * AgentsSidebar component.
 *
 * Mocks: /api/config, /api/workspaces/active, workspace-scoped sub-routes,
 * /api/health, /api/auth/token, and all 3 loom endpoints
 * (/api/monitor/agents, /api/monitor/status, /api/monitor/tasks)
 * with both proxy and direct URL patterns.
 */

import { test, expect, type Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WORKSPACE_ID = "default";
const BASE_PATH = `/ws/${WORKSPACE_ID}/`;
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

function createWorkspaceData(
  agents: Array<{
    name: string;
    repos: string[];
    repo_groups: string[];
    cross_repo: boolean;
  }> = [
    { name: "nova", repos: ["loomcli"], repo_groups: [], cross_repo: false },
    { name: "falcon", repos: ["loomcli"], repo_groups: [], cross_repo: false },
  ],
) {
  return {
    id: WORKSPACE_ID,
    name: "default",
    path: "/tmp/test-ws",
    repos: [
      {
        name: "loomcli",
        path: "/repos/loomcli",
        default_branch: "main",
        remote: "origin",
        groups: [],
      },
    ],
    groups: [],
    agents,
    workspaces: [
      {
        id: WORKSPACE_ID,
        name: "default",
        path: "/tmp/test-ws",
        active: true,
        repo_count: 1,
        is_default: true,
      },
    ],
    workspace_order: ["default"],
    default_workspace: "default",
  };
}

const mockAgents = [
  {
    name: "nova",
    branch: "feature-auth",
    status: "working: loom-101 (5m)",
    ahead: 2,
    behind: 0,
    role: "task",
    workspace: "",
    repo: "loomcli",
  },
  {
    name: "falcon",
    branch: "main",
    status: "ready",
    ahead: 0,
    behind: 1,
    role: "plan",
    workspace: "",
    repo: "loomcli",
  },
];

const mockLoomStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 3,
    ready_to_implement: 5,
    in_progress: 2,
    need_review: 1,
    backlog: 0,
  },
  in_progress_list: null,
  agent_tasks: {
    nova: {
      id: "loom-101",
      title: "Implement auth flow",
      priority: 1,
      status: "in_progress",
    },
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-24T12:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
  },
  stats: {
    open: 8,
    closed: 12,
    total: 20,
    completion: 60,
    remaining: 8,
    in_progress: 2,
    review: 1,
    blocked: 0,
  },
  timestamp: "2026-01-24T12:00:00Z",
};

const mockLoomTasks = {
  summary: {
    needs_planning: 3,
    ready_to_implement: 5,
    in_progress: 2,
    need_review: 1,
    backlog: 0,
  },
  needs_planning: [{ id: "loom-010", title: "Plan auth", priority: 1 }],
  ready_to_implement: [
    { id: "loom-020", title: "Build login page", priority: 1 },
  ],
  in_progress: [{ id: "loom-101", title: "Implement auth flow", priority: 1 }],
  needs_review: [{ id: "loom-030", title: "Review PR #42", priority: 2 }],
  backlog: [],
  closed: [],
  timestamp: "2026-01-24T12:00:00Z",
};

const mockIssues = [
  {
    id: "test-1",
    title: "Feature A",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "test-2",
    title: "In Progress Task",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "test-3",
    title: "Blocked Task",
    status: "blocked",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

interface SetupOptions {
  loomServerAvailable?: boolean;
  emptyAgents?: boolean;
  workspaceError?: boolean;
  agents?: typeof mockAgents;
  /** Override workspace config agents (defaults to nova+falcon). Set to [] to remove all config agents. */
  workspaceAgents?: Array<{
    name: string;
    repos: string[];
    repo_groups: string[];
    cross_repo: boolean;
  }>;
}

async function setupMocks(
  page: Page,
  options: SetupOptions = {},
): Promise<void> {
  const {
    loomServerAvailable = true,
    emptyAgents = false,
    workspaceError = false,
    agents: customAgents,
    workspaceAgents,
  } = options;

  const activeAgents = emptyAgents ? [] : (customAgents ?? mockAgents);
  const wsData = createWorkspaceData(workspaceAgents);

  // Neutralize AbortController signals (React StrictMode double-fetch workaround)
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

  // App config (boot-time auth mode discovery — must be mocked first)
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

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
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

  // Workspace-scoped routes — single handler with internal dispatch
  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();

      // Workspace resolution: /api/workspaces/active
      if (url.includes("/api/workspaces/active")) {
        if (workspaceError) {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({
              success: false,
              error: "Internal Server Error",
            }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(wsData),
        });
        return;
      }

      // SSE events — abort to prevent hang
      if (url.includes(WS_API + "/events")) {
        await route.abort();
        return;
      }

      // Workspace data: /api/workspaces/default (exact)
      const afterWs = url.split(WS_API)[1] || "";
      if (
        afterWs === "" ||
        afterWs === "/" ||
        afterWs.startsWith("?") ||
        afterWs.startsWith("/?")
      ) {
        if (workspaceError) {
          await route.fulfill({
            status: 500,
            contentType: "application/json",
            body: JSON.stringify({
              success: false,
              error: "Internal Server Error",
            }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(wsData),
        });
        return;
      }

      // Ready (issues)
      if (afterWs.startsWith("/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(mockIssues),
        });
        return;
      }

      // Stats
      if (afterWs.startsWith("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 3,
            open_issues: 1,
            in_progress_issues: 1,
            closed_issues: 0,
            blocked_issues: 1,
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

      // Blocked
      if (afterWs.startsWith("/blocked")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok([]),
        });
        return;
      }

      // Terminal sessions
      if (afterWs.includes("/terminal/sessions")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({}),
        });
        return;
      }

      // Issues graph
      if (afterWs.startsWith("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, issues: [] }),
        });
        return;
      }

      // Issues (list/kanban)
      if (afterWs.startsWith("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(mockIssues),
        });
        return;
      }

      // Fallback for other workspace-scoped routes
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  // Consolidated monitor API handler
  await page.route("**/api/monitor/**", async (route) => {
    const url = route.request().url();

    if (!loomServerAvailable) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      });
      return;
    }

    if (url.includes("/api/monitor/agents")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          agents: activeAgents,
          timestamp: "2026-01-24T12:00:00Z",
        }),
      });
    } else if (url.includes("/api/monitor/status")) {
      const status = emptyAgents
        ? { ...mockLoomStatus, agents: [], agent_tasks: {} }
        : customAgents
          ? { ...mockLoomStatus, agents: customAgents }
          : mockLoomStatus;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(status),
      });
    } else if (url.includes("/api/monitor/tasks")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockLoomTasks),
      });
    } else if (url.includes("/health")) {
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

  // Direct loom server routes (dual URL pattern)
  await page.route("**/localhost:9000/api/agents", async (route) => {
    if (!loomServerAvailable) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: activeAgents,
        timestamp: "2026-01-24T12:00:00Z",
      }),
    });
  });

  await page.route("**/localhost:9000/api/status", async (route) => {
    if (!loomServerAvailable) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      });
      return;
    }
    const status = emptyAgents
      ? { ...mockLoomStatus, agents: [], agent_tasks: {} }
      : customAgents
        ? { ...mockLoomStatus, agents: customAgents }
        : mockLoomStatus;
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(status),
    });
  });

  await page.route("**/localhost:9000/api/tasks", async (route) => {
    if (!loomServerAvailable) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: "invalid json{",
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomTasks),
    });
  });
}

/** Locator for the sidebar (WorkspaceTree aside). Uses data-collapsed to distinguish from panel asides. */
function sidebar(page: Page) {
  return page.locator("aside[data-collapsed]");
}

async function navigateAndWait(page: Page): Promise<void> {
  await page.goto(BASE_PATH, { waitUntil: "domcontentloaded" });
  // Wait for sidebar to render (workspace data loaded)
  await expect(sidebar(page)).toBeVisible({ timeout: 10000 });
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

test.describe("Agents Sidebar", () => {
  // ------- agent rendering -------

  test.describe("agent rendering", () => {
    test("agent section shows with Agents header", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByText("Agents")).toBeVisible({ timeout: 10000 });
    });

    test("agent cards render for each agent", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      // Agent names appear in multiple places (agents section + workspace tree)
      await expect(sb.getByText("nova").first()).toBeVisible({
        timeout: 10000,
      });
      await expect(sb.getByText("falcon").first()).toBeVisible();
      // Agent cards in the main agents section (not workspace tree duplicates)
      const agentSection = sb.locator('[class*="agentSection"]').first();
      const agentCards = agentSection.locator("[data-status]");
      await expect(agentCards).toHaveCount(2);
    });

    test("no agent section when loom unavailable and no config agents", async ({
      page,
    }) => {
      // No config agents + loom unavailable = no agents at all
      await setupMocks(page, {
        loomServerAvailable: false,
        workspaceAgents: [],
      });
      await navigateAndWait(page);

      const sb = sidebar(page);
      // Workspace tree still renders
      await expect(sb.getByRole("heading", { name: "Repos" })).toBeVisible({
        timeout: 10000,
      });
      // Agent section header should NOT be visible (no fleet agents, no config agents)
      await expect(sb.getByText("Agents")).not.toBeVisible();
    });
  });

  // ------- agent card details -------

  test.describe("agent card details", () => {
    test("working agent shows Working status label", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      const agentSection = sb.locator('[class*="agentSection"]').first();
      const workingCard = agentSection.locator('[data-status="working"]');
      await expect(workingCard).toBeVisible({ timeout: 10000 });
      const statusLine = workingCard.locator('[class*="statusLine"]');
      await expect(statusLine).toContainText("Working");
    });

    test("ready agent shows Ready status label", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByText("Agents")).toBeVisible({ timeout: 10000 });

      const agentSection = sb.locator('[class*="agentSection"]').first();
      const readyCard = agentSection.locator('[data-status="ready"]');
      await expect(readyCard).toBeVisible({ timeout: 10000 });
      const statusLine = readyCard.locator('[class*="statusLine"]');
      await expect(statusLine).toContainText("Ready");
    });

    test("agent card renders status and metadata", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      const agentSection = sb.locator('[class*="agentSection"]').first();

      // nova: working agent card shows status, role, and repo
      const workingCard = agentSection.locator('[data-status="working"]');
      await expect(workingCard).toBeVisible({ timeout: 10000 });
      await expect(workingCard.locator('[class*="statusLine"]')).toContainText(
        "Working",
      );
      await expect(workingCard.locator('[class*="role"]')).toContainText(
        "Task",
      );
      await expect(workingCard.getByText("loomcli")).toBeVisible();

      // falcon: ready agent card shows status and role
      const readyCard = agentSection.locator('[data-status="ready"]');
      await expect(readyCard).toBeVisible();
      await expect(readyCard.locator('[class*="statusLine"]')).toContainText(
        "Ready",
      );
      await expect(readyCard.locator('[class*="role"]')).toContainText("Plan");
    });

    test("agent card shows role label", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      const agentSection = sb.locator('[class*="agentSection"]').first();

      // nova role: "task" → "Task"
      const workingCard = agentSection.locator('[data-status="working"]');
      await expect(workingCard).toBeVisible({ timeout: 10000 });
      const role = workingCard.locator('[class*="role"]');
      await expect(role).toContainText("Task");

      // falcon role: "plan" → "Plan"
      const readyCard = agentSection.locator('[data-status="ready"]');
      const readyRole = readyCard.locator('[class*="role"]');
      await expect(readyRole).toContainText("Plan");
    });
  });

  // ------- repos -------

  test.describe("repos", () => {
    test("repos section renders repo rows with branch pills", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByRole("heading", { name: "Repos" })).toBeVisible({
        timeout: 10000,
      });
      await expect(sb.getByRole("button", { name: /Add repository/i })).toBeVisible();
    });

    test("repos section shows open issue count pill", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      // mockIssues: 1 open, 1 in_progress, 1 blocked (3 open total)
      const sb = sidebar(page);
      const repoRow = sb.getByRole("button", {
        name: /open issues/i,
      });
      await expect(repoRow.first()).toBeVisible({ timeout: 10000 });
    });
  });

  // ------- sidebar collapse -------

  test.describe("sidebar collapse", () => {
    test("toggle button collapses sidebar", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByText("Agents")).toBeVisible({
        timeout: 10000,
      });

      // Click the toggle icon "<" to avoid hitting the ActiveAllToggle (which has stopPropagation)
      const toggleIcon = sb.locator('[class*="toggleIcon"]');
      await toggleIcon.click();

      await expect(sb).toHaveAttribute("data-collapsed", "true");
      // Content hidden when collapsed
      await expect(sb.getByText("Agents")).not.toBeVisible();
    });

    test("toggle button expands sidebar", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByText("Agents")).toBeVisible({
        timeout: 10000,
      });

      // Collapse first
      const toggleIcon = sb.locator('[class*="toggleIcon"]');
      await toggleIcon.click();
      await expect(sb).toHaveAttribute("data-collapsed", "true");

      // Expand — the toggle icon is now ">"
      await toggleIcon.click();
      await expect(sb).toHaveAttribute("data-collapsed", "false");
      await expect(sb.getByText("Agents")).toBeVisible();
    });

    test("collapsed sidebar hides healthy badge", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      const sb = sidebar(page);
      await expect(sb.getByText("Agents")).toBeVisible({
        timeout: 10000,
      });

      // Collapse
      const toggleIcon = sb.locator('[class*="toggleIcon"]');
      await toggleIcon.click();
      await expect(sb).toHaveAttribute("data-collapsed", "true");

      // Healthy connected state does not need a collapsed warning badge.
      const badge = sb.locator('[class*="collapsedBadge"]');
      await expect(badge).toHaveCount(0);
    });
  });

  // ------- connection states -------

  test.describe("connection states", () => {
    test("sidebar renders with connection issue when workspace endpoint fails", async ({
      page,
    }) => {
      await setupMocks(page, { workspaceError: true });
      await page.goto(BASE_PATH, { waitUntil: "domcontentloaded" });

      // WorkspaceLayout passes non-404 errors through (setValid(true)),
      // so the sidebar still renders. useWorkspaceRepos fails and shows
      // a connection error state or reconnection attempt status.
      const sb = sidebar(page);
      await expect(sb).toBeVisible({ timeout: 10000 });

      // The status badge in the header should indicate a connection problem
      // Either ErrorDisplay, reconnecting status, or connection lost banner
      const reconnecting = page.getByText(/Reconnecting/);
      const connectionLost = page.getByText("Connection lost");
      const errorDisplay = page.getByTestId("error-display");
      const errorBadge = sb.locator('[class*="errorBadge"]');
      await expect(
        reconnecting.or(connectionLost).or(errorDisplay).or(errorBadge),
      ).toBeVisible({ timeout: 15000 });
    });

    test("no agent section when agents array is empty and no config agents", async ({
      page,
    }) => {
      // Must also remove workspace config agents to get zero total agents
      await setupMocks(page, { emptyAgents: true, workspaceAgents: [] });
      await navigateAndWait(page);

      const sb = sidebar(page);
      // Workspace tree renders
      await expect(sb.getByRole("heading", { name: "Repos" })).toBeVisible({
        timeout: 10000,
      });
      // No agent section since agents.length === 0
      await expect(sb.getByText("Agents")).not.toBeVisible();
    });
  });
});

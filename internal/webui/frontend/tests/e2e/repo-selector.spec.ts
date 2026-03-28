/**
 * E2E: RepoSelector — multi-checkbox dropdown for filtering issues by repository.
 *
 * Tests the RepoSelector component rendered inside FilterBar. It only appears
 * when the workspace has 2+ repos. Checkbox selections update the workspace
 * context which drives client-side issue filtering by `issue.repo`. Selection
 * is persisted to workspace-scoped localStorage.
 *
 * Uses workspace-scoped API mocking via /ws/{id}/ routes following the
 * workspace-breadcrumb.spec.ts pattern.
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const WORKSPACE_ID = "test-ws";
const BASE_PATH = `/ws/${WORKSPACE_ID}/`;
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

// ---------------------------------------------------------------------------
// Mock data
// ---------------------------------------------------------------------------

function createWorkspaceResponse(
  repos: Array<{ name: string; path: string }>
) {
  return {
    success: true,
    data: {
      id: WORKSPACE_ID,
      name: "test-workspace",
      path: "/tmp/test",
      repos: repos.map((r) => ({
        name: r.name,
        path: r.path,
        default_branch: "main",
        remote: "origin",
        groups: [],
      })),
      groups: [],
      agents: [],
      workspaces: [
        {
          id: WORKSPACE_ID,
          name: "test-workspace",
          path: "/tmp/test",
          active: true,
          repo_count: repos.length,
          is_default: true,
        },
      ],
      workspace_order: ["test-workspace"],
      default_workspace: "test-workspace",
    },
  };
}

const MULTI_REPO_REPOS = [
  { name: "frontend", path: "/tmp/test/frontend" },
  { name: "backend", path: "/tmp/test/backend" },
  { name: "shared", path: "/tmp/test/shared" },
];

const SINGLE_REPO_REPOS = [{ name: "monorepo", path: "/tmp/test/monorepo" }];

const mockIssues = [
  {
    id: "issue-fe-1",
    title: "Login page",
    description: "",
    notes: "",
    repo: "frontend",
    status: "open",
    priority: 0,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
  {
    id: "issue-fe-2",
    title: "Dashboard CSS",
    description: "",
    notes: "",
    repo: "frontend",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
  {
    id: "issue-be-1",
    title: "API endpoint",
    description: "",
    notes: "",
    repo: "backend",
    status: "open",
    priority: 1,
    issue_type: "bug",
    created_at: "2026-01-24T12:00:00Z",
    updated_at: "2026-01-24T12:00:00Z",
  },
  {
    id: "issue-be-2",
    title: "DB migration",
    description: "",
    notes: "",
    repo: "backend",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T13:00:00Z",
    updated_at: "2026-01-24T13:00:00Z",
  },
  {
    id: "issue-sh-1",
    title: "Shared utils",
    description: "",
    notes: "",
    repo: "shared",
    status: "open",
    priority: 3,
    issue_type: "task",
    created_at: "2026-01-24T14:00:00Z",
    updated_at: "2026-01-24T14:00:00Z",
  },
];

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

interface SetupOptions {
  repos?: Array<{ name: string; path: string }>;
  issues?: typeof mockIssues;
}

async function setupMocks(page: Page, options: SetupOptions = {}) {
  const { repos = MULTI_REPO_REPOS, issues = mockIssues } = options;
  const workspaceResponse = createWorkspaceResponse(repos);

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

  // Auth token
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
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
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(workspaceResponse),
        });
        return;
      }

      // SSE events — abort to prevent hang
      if (url.includes(WS_API + "/events")) {
        await route.abort();
        return;
      }

      // Workspace data: /api/workspaces/test-ws (exact)
      const afterWs = url.split(WS_API)[1] || "";
      if (
        afterWs === "" ||
        afterWs === "/" ||
        afterWs.startsWith("?") ||
        afterWs.startsWith("/?")
      ) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify(workspaceResponse),
        });
        return;
      }

      // Ready (issues) or Kanban issues (/issues?exclude_status=...)
      if (afterWs.startsWith("/ready") || afterWs.startsWith("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: issues }),
        });
        return;
      }

      // Stats
      if (afterWs.startsWith("/stats")) {
        const openCount = issues.filter((i) => i.status === "open").length;
        const inProgressCount = issues.filter(
          (i) => i.status === "in_progress"
        ).length;
        const closedCount = issues.filter((i) => i.status === "closed").length;
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: {
              total: issues.length,
              open: openCount,
              in_progress: inProgressCount,
              closed: closedCount,
            },
          }),
        });
        return;
      }

      // Issues graph
      if (afterWs.startsWith("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: issues }),
        });
        return;
      }

      // Fallback for other workspace-scoped routes
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    }
  );
}

async function navigateAndWait(page: Page, path: string) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        (res.url().includes("/issues") || res.url().includes("/ready")) &&
        res.status() === 200
    ),
    page.goto(path),
  ]);
  expect(response.ok()).toBe(true);
}

// ---------------------------------------------------------------------------
// Tests: Visibility
// ---------------------------------------------------------------------------

test.describe("RepoSelector E2E — Visibility", () => {
  test("not visible when workspace has only 1 repo", async ({ page }) => {
    await setupMocks(page, { repos: SINGLE_REPO_REPOS });
    await navigateAndWait(page, BASE_PATH);

    // FilterBar should be visible but repo trigger should not
    const trigger = page.getByTestId("repo-filter-trigger");
    await expect(trigger).not.toBeVisible();
  });

  test("visible when workspace has 2+ repos", async ({ page }) => {
    await setupMocks(page);
    await navigateAndWait(page, BASE_PATH);

    const trigger = page.getByTestId("repo-filter-trigger");
    await expect(trigger).toBeVisible();
    await expect(trigger).toContainText("Repos");
    expect(await trigger.textContent()).not.toContain("(");
  });
});

// ---------------------------------------------------------------------------
// Tests: Dropdown interactions
// ---------------------------------------------------------------------------

test.describe("RepoSelector E2E — Dropdown interactions", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("clicking trigger opens dropdown with repo checkboxes", async ({
    page,
  }) => {
    await navigateAndWait(page, BASE_PATH);

    const trigger = page.getByTestId("repo-filter-trigger");
    await trigger.click();

    const menu = page.getByTestId("repo-filter-menu");
    await expect(menu).toBeVisible();

    // Should have 3 checkboxes (frontend, backend, shared)
    await expect(page.getByTestId("repo-option-frontend")).toBeVisible();
    await expect(page.getByTestId("repo-option-backend")).toBeVisible();
    await expect(page.getByTestId("repo-option-shared")).toBeVisible();

    // All checkboxes should be unchecked initially (empty = all repos)
    await expect(page.getByTestId("repo-option-frontend")).not.toBeChecked();
    await expect(page.getByTestId("repo-option-backend")).not.toBeChecked();
    await expect(page.getByTestId("repo-option-shared")).not.toBeChecked();
  });

  test("clicking trigger again closes dropdown", async ({ page }) => {
    await navigateAndWait(page, BASE_PATH);

    const trigger = page.getByTestId("repo-filter-trigger");
    await trigger.click();

    const menu = page.getByTestId("repo-filter-menu");
    await expect(menu).toBeVisible();

    // Click trigger again to close
    await trigger.click();
    await expect(menu).not.toBeVisible();
  });

  test("clicking outside closes dropdown", async ({ page }) => {
    await navigateAndWait(page, BASE_PATH);

    const trigger = page.getByTestId("repo-filter-trigger");
    await trigger.click();

    const menu = page.getByTestId("repo-filter-menu");
    await expect(menu).toBeVisible();

    // Click on body to close
    await page.locator("body").click({ position: { x: 10, y: 10 } });
    await expect(menu).not.toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Tests: Repo filtering
// ---------------------------------------------------------------------------

test.describe("RepoSelector E2E — Repo filtering", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("selecting a repo filters issues to only that repo", async ({
    page,
  }) => {
    await navigateAndWait(page, BASE_PATH);

    const openColumn = page.locator('section[data-status="ready"]');
    await expect(openColumn).toBeVisible();

    // Initially all 4 open issues should be visible
    const openCards = openColumn.locator("article");
    await expect(openCards).toHaveCount(4);

    // Open dropdown and select "frontend" via click (not .check() which races re-render)
    const trigger = page.getByTestId("repo-filter-trigger");
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();

    // Wait for filter to apply by verifying trigger label update
    await expect(trigger).toContainText("Repos (1)");

    // Only frontend issues should be visible
    await expect(openColumn.getByText("Login page")).toBeVisible();
    await expect(openColumn.getByText("Dashboard CSS")).toBeVisible();
    await expect(openColumn.getByText("API endpoint")).not.toBeVisible();
    await expect(openColumn.getByText("Shared utils")).not.toBeVisible();

    // In-progress column should not show backend issue
    const inProgressColumn = page.locator('section[data-status="in_progress"]');
    const inProgressCards = inProgressColumn.locator("article");
    await expect(inProgressCards).toHaveCount(0);
  });

  test("selecting multiple repos shows issues from all selected repos", async ({
    page,
  }) => {
    await navigateAndWait(page, BASE_PATH);

    const openColumn = page.locator('section[data-status="ready"]');
    await expect(openColumn).toBeVisible();

    const trigger = page.getByTestId("repo-filter-trigger");

    // Select "frontend" — dropdown may close after state update
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();
    await expect(trigger).toContainText("Repos (1)");

    // Reopen dropdown and select "backend"
    await trigger.click();
    await page.getByTestId("repo-option-backend").click();
    await expect(trigger).toContainText("Repos (2)");

    // Frontend and backend issues should be visible, shared should be hidden
    await expect(openColumn.getByText("Login page")).toBeVisible();
    await expect(openColumn.getByText("Dashboard CSS")).toBeVisible();
    await expect(openColumn.getByText("API endpoint")).toBeVisible();
    await expect(openColumn.getByText("Shared utils")).not.toBeVisible();

    // In-progress column should show backend issue
    const inProgressColumn = page.locator('section[data-status="in_progress"]');
    await expect(inProgressColumn.getByText("DB migration")).toBeVisible();
  });

  test("deselecting all repos shows all issues again", async ({ page }) => {
    await navigateAndWait(page, BASE_PATH);

    const openColumn = page.locator('section[data-status="ready"]');
    await expect(openColumn).toBeVisible();

    const trigger = page.getByTestId("repo-filter-trigger");

    // Select a repo first
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();
    await expect(trigger).toContainText("Repos (1)");

    // Only frontend issues visible
    const openCards = openColumn.locator("article");
    await expect(openCards).toHaveCount(2);

    // Reopen dropdown and deselect frontend (click to uncheck)
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();

    // Trigger should revert to "Repos" with no count
    await expect(trigger).toContainText("Repos");
    expect(await trigger.textContent()).not.toContain("(");

    // All issues should be visible again
    await expect(openCards).toHaveCount(4);
    await expect(openColumn.getByText("API endpoint")).toBeVisible();
    await expect(openColumn.getByText("Shared utils")).toBeVisible();
  });

  test("trigger label shows count when repos selected", async ({ page }) => {
    await navigateAndWait(page, BASE_PATH);

    const trigger = page.getByTestId("repo-filter-trigger");

    // Initially shows "Repos" (no count)
    await expect(trigger).toContainText("Repos");
    expect(await trigger.textContent()).not.toContain("(");

    // Select first repo
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();
    await expect(trigger).toContainText("Repos (1)");

    // Reopen dropdown and select second repo
    await trigger.click();
    await page.getByTestId("repo-option-backend").click();

    // Trigger should show "Repos (2)"
    await expect(trigger).toContainText("Repos (2)");
  });
});

// ---------------------------------------------------------------------------
// Tests: Filter integration
// ---------------------------------------------------------------------------

test.describe("RepoSelector E2E — Filter integration", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("repo filter combines with priority filter", async ({ page }) => {
    await navigateAndWait(page, BASE_PATH);

    const openColumn = page.locator('section[data-status="ready"]');
    await expect(openColumn).toBeVisible();

    // Select "frontend" repo
    const trigger = page.getByTestId("repo-filter-trigger");
    await trigger.click();
    await page.getByTestId("repo-option-frontend").click();
    await expect(trigger).toContainText("Repos (1)");

    // Also select P0 priority (only issue-fe-1 "Login page" is P0+frontend)
    const priorityFilter = page.getByTestId("priority-filter");
    await priorityFilter.selectOption("0");

    // Only the intersection should be visible: P0 + frontend = "Login page"
    const openCards = openColumn.locator("article");
    await expect(openCards).toHaveCount(1);
    await expect(openColumn.getByText("Login page")).toBeVisible();
  });
});

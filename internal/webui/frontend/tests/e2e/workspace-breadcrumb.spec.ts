/**
 * E2E: Workspace breadcrumb navigation.
 *
 * Tests the WorkspaceBreadcrumb component rendered in the AppLayout header.
 * Breadcrumb shows "● WorkspaceName / ViewLabel" when isMultiRepo is true
 * (workspace has 1+ repos) and falls back to "Cortex" when isMultiRepo is
 * false (0 repos). Uses workspace-scoped API mocking via /ws/{id}/ routes.
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
// Mock data helpers
// ---------------------------------------------------------------------------

function createWorkspaceResponse(
  name: string,
  repos: Array<{ name: string; path: string }>
) {
  return {
    success: true,
    data: {
      id: WORKSPACE_ID,
      name,
      path: "/mock/path/" + name,
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
          name,
          path: "/mock/path/" + name,
          active: true,
          repo_count: repos.length,
          is_default: true,
        },
      ],
      workspace_order: [name],
      default_workspace: name,
    },
  };
}

const mockIssues = [
  {
    id: "test-1",
    title: "Test Issue 1",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-24T10:00:00Z",
    updated_at: "2026-01-24T10:00:00Z",
  },
];

// ---------------------------------------------------------------------------
// Setup helpers
// ---------------------------------------------------------------------------

interface SetupOptions {
  workspaceStatus?: number;
}

async function setupMocks(
  page: Page,
  workspaceResponse: object,
  options: SetupOptions = {}
) {
  const { workspaceStatus = 200 } = options;
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
        if (workspaceStatus !== 200) {
          await route.fulfill({
            status: workspaceStatus,
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
          body: JSON.stringify(workspaceResponse),
        });
        return;
      }

      // Ready (issues)
      if (afterWs.startsWith("/ready")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
        });
        return;
      }

      // Stats
      if (afterWs.startsWith("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({
            success: true,
            data: { open: 1, closed: 0, total: 1, completion: 0 },
          }),
        });
        return;
      }

      // Issues graph
      if (afterWs.startsWith("/issues/graph")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: JSON.stringify({ success: true, data: mockIssues }),
        });
        return;
      }

      // Fallback for other workspace-scoped routes (blocked, issues, etc.)
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
    }
  );
}

// ---------------------------------------------------------------------------
// Tests: zero-repo fallback
// ---------------------------------------------------------------------------

test.describe("zero-repo fallback", () => {
  test("shows Cortex when workspace has no repos", async ({ page }) => {
    const wsResponse = createWorkspaceResponse("empty-project", []);
    await setupMocks(page, wsResponse);

    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Cortex")).toBeVisible();

    // No separator or dot in zero-repo mode
    await expect(
      page.locator("h1").getByText("/", { exact: true })
    ).not.toBeVisible();
    await expect(
      page.locator('h1 span[style*="background"]')
    ).not.toBeVisible();
  });

  test("shows Cortex when workspace API returns error", async ({ page }) => {
    // Workspace endpoint returns 500 — WorkspaceLayout passes non-404 errors
    // through (valid=true), useWorkspace fails → workspace=null → "Cortex"
    const wsResponse = createWorkspaceResponse("err", []);
    await setupMocks(page, wsResponse, { workspaceStatus: 500 });

    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Cortex")).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Tests: multi-repo breadcrumb
// ---------------------------------------------------------------------------

test.describe("multi-repo breadcrumb", () => {
  const multiRepoResponse = createWorkspaceResponse("my-project", [
    { name: "repo-one", path: "/mock/repos/one" },
    { name: "repo-two", path: "/mock/repos/two" },
  ]);

  test.beforeEach(async ({ page }) => {
    await setupMocks(page, multiRepoResponse);
  });

  test("shows workspace name with dot and separator", async ({ page }) => {
    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("my-project")).toBeVisible();
    await expect(
      page.locator("h1").getByText("/", { exact: true })
    ).toBeVisible();
    await expect(page.locator('h1 span[style*="background"]')).toBeVisible();
  });

  test("shows Kanban label on default view", async ({ page }) => {
    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Kanban")).toBeVisible();
  });

  test("shows List label on table view", async ({ page }) => {
    await page.goto(BASE_PATH + "?view=table");
    await expect(page.locator("h1").getByText("List")).toBeVisible();
  });

  test("shows Terminal label on terminal view", async ({ page }) => {
    await page.goto(BASE_PATH + "?view=terminal");
    await expect(page.locator("h1").getByText("Terminal")).toBeVisible();
  });

  test("shows Settings label on settings view", async ({ page }) => {
    await page.goto(BASE_PATH + "?view=settings");
    await expect(page.locator("h1").getByText("Settings")).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Tests: view label updates via NavRail
// ---------------------------------------------------------------------------

test.describe("view label updates via NavRail", () => {
  const multiRepoResponse = createWorkspaceResponse("nav-project", [
    { name: "repo-a", path: "/mock/repos/a" },
  ]);

  test.beforeEach(async ({ page }) => {
    await setupMocks(page, multiRepoResponse);
  });

  test("updates to List when clicking List NavRail button", async ({
    page,
  }) => {
    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Kanban")).toBeVisible();

    await page.getByRole("button", { name: "List" }).click();
    await expect(page.locator("h1").getByText("List")).toBeVisible();
  });

  test("updates to Terminal when clicking Terminal NavRail button", async ({
    page,
  }) => {
    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Kanban")).toBeVisible();

    await page.getByRole("button", { name: "Terminal" }).click();
    await expect(page.locator("h1").getByText("Terminal")).toBeVisible();
  });

  test("updates to Settings when clicking Settings NavRail button", async ({
    page,
  }) => {
    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("Kanban")).toBeVisible();

    await page.getByRole("button", { name: "Settings" }).click();
    await expect(page.locator("h1").getByText("Settings")).toBeVisible();
  });

  test("updates back to Kanban when clicking Kanban NavRail button", async ({
    page,
  }) => {
    await page.goto(BASE_PATH + "?view=table");
    await expect(page.locator("h1").getByText("List")).toBeVisible();

    await page.getByRole("button", { name: "Kanban" }).click();
    await expect(page.locator("h1").getByText("Kanban")).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Tests: workspace name display
// ---------------------------------------------------------------------------

test.describe("workspace name display", () => {
  test("displays workspace name from API response", async ({ page }) => {
    const wsResponse = createWorkspaceResponse("alpha-team", [
      { name: "main-repo", path: "/mock/repos/main" },
    ]);
    await setupMocks(page, wsResponse);

    await page.goto(BASE_PATH);
    await expect(page.locator("h1").getByText("alpha-team")).toBeVisible();
  });
});

// ---------------------------------------------------------------------------
// Tests: visual structure
// ---------------------------------------------------------------------------

test.describe("visual structure", () => {
  test("dot has a background color", async ({ page }) => {
    const wsResponse = createWorkspaceResponse("color-project", [
      { name: "repo", path: "/mock/repos/repo" },
    ]);
    await setupMocks(page, wsResponse);

    await page.goto(BASE_PATH);
    const dot = page.locator('h1 span[style*="background"]');
    await expect(dot).toBeVisible();
  });
});

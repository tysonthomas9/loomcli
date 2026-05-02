/**
 * E2E: workspace identity in the current app shell.
 *
 * The header is global application chrome ("Aether"). Workspace identity is
 * local to the workspace tree/sidebar and view state is represented by the
 * Workspaces view tabs.
 */

import { test, expect } from "../fixtures";
import type { Page } from "@playwright/test";

const WORKSPACE_ID = "test-ws";
const BASE_PATH = `/ws/${WORKSPACE_ID}/`;
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

function createWorkspaceResponse(
  name: string,
  repos: Array<{ name: string; path: string }>,
) {
  return {
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
    workspace_order: [WORKSPACE_ID],
    default_workspace: WORKSPACE_ID,
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

interface SetupOptions {
  workspaceStatus?: number;
}

async function setupMocks(
  page: Page,
  workspaceData: object,
  options: SetupOptions = {},
) {
  const { workspaceStatus = 200 } = options;

  await page.route("**/api/config", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname !== "/api/config") {
      await route.fallback();
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        mode: "open",
        auth: { mode: "open" },
        version: "test",
      }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
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

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  await page.route(
    (url) => {
      const s = url.toString();
      return s.includes("/api/workspaces/") && !s.includes("/src/");
    },
    async (route) => {
      const url = route.request().url();

      if (url.includes("/events")) {
        await route.fulfill({
          status: 200,
          contentType: "text/event-stream",
          headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
          body: 'event: connected\ndata: {"message":"connected"}\n\n',
        });
        return;
      }

      if (url.includes("/api/workspaces/active")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

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
            body: JSON.stringify({ success: false, error: "Internal Server Error" }),
          });
          return;
        }
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(workspaceData),
        });
        return;
      }

      if (afterWs.startsWith("/ready") || afterWs.startsWith("/issues")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(mockIssues),
        });
        return;
      }

      if (afterWs.startsWith("/stats")) {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            total_issues: 1,
            open_issues: 1,
            in_progress_issues: 0,
            closed_issues: 0,
            blocked_issues: 0,
            deferred_issues: 0,
            ready_issues: 1,
            tombstone_issues: 0,
            pinned_issues: 0,
            epics_eligible_for_closure: 0,
            average_lead_time_hours: 0,
          }),
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
}

async function expectGlobalHeader(page: Page) {
  await expect(page.getByRole("banner").getByRole("heading", {
    name: "Aether",
  })).toBeVisible();
}

test.describe("global header and local workspace identity", () => {
  test("keeps global brand in the header when workspace has no repos", async ({
    page,
  }) => {
    await setupMocks(page, createWorkspaceResponse("empty-project", []));

    await page.goto(BASE_PATH);

    await expectGlobalHeader(page);
    await expect(
      page.getByRole("button", {
        name: /Active workspace: empty-project\. Click to switch\./,
      }),
    ).toBeVisible();
    await expect(page.getByText("No repos in workspace")).toBeVisible();
  });

  test("keeps global brand in the header when workspace API returns error", async ({
    page,
  }) => {
    await setupMocks(page, createWorkspaceResponse("err", []), {
      workspaceStatus: 500,
    });

    await page.goto(BASE_PATH);

    await expectGlobalHeader(page);
  });

  test("shows workspace name in the workspace tree, not the global heading", async ({
    page,
  }) => {
    await setupMocks(
      page,
      createWorkspaceResponse("my-project", [
        { name: "repo-one", path: "/mock/repos/one" },
        { name: "repo-two", path: "/mock/repos/two" },
      ]),
    );

    await page.goto(BASE_PATH);

    await expectGlobalHeader(page);
    await expect(
      page.getByRole("button", {
        name: /Active workspace: my-project\. Click to switch\./,
      }),
    ).toBeVisible();
    await expect(page.getByText("repo-one")).toBeVisible();
    await expect(page.getByText("repo-two")).toBeVisible();
    await expect(page.locator("h1").getByText("my-project")).not.toBeVisible();
  });

  test("uses Workspaces view tabs for Kanban/List local view state", async ({
    page,
  }) => {
    await setupMocks(
      page,
      createWorkspaceResponse("nav-project", [
        { name: "repo-a", path: "/mock/repos/a" },
      ]),
    );

    await page.goto(BASE_PATH);

    await expect(page.getByRole("tab", { name: "Kanban" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await page.getByRole("tab", { name: "List" }).click();
    await expect(page.getByRole("tab", { name: "List" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await page.getByRole("tab", { name: "Kanban" }).click();
    await expect(page.getByRole("tab", { name: "Kanban" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });
});

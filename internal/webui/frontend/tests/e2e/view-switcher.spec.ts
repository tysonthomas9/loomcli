import { test, expect, Page } from "@playwright/test";

import {
  setupFleetMocks,
  waitForWorkspaceIssues,
  WORKSPACE_ID,
  WS_API,
  ok,
} from "./helpers/fleet";

/**
 * Mock issues for testing view switching.
 * Minimal set to verify views render with data.
 */
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
  {
    id: "test-2",
    title: "Test Issue 2",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    created_at: "2026-01-24T11:00:00Z",
    updated_at: "2026-01-24T11:00:00Z",
  },
];

async function setupExtraMocks(page: Page) {
  await page.route("**/*", async (route) => {
    const pathname = new URL(route.request().url()).pathname;

    if (pathname === "/api/client-errors") {
      await route.fulfill({ status: 204 });
    } else if (
      pathname === `${WS_API}/agents` ||
      pathname === `${WS_API}/terminal/sessions` ||
      pathname === `${WS_API}/terminal/sessions/by-issue`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    } else if (pathname === "/api/monitor/agents") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          workspace: { mode: "workspace", name: "Default" },
          agents: [],
          timestamp: "2026-01-24T12:00:00Z",
        }),
      });
    } else if (pathname === "/api/monitor/status") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          workspace: { mode: "workspace", name: "Default" },
          agents: [],
          tasks: {
            needs_planning: 0,
            ready_to_implement: 1,
            in_progress: 1,
            need_review: 0,
            backlog: 0,
            epics: 0,
          },
          in_progress_list: [],
          agent_tasks: {},
          stats: {
            open: 1,
            closed: 0,
            total: 2,
            completion: 0,
            remaining: 2,
            in_progress: 1,
            review: 0,
            blocked: 0,
          },
          sync: {
            db_synced: true,
            db_last_sync: "2026-01-24T12:00:00Z",
            git_needs_push: 0,
            git_needs_pull: 0,
          },
          timestamp: "2026-01-24T12:00:00Z",
        }),
      });
    } else if (pathname === "/api/monitor/tasks") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          summary: {
            needs_planning: 0,
            ready_to_implement: 1,
            in_progress: 1,
            need_review: 0,
            backlog: 0,
            epics: 0,
          },
          needs_planning: [],
          ready_to_implement: [],
          needs_review: [],
          in_progress: [],
          backlog: [],
          closed: [],
          timestamp: "2026-01-24T12:00:00Z",
        }),
      });
    } else if (pathname === "/api/monitor/usage") {
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
          timestamp: "2026-01-24T12:00:00Z",
        }),
      });
    } else if (pathname.startsWith("/api/monitor/")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok({})),
      });
    } else {
      await route.fallback();
    }
  });
}

async function setupMocks(page: Page) {
  await setupFleetMocks(page, mockIssues);
  await setupExtraMocks(page);
}

async function gotoView(
  page: Page,
  view: "kanban" | "table" | "graph" | "monitor",
) {
  const path = `/ws/${WORKSPACE_ID}/${view}`;
  const responsePromise =
    view === "graph"
      ? page.waitForResponse(
          (res) =>
            new URL(res.url()).pathname === `${WS_API}/issues/graph` &&
            res.status() === 200,
        )
      : view === "monitor"
        ? page.waitForResponse(
            (res) =>
              new URL(res.url()).pathname === `${WS_API}/ready` &&
              res.status() === 200,
          )
        : waitForWorkspaceIssues(page);

  await Promise.all([responsePromise, page.goto(path)]);
  expect(new URL(page.url()).pathname).toBe(path);
}

function viewModeTab(page: Page, name: "Kanban" | "List") {
  return page.getByRole("tablist", { name: "View mode" }).getByRole("tab", {
    name,
  });
}

async function expectPath(page: Page, path: string) {
  await expect.poll(() => new URL(page.url()).pathname).toBe(path);
}

test.describe("ViewSwitcher", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("kanban route renders Kanban view", async ({ page }) => {
    await gotoView(page, "kanban");

    await expect(viewModeTab(page, "Kanban")).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.locator('section[data-status="ready"]')).toBeVisible();
  });

  test("clicking List tab renders IssueTable and updates route", async ({
    page,
  }) => {
    await gotoView(page, "kanban");

    await viewModeTab(page, "List").click();

    await expectPath(page, `/ws/${WORKSPACE_ID}/table`);
    await expect(viewModeTab(page, "List")).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.getByTestId("issue-table")).toBeVisible();
    await expect(
      page.locator('section[data-status="ready"]'),
    ).not.toBeVisible();
  });

  test("clicking Kanban tab returns to Kanban route", async ({ page }) => {
    await gotoView(page, "table");

    await viewModeTab(page, "Kanban").click();

    await expectPath(page, `/ws/${WORKSPACE_ID}/kanban`);
    await expect(viewModeTab(page, "Kanban")).toHaveAttribute(
      "aria-selected",
      "true",
    );
    await expect(page.locator('section[data-status="ready"]')).toBeVisible();
  });

  test("graph route renders GraphView", async ({ page }) => {
    await gotoView(page, "graph");

    await expect(page.getByTestId("graph-view")).toBeVisible();
    await expect(page.getByTestId("issue-table")).not.toBeVisible();
    await expect(
      page.locator('section[data-status="ready"]'),
    ).not.toBeVisible();
  });

  test("monitor route renders MonitorDashboard", async ({ page }) => {
    await gotoView(page, "monitor");

    await expect(page.getByTestId("monitor-dashboard")).toBeVisible();
    await expect(
      page.getByRole("heading", { name: "Project Health" }),
    ).toBeVisible();
  });

  test.describe("keyboard navigation", () => {
    test("ArrowRight navigates from Kanban to List", async ({ page }) => {
      await gotoView(page, "kanban");

      const kanbanTab = viewModeTab(page, "Kanban");
      await kanbanTab.focus();
      await page.keyboard.press("ArrowRight");

      const listTab = viewModeTab(page, "List");
      await expect(listTab).toHaveAttribute("aria-selected", "true");
      await expectPath(page, `/ws/${WORKSPACE_ID}/table`);
      await expect(page.getByTestId("issue-table")).toBeVisible();
    });

    test("ArrowLeft navigates from List to Kanban", async ({ page }) => {
      await gotoView(page, "table");

      const listTab = viewModeTab(page, "List");
      await listTab.focus();
      await page.keyboard.press("ArrowLeft");

      const kanbanTab = viewModeTab(page, "Kanban");
      await expect(kanbanTab).toHaveAttribute("aria-selected", "true");
      await expectPath(page, `/ws/${WORKSPACE_ID}/kanban`);
      await expect(page.locator('section[data-status="ready"]')).toBeVisible();
    });
  });
});

test.describe("ViewSwitcher route segments", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("table route segment loads Table view", async ({ page }) => {
    await gotoView(page, "table");

    await expectPath(page, `/ws/${WORKSPACE_ID}/table`);
    expect(page.url()).not.toContain("view=");
    await expect(page.getByTestId("issue-table")).toBeVisible();
    await expect(viewModeTab(page, "List")).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  test("graph route segment loads Graph view", async ({ page }) => {
    await gotoView(page, "graph");

    await expectPath(page, `/ws/${WORKSPACE_ID}/graph`);
    expect(page.url()).not.toContain("view=");
    await expect(page.getByTestId("graph-view")).toBeVisible();
  });
});

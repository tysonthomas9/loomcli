import { test, expect, Page } from "@playwright/test";
import { dragWithPointer } from "../helpers";

/**
 * Workspace fixture — must have a non-empty id, otherwise WorkspaceLayout
 * redirects to "/" and loops.
 */
const mockWorkspaceData = {
  id: "default",
  name: "default",
  path: "/test",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: "default",
      name: "default",
      path: "/test",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: ["default"],
  default_workspace: "default",
};

/**
 * Mock issues for testing groupBy Epic swim lanes.
 * Includes multiple epics, issues with/without parent, varied statuses.
 */
const mockIssues = [
  // Epic One - 3 issues across different statuses
  {
    id: "epic-1-open",
    title: "Feature in Epic One (Open)",
    status: "open",
    priority: 2,
    issue_type: "feature",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "epic-1-progress",
    title: "Task in Epic One (In Progress)",
    status: "in_progress",
    priority: 1,
    issue_type: "task",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "epic-1-closed",
    title: "Bug in Epic One (Closed)",
    status: "closed",
    priority: 0,
    issue_type: "bug",
    parent: "epic-1",
    parent_title: "Epic One: Authentication",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  // Epic Two - 2 issues
  {
    id: "epic-2-open",
    title: "Feature in Epic Two",
    status: "open",
    priority: 2,
    issue_type: "feature",
    parent: "epic-2",
    parent_title: "Epic Two: Dashboard",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "epic-2-closed",
    title: "Task in Epic Two (Closed)",
    status: "closed",
    priority: 3,
    issue_type: "task",
    parent: "epic-2",
    parent_title: "Epic Two: Dashboard",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
  // Orphan issues (no parent) - go to Ungrouped
  {
    id: "orphan-open",
    title: "Standalone Task (Open)",
    status: "open",
    priority: 4,
    issue_type: "task",
    created_at: "2026-01-27T15:00:00Z",
    updated_at: "2026-01-27T15:00:00Z",
  },
  {
    id: "orphan-closed",
    title: "Standalone Bug (Closed)",
    status: "closed",
    priority: 2,
    issue_type: "bug",
    created_at: "2026-01-27T16:00:00Z",
    updated_at: "2026-01-27T16:00:00Z",
  },
];
// Summary: Epic One = 3, Epic Two = 2, Ungrouped = 2 (total = 7)

/**
 * Set up API mocks for epic grouping tests.
 *
 * Uses workspace-scoped routing: tests navigate to /ws/default/kanban, and
 * API mocks respond on /api/workspaces/:id/... endpoints. The groupBy
 * control now lives in MoreFiltersMenu, not FilterBar — tests must open
 * the "..." popover before interacting with the select.
 */
async function setupMocks(page: Page, issues: object[] = mockIssues) {
  // Neutralize AbortController signals. React StrictMode double-fires effects
  // in dev; the cleanup aborts in-flight fetches before they reach page.route.
  // Strip signals from init and from Request inputs so route.fulfill() always
  // dispatches.
  await page.addInitScript(() => {
    const origFetch = window.fetch;
    window.fetch = function (input: RequestInfo | URL, init?: RequestInit) {
      const strippedInit: RequestInit = init ? { ...init } : {};
      if ("signal" in strippedInit) delete strippedInit.signal;
      if (input instanceof Request) {
        const req = input;
        const newInit: RequestInit = {
          method: req.method,
          headers: req.headers,
          credentials: req.credentials,
          cache: req.cache,
          redirect: req.redirect,
          referrer: req.referrer,
          referrerPolicy: req.referrerPolicy,
          integrity: req.integrity,
          keepalive: req.keepalive,
        };
        const preserveTimeout = (target: Request) => {
          const tc = (req as unknown as { _timeoutController?: unknown })
            ._timeoutController;
          if (tc) {
            (
              target as unknown as { _timeoutController: unknown }
            )._timeoutController = tc;
          }
        };
        if (req.method !== "GET" && req.method !== "HEAD") {
          return req
            .clone()
            .blob()
            .then((blob) => {
              const newReq = new Request(req.url, { ...newInit, body: blob });
              preserveTimeout(newReq);
              return origFetch.call(this, newReq, {});
            });
        }
        const newReq = new Request(req.url, newInit);
        preserveTimeout(newReq);
        return origFetch.call(this, newReq, {});
      }
      return origFetch.call(this, input, strippedInit);
    };
  });

  // App config — fetchAppConfig() runs before anything else on boot
  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  // Auth token — initAuth fires on mount
  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
    });
  });

  // Global health endpoint
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Global backend config endpoint
  await page.route("**/api/config/backend", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        success: true,
        data: {
          backend: "shell",
          source: "default",
          available: ["shell"],
          agents: [],
        },
      }),
    });
  });

  // Workspace-scoped endpoints. Single handler dispatches on pathname.
  await page.route("**/api/workspaces/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    const method = request.method();

    // SSE events — abort so we don't hang waitForLoadState("networkidle")
    if (/\/api\/workspaces\/[^/]+\/events/.test(pathname)) {
      await route.abort();
      return;
    }

    // PATCH /api/workspaces/{ws}/issues/{id} — drag-and-drop status update
    if (
      /\/api\/workspaces\/[^/]+\/issues\/[^/]+$/.test(pathname) &&
      method === "PATCH"
    ) {
      const issueId = pathname.split("/issues/")[1]?.split("/")[0];
      const body = request.postDataJSON() as Record<string, unknown>;
      const issue = issues.find((i) => (i as { id: string }).id === issueId) as
        | object
        | undefined;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: {
            ...(issue ?? {}),
            ...body,
            updated_at: new Date().toISOString(),
          },
        }),
      });
      return;
    }

    // GET /api/workspaces/{ws}/issues — kanban mode hits this via getKanbanIssues
    // GET /api/workspaces/{ws}/ready — other modes hit this
    if (
      (/\/api\/workspaces\/[^/]+\/issues$/.test(pathname) ||
        /\/api\/workspaces\/[^/]+\/ready$/.test(pathname)) &&
      method === "GET"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: issues }),
      });
      return;
    }

    // /api/workspaces/{ws}/stats
    if (/\/api\/workspaces\/[^/]+\/stats$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { open: 0, closed: 0, total: 0, completion: 0 },
        }),
      });
      return;
    }

    // /api/workspaces/{ws}/blocked
    if (/\/api\/workspaces\/[^/]+\/blocked$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
      return;
    }

    // /api/workspaces/{ws}/issues/graph
    if (/\/api\/workspaces\/[^/]+\/issues\/graph$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: issues }),
      });
      return;
    }

    // /api/workspaces/{ws} — workspace metadata
    if (/^\/api\/workspaces\/[^/]+\/?$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockWorkspaceData }),
      });
      return;
    }

    // Fallback under /api/workspaces/*
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    });
  });

  // Loom endpoints (not workspace-scoped) — abort so they don't hang
  await page.route("**/api/monitor/**", async (route) => {
    await route.abort();
  });
}

/**
 * Navigate to /ws/default/kanban and wait for the kanban issues response.
 * "epic" is DEFAULT_GROUP_BY so no ?groupBy=epic query param is needed.
 */
async function navigateToEpicView(page: Page) {
  const [response] = await Promise.all([
    page.waitForResponse(
      (res) =>
        res.url().includes("/api/workspaces/") &&
        /\/issues(\?|$)/.test(res.url()) &&
        res.status() === 200,
    ),
    page.goto("/ws/default/kanban"),
  ]);
  expect(response.ok()).toBe(true);
  await expect(page.getByTestId("swim-lane-board")).toBeVisible();
}

/**
 * Open the MoreFiltersMenu popover so the groupBy select becomes interactable.
 * Returns a locator for the select element.
 */
async function openMoreFiltersMenu(page: Page) {
  await page.getByTestId("more-filters-trigger").click();
  await expect(page.getByTestId("more-filters-menu")).toBeVisible();
  return page.getByTestId("more-filters-groupby");
}

/**
 * Close the MoreFiltersMenu popover if it's open, then assert closed.
 * Idempotent so it still works if a future refactor makes the popover
 * auto-close on select change.
 */
async function closeMoreFiltersMenu(page: Page) {
  const menu = page.getByTestId("more-filters-menu");
  if (await menu.isVisible()) {
    await page.getByTestId("more-filters-trigger").click();
  }
  await expect(menu).not.toBeVisible();
}

/**
 * Change the groupBy selection by opening the popover, selecting, then closing.
 */
async function selectGroupBy(page: Page, value: string) {
  const select = await openMoreFiltersMenu(page);
  await select.selectOption(value);
  await closeMoreFiltersMenu(page);
}

/**
 * Get the current groupBy value by briefly opening the popover.
 */
async function readGroupByValue(page: Page) {
  const select = await openMoreFiltersMenu(page);
  const value = await select.inputValue();
  await closeMoreFiltersMenu(page);
  return value;
}

/**
 * Get a specific epic lane by its parent ID.
 */
function getEpicLane(page: Page, epicId: string) {
  return page.getByTestId(`swim-lane-lane-epic-${epicId}`);
}

test.describe("groupBy Epic Swim Lanes", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test.describe("Grouping - Issues Grouped by parent/epic Field", () => {
    test("issues are grouped into lanes by parent field", async ({ page }) => {
      await navigateToEpicView(page);

      // Verify 3 lanes exist: Epic One, Epic Two, Ungrouped
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      await expect(lanes).toHaveCount(3);

      // Verify Epic One has 3 issues
      const epicOneLane = getEpicLane(page, "epic-1");
      await expect(epicOneLane.locator("article")).toHaveCount(3);

      // Verify Epic Two has 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2");
      await expect(epicTwoLane.locator("article")).toHaveCount(2);

      // Verify Ungrouped has 2 orphan issues
      const ungroupedLane = getEpicLane(page, "__ungrouped__");
      await expect(ungroupedLane.locator("article")).toHaveCount(2);
    });

    test("specific issues appear in their parent's lane", async ({ page }) => {
      await navigateToEpicView(page);

      // Verify Epic One contains its 3 issues
      const epicOneLane = getEpicLane(page, "epic-1");
      await expect(
        epicOneLane.getByText("Feature in Epic One (Open)"),
      ).toBeVisible();
      await expect(
        epicOneLane.getByText("Task in Epic One (In Progress)"),
      ).toBeVisible();
      await expect(
        epicOneLane.getByText("Bug in Epic One (Closed)"),
      ).toBeVisible();

      // Verify Epic Two contains its 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2");
      await expect(epicTwoLane.getByText("Feature in Epic Two")).toBeVisible();
      await expect(
        epicTwoLane.getByText("Task in Epic Two (Closed)"),
      ).toBeVisible();
    });
  });

  test.describe("Lane Headers - Each Lane Shows Epic Title", () => {
    test("lane headers display parent_title", async ({ page }) => {
      await navigateToEpicView(page);

      // Verify lane titles show full parent_title (not just ID)
      await expect(
        page.getByRole("heading", {
          name: "Epic One: Authentication",
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page.getByRole("heading", { name: "Epic Two: Dashboard", exact: true }),
      ).toBeVisible();
    });

    test("lane header falls back to parent ID when parent_title missing", async ({
      page,
    }) => {
      // Mock issues without parent_title
      const issuesWithoutTitle = [
        {
          id: "issue-no-title",
          title: "Issue Without Parent Title",
          status: "open",
          priority: 2,
          issue_type: "task",
          parent: "epic-fallback", // No parent_title field
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
      ];

      await setupMocks(page, issuesWithoutTitle);
      await navigateToEpicView(page);

      // Should show parent ID as title
      await expect(
        page.getByRole("heading", { name: "epic-fallback", exact: true }),
      ).toBeVisible();
    });
  });

  test.describe("Ungrouped Lane - Issues Without parent", () => {
    test("orphan issues appear in Ungrouped lane", async ({ page }) => {
      await navigateToEpicView(page);

      const ungroupedLane = getEpicLane(page, "__ungrouped__");
      await expect(ungroupedLane).toBeVisible();

      // Verify orphan issues are in Ungrouped
      await expect(
        ungroupedLane.getByText("Standalone Task (Open)"),
      ).toBeVisible();
      await expect(
        ungroupedLane.getByText("Standalone Bug (Closed)"),
      ).toBeVisible();

      // Verify heading is "Ungrouped"
      await expect(
        ungroupedLane.getByRole("heading", { name: "Ungrouped", exact: true }),
      ).toBeVisible();
    });

    test("Ungrouped lane appears at bottom", async ({ page }) => {
      await navigateToEpicView(page);

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      const laneCount = await lanes.count();

      // Last lane should be Ungrouped
      const lastLane = lanes.nth(laneCount - 1);
      await expect(
        lastLane.getByRole("heading", { name: "Ungrouped", exact: true }),
      ).toBeVisible();
    });

    test("all issues ungrouped creates single Ungrouped lane", async ({
      page,
    }) => {
      // All orphan issues
      const allOrphans = [
        {
          id: "orphan-1",
          title: "Orphan One",
          status: "open",
          priority: 2,
          issue_type: "task",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "orphan-2",
          title: "Orphan Two",
          status: "open",
          priority: 3,
          issue_type: "bug",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, allOrphans);
      await navigateToEpicView(page);

      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      await expect(lanes).toHaveCount(1);
      await expect(
        page.getByRole("heading", { name: "Ungrouped", exact: true }),
      ).toBeVisible();
    });
  });

  test.describe("Issue Count - Lane Headers Show Correct Counts", () => {
    test("lane headers show correct issue counts", async ({ page }) => {
      await navigateToEpicView(page);

      // Epic One: 3 issues
      const epicOneLane = getEpicLane(page, "epic-1");
      await expect(epicOneLane.getByLabel("3 issues")).toBeVisible();

      // Epic Two: 2 issues
      const epicTwoLane = getEpicLane(page, "epic-2");
      await expect(epicTwoLane.getByLabel("2 issues")).toBeVisible();

      // Ungrouped: 2 issues
      const ungroupedLane = getEpicLane(page, "__ungrouped__");
      await expect(ungroupedLane.getByLabel("2 issues")).toBeVisible();
    });

    test("count remains visible when lane collapsed", async ({ page }) => {
      await navigateToEpicView(page);

      const epicOneLane = getEpicLane(page, "epic-1");
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle");

      // Collapse the lane
      await collapseToggle.click();
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true");

      // Count should still be visible
      await expect(epicOneLane.getByLabel("3 issues")).toBeVisible();
    });
  });

  test.describe("Status Columns - Each Lane Has Status Columns", () => {
    test("each epic lane contains all status columns", async ({ page }) => {
      await navigateToEpicView(page);

      // Check Epic One lane
      const epicOneLane = getEpicLane(page, "epic-1");

      await expect(
        epicOneLane.locator('section[data-status="ready"]'),
      ).toBeVisible();
      await expect(
        epicOneLane.locator('section[data-status="in_progress"]'),
      ).toBeVisible();
      await expect(
        epicOneLane.locator('section[data-status="done"]'),
      ).toBeVisible();
    });

    test("issues distributed by status within epic lane", async ({ page }) => {
      await navigateToEpicView(page);

      const epicOneLane = getEpicLane(page, "epic-1");

      // Open column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="ready"] article'),
      ).toHaveCount(1);
      await expect(
        epicOneLane
          .locator('section[data-status="ready"]')
          .getByText("Feature in Epic One (Open)"),
      ).toBeVisible();

      // In Progress column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="in_progress"] article'),
      ).toHaveCount(1);
      await expect(
        epicOneLane
          .locator('section[data-status="in_progress"]')
          .getByText("Task in Epic One (In Progress)"),
      ).toBeVisible();

      // Closed column: 1 issue
      await expect(
        epicOneLane.locator('section[data-status="done"] article'),
      ).toHaveCount(1);
      await expect(
        epicOneLane
          .locator('section[data-status="done"]')
          .getByText("Bug in Epic One (Closed)"),
      ).toBeVisible();
    });
  });

  test.describe("Drag and Drop", () => {
    test("drag issue changes status within epic lane", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = [];

      await page.route("**/api/workspaces/*/issues/*", async (route) => {
        if (route.request().method() === "PATCH") {
          const url = route.request().url();
          const body = route.request().postDataJSON() as { status?: string };
          patchCalls.push({ url, body });

          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...mockIssues[0], status: body.status },
            }),
          });
        } else {
          await route.continue();
        }
      });

      await navigateToEpicView(page);

      const epicOneLane = getEpicLane(page, "epic-1");
      const openColumn = epicOneLane.locator('section[data-status="ready"]');
      const inProgressColumn = epicOneLane.locator(
        'section[data-status="in_progress"]',
      );

      // Get the card to drag
      const cardToDrag = openColumn
        .locator("article")
        .filter({ hasText: "Feature in Epic One" });
      await expect(cardToDrag).toBeVisible();

      // Perform drag operation
      const draggable = cardToDrag.locator("..");
      const dropTarget = inProgressColumn.locator(
        '[data-droppable-id="in_progress"]',
      );

      await dragWithPointer(page, draggable, dropTarget);

      // open -> in_progress triggers AssigneePrompt; Skip preserves
      // the { status: "in_progress" } PATCH body the test asserts.
      await page.getByTestId("assignee-skip-button").click();

      await page.waitForResponse(
        (res) =>
          res.url().includes("/issues/epic-1-open") &&
          res.request().method() === "PATCH",
      );

      expect(patchCalls).toHaveLength(1);
      expect(patchCalls[0].body).toEqual({ status: "in_progress" });

      // Verify UI updated
      await expect(
        inProgressColumn.getByText("Feature in Epic One"),
      ).toBeVisible();
    });

    test("drag between epic lanes changes status but not epic", async ({
      page,
    }) => {
      // Note: This test verifies that dragging from Epic One to Epic Two
      // changes STATUS but keeps the issue in Epic One (epic assignment
      // doesn't change via drag-drop - only status does)

      const patchCalls: { url: string; body: object }[] = [];

      await page.route("**/api/workspaces/*/issues/*", async (route) => {
        if (route.request().method() === "PATCH") {
          const url = route.request().url();
          const body = route.request().postDataJSON() as Record<
            string,
            unknown
          >;
          patchCalls.push({ url, body });

          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({
              success: true,
              data: { ...mockIssues[0], ...body },
            }),
          });
        } else {
          await route.continue();
        }
      });

      await navigateToEpicView(page);

      // Epic One open column
      const epicOneLane = getEpicLane(page, "epic-1");
      const epicOneOpen = epicOneLane.locator('section[data-status="ready"]');

      // Epic Two in_progress column
      const epicTwoLane = getEpicLane(page, "epic-2");
      const epicTwoInProgress = epicTwoLane.locator(
        'section[data-status="in_progress"]',
      );

      // Get the card to drag
      const cardToDrag = epicOneOpen
        .locator("article")
        .filter({ hasText: "Feature in Epic One" });
      await expect(cardToDrag).toBeVisible();

      const draggable = cardToDrag.locator("..");
      const dropTarget = epicTwoInProgress.locator(
        '[data-droppable-id="in_progress"]',
      );

      await dragWithPointer(page, draggable, dropTarget);

      // open -> in_progress triggers AssigneePrompt; Skip preserves
      // the { status: "in_progress" } PATCH body the test asserts.
      await page.getByTestId("assignee-skip-button").click();

      await page.waitForResponse(
        (res) =>
          res.url().includes("/issues/epic-1-open") &&
          res.request().method() === "PATCH",
      );

      // Verify ONLY status changed, NOT parent
      expect(patchCalls).toHaveLength(1);
      expect(patchCalls[0].body).toEqual({ status: "in_progress" });
      // Note: parent is NOT in the PATCH body - it remains "epic-1"
    });
  });

  test.describe("Edge Cases", () => {
    test("empty issues array shows no epic lanes", async ({ page }) => {
      await setupMocks(page, []);
      await Promise.all([
        page.waitForResponse(
          (res) =>
            res.url().includes("/api/workspaces/") &&
            /\/issues(\?|$)/.test(res.url()),
        ),
        page.goto("/ws/default/kanban"),
      ]);

      // When issues is empty, IssueViewGuard renders EmptyWorkspaceBoard
      // ("No issues yet") rather than the SwimLaneBoard.
      await expect(
        page.getByRole("heading", { name: "No issues yet" }),
      ).toBeVisible();
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      await expect(lanes).toHaveCount(0);
    });

    test("single epic with all issues shows one lane", async ({ page }) => {
      const singleEpicIssues = [
        {
          id: "issue-1",
          title: "Issue One",
          status: "open",
          priority: 2,
          issue_type: "task",
          parent: "epic-only",
          parent_title: "The Only Epic",
          created_at: "2026-01-27T10:00:00Z",
          updated_at: "2026-01-27T10:00:00Z",
        },
        {
          id: "issue-2",
          title: "Issue Two",
          status: "closed",
          priority: 3,
          issue_type: "bug",
          parent: "epic-only",
          parent_title: "The Only Epic",
          created_at: "2026-01-27T11:00:00Z",
          updated_at: "2026-01-27T11:00:00Z",
        },
      ];

      await setupMocks(page, singleEpicIssues);
      await navigateToEpicView(page);

      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      await expect(lanes).toHaveCount(1);
      await expect(
        page.getByRole("heading", { name: "The Only Epic", exact: true }),
      ).toBeVisible();
    });

    test("epic lanes sorted alphabetically by title", async ({ page }) => {
      await navigateToEpicView(page);

      // Get all lanes in order
      const lanes = page.locator('[data-testid^="swim-lane-lane-epic"]');
      const laneCount = await lanes.count();

      // Get titles in order (excluding Ungrouped at end)
      const titles: string[] = [];
      for (let i = 0; i < laneCount - 1; i++) {
        const lane = lanes.nth(i);
        // Use first() to get only the lane heading, not card headings
        const heading = lane.getByRole("heading").first();
        const title = await heading.textContent();
        if (title) titles.push(title);
      }

      // Verify alphabetical order (Ungrouped excluded from this check)
      const sortedTitles = [...titles].sort();
      expect(titles).toEqual(sortedTitles);
    });

    test("switching from epic to assignee regroups issues", async ({
      page,
    }) => {
      await navigateToEpicView(page);

      // Verify epic lanes exist
      await expect(
        page.getByRole("heading", {
          name: "Epic One: Authentication",
          exact: true,
        }),
      ).toBeVisible();

      // Switch to assignee grouping via MoreFiltersMenu
      await selectGroupBy(page, "assignee");

      // Epic lanes should be gone
      await expect(
        page.getByRole("heading", {
          name: "Epic One: Authentication",
          exact: true,
        }),
      ).not.toBeVisible();

      // Assignee lanes should appear
      await expect(
        page.getByRole("heading", { name: "alice", exact: true }),
      ).toBeVisible();
      await expect(
        page.getByRole("heading", { name: "bob", exact: true }),
      ).toBeVisible();
    });

    test("default groupBy shows epic swim lanes on load", async ({ page }) => {
      // "epic" is DEFAULT_GROUP_BY, so a bare /ws/default/kanban should
      // render epic swim lanes without any ?groupBy query param.
      await setupMocks(page);
      await Promise.all([
        page.waitForResponse(
          (res) =>
            res.url().includes("/api/workspaces/") &&
            /\/issues(\?|$)/.test(res.url()),
        ),
        page.goto("/ws/default/kanban"),
      ]);

      // Verify swim lanes visible
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Verify MoreFiltersMenu shows "epic" selected
      expect(await readGroupByValue(page)).toBe("epic");

      // Verify epic lanes present
      await expect(
        page.getByRole("heading", {
          name: "Epic One: Authentication",
          exact: true,
        }),
      ).toBeVisible();
    });
  });

  test.describe("Collapse/Expand Behavior", () => {
    test("click collapse button hides lane content", async ({ page }) => {
      await navigateToEpicView(page);

      const epicOneLane = getEpicLane(page, "epic-1");
      await expect(epicOneLane).toBeVisible();

      // Verify initially expanded
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "false");

      // Click collapse toggle
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle");
      await collapseToggle.click();

      // Verify lane is collapsed
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true");

      // Lane content div should be hidden
      const laneContent = epicOneLane.locator('[data-collapsed="true"]');
      await expect(laneContent).toHaveAttribute("aria-hidden", "true");
    });

    test("expand collapsed lane shows content", async ({ page }) => {
      await navigateToEpicView(page);

      const epicOneLane = getEpicLane(page, "epic-1");

      // Collapse first
      const collapseToggle = epicOneLane.getByTestId("collapse-toggle");
      await collapseToggle.click();
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "true");

      // Click toggle again to expand
      await collapseToggle.click();
      await expect(epicOneLane).toHaveAttribute("data-collapsed", "false");

      // Content should be visible again
      const laneContent = epicOneLane.locator('[aria-hidden="false"]');
      await expect(laneContent).toBeVisible();
    });
  });
});

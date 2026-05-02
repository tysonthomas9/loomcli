import { test, expect, Page } from "@playwright/test";

const WORKSPACE_ID = "default";
const WS_API = `/api/workspaces/${WORKSPACE_ID}`;

function ok<T>(data: T) {
  return { success: true, data };
}

function workspaceData() {
  return {
    id: WORKSPACE_ID,
    name: "Default",
    path: "/tmp/default",
    repos: [],
    groups: [],
    agents: [],
    workspaces: [
      {
        id: WORKSPACE_ID,
        name: "Default",
        path: "/tmp/default",
        active: true,
        repo_count: 0,
        is_default: true,
      },
    ],
    workspace_order: [WORKSPACE_ID],
    default_workspace: "Default",
  };
}

/**
 * Mock issues for testing flat view (groupBy=none).
 * Distribution: 3 open, 1 in_progress, 1 closed (total 5)
 */
const mockIssues = [
  {
    id: "open-1",
    title: "Open Issue One",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "open-2",
    title: "Open Bug Two",
    status: "open",
    priority: 1,
    issue_type: "bug",
    assignee: "bob",
    created_at: "2026-01-27T11:00:00Z",
    updated_at: "2026-01-27T11:00:00Z",
  },
  {
    id: "open-3",
    title: "Open Feature Three",
    status: "open",
    priority: 0,
    issue_type: "feature",
    created_at: "2026-01-27T12:00:00Z",
    updated_at: "2026-01-27T12:00:00Z",
  },
  {
    id: "progress-1",
    title: "In Progress Task",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "alice",
    created_at: "2026-01-27T13:00:00Z",
    updated_at: "2026-01-27T13:00:00Z",
  },
  {
    id: "closed-1",
    title: "Closed Bug",
    status: "closed",
    priority: 3,
    issue_type: "bug",
    created_at: "2026-01-27T14:00:00Z",
    updated_at: "2026-01-27T14:00:00Z",
  },
];

// Timestamp helper for custom test data
const timestamps = {
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
};

/**
 * Set up API mocks.
 */
async function setupMocks(
  page: Page,
  issues: object[] = mockIssues,
  patchCalls?: { url: string; body: object }[],
) {
  await page.route("**/*", async (route) => {
    const request = route.request();
    const pathname = new URL(request.url()).pathname;

    if (pathname === "/api/config") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ mode: "open" }),
      });
    } else if (
      pathname === "/api/workspaces/active" ||
      pathname === `/api/workspaces/${WORKSPACE_ID}`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(workspaceData())),
      });
    } else if (
      pathname === `${WS_API}/issues` ||
      pathname === `${WS_API}/ready`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok(issues)),
      });
    } else if (pathname === `${WS_API}/stats`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          total_issues: issues.length,
          open_issues: issues.filter(
            (i) => (i as { status?: string }).status === "open",
          ).length,
          in_progress_issues: issues.filter(
            (i) => (i as { status?: string }).status === "in_progress",
          ).length,
          closed_issues: issues.filter(
            (i) => (i as { status?: string }).status === "closed",
          ).length,
          blocked_issues: 0,
          deferred_issues: 0,
          ready_issues: issues.filter(
            (i) => (i as { status?: string }).status === "open",
          ).length,
          tombstone_issues: 0,
          pinned_issues: 0,
          epics_eligible_for_closure: 0,
        }),
      });
    } else if (
      pathname === `${WS_API}/blocked` ||
      pathname === `${WS_API}/agents` ||
      pathname === `${WS_API}/terminal/sessions` ||
      pathname === `${WS_API}/terminal/sessions/by-issue` ||
      pathname === `${WS_API}/terminal/tabs`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok([])),
      });
    } else if (pathname === `${WS_API}/terminal/state`) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ active_tab: "" }),
      });
    } else if (
      request.method() === "PATCH" &&
      pathname.startsWith(`${WS_API}/issues/`)
    ) {
      const issueId = pathname.split("/").pop();
      const body = request.postDataJSON() as { status?: string };
      patchCalls?.push({ url: request.url(), body });
      const issue = issues.find(
        (i) => (i as { id: string }).id === issueId,
      ) as object;
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { ...issue, ...body, updated_at: new Date().toISOString() },
        }),
      });
    } else if (pathname.startsWith("/api/") && pathname.includes("/events")) {
      await route.abort();
    } else if (pathname === "/api/client-errors") {
      await route.fulfill({ status: 204 });
    } else if (pathname.startsWith("/api/")) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(ok({})),
      });
    } else {
      await route.continue();
    }
  });
}

/**
 * Navigate and wait for API response.
 */
async function navigateAndWait(page: Page, query = "") {
  const path = `/ws/${WORKSPACE_ID}/kanban${query}`;
  await page.goto(path);
  await expect(
    page
      .getByTestId("swim-lane-board")
      .or(page.locator('section[data-status="ready"]'))
      .or(page.getByText("No issues yet"))
      .first(),
  ).toBeVisible();
}

async function showMoreFilters(page: Page) {
  const groupByFilter = page.getByLabel("Group issues by");
  if (
    (await groupByFilter.count()) === 0 ||
    !(await groupByFilter.isVisible())
  ) {
    await page.getByRole("button", { name: "More filters" }).click();
  }
}

async function skipClaimModalIfShown(page: Page) {
  const skipButton = page.getByRole("button", { name: "Skip" });
  if (await skipButton.isVisible().catch(() => false)) {
    await skipButton.click();
  }
}

test.describe("groupBy None (Flat View)", () => {
  test.describe("Default State", () => {
    test("default page load shows epic swim lane view without groupBy URL param", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      // No groupBy param in URL
      expect(page.url()).not.toContain("groupBy");

      // Swim lane board should be visible (epic swim lanes are the default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // GroupBy dropdown shows 'epic' selected
      await showMoreFilters(page);
      await expect(page.getByLabel("Group issues by")).toHaveValue("epic");
    });

    test("navigating with explicit groupBy=none shows flat view", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // Flat view renders
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();
      await expect(page.locator('section[data-status="ready"]')).toBeVisible();

      // Dropdown shows 'none'
      await showMoreFilters(page);
      await expect(page.getByLabel("Group issues by")).toHaveValue("none");
    });
  });

  test.describe("Flat View Rendering", () => {
    test("flat view renders KanbanBoard without any swim lane elements", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // No swim lane board
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();

      // No swim lane elements at all
      const lanes = page.locator('[data-testid^="swim-lane-lane-"]');
      await expect(lanes).toHaveCount(0);

      // No collapse toggles (swim lane feature)
      const collapseToggles = page.locator('[data-testid="collapse-toggle"]');
      await expect(collapseToggles).toHaveCount(0);
    });

    test("no grouping headers shown in flat view", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // No swim lane headings (e.g., epic titles, assignee names, priority labels)
      const swimLaneHeadings = page.locator(
        '[data-testid^="swim-lane-lane-"] h3',
      );
      await expect(swimLaneHeadings).toHaveCount(0);
    });
  });

  test.describe("Status Columns", () => {
    test("standard status columns visible with correct structure", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      const openColumn = page.locator('section[data-status="ready"]');
      const inProgressColumn = page.locator(
        'section[data-status="in_progress"]',
      );
      const closedColumn = page.locator('section[data-status="done"]');

      // All three status columns visible
      await expect(openColumn).toBeVisible();
      await expect(inProgressColumn).toBeVisible();
      await expect(closedColumn).toBeVisible();

      // Verify issue counts per column (3 open, 1 in_progress, 1 closed)
      await expect(openColumn.locator("article")).toHaveCount(3);
      await expect(inProgressColumn.locator("article")).toHaveCount(1);
      await expect(closedColumn.locator("article")).toHaveCount(1);
    });

    test("issues appear in correct status columns", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      const openColumn = page.locator('section[data-status="ready"]');
      const inProgressColumn = page.locator(
        'section[data-status="in_progress"]',
      );
      const closedColumn = page.locator('section[data-status="done"]');

      // Open issues
      await expect(openColumn.getByText("Open Issue One")).toBeVisible();
      await expect(openColumn.getByText("Open Bug Two")).toBeVisible();
      await expect(openColumn.getByText("Open Feature Three")).toBeVisible();

      // In Progress issue
      await expect(
        inProgressColumn.getByText("In Progress Task"),
      ).toBeVisible();

      // Closed issue
      await expect(closedColumn.getByText("Closed Bug")).toBeVisible();
    });
  });

  test.describe("Drag and Drop", () => {
    test("drag issue from open to review updates status via API", async ({
      page,
    }) => {
      const patchCalls: { url: string; body: object }[] = [];
      await setupMocks(page, mockIssues, patchCalls);
      await navigateAndWait(page, "?groupBy=none");

      const openColumn = page.locator('section[data-status="ready"]');
      const reviewColumn = page.locator('section[data-status="review"]');

      // Initial state
      await expect(openColumn.locator("article")).toHaveCount(3);
      await expect(reviewColumn.locator("article")).toHaveCount(0);

      // Get card to drag
      const card = openColumn
        .locator("article")
        .filter({ hasText: "Open Issue One" });
      const draggable = card.locator("..");
      const dropTarget = reviewColumn.locator('[data-droppable-id="review"]');
      await reviewColumn.evaluate((el) => {
        el.scrollIntoView({ block: "nearest", inline: "center" });
      });

      const sourceBox = await draggable.boundingBox();
      const targetBox = await dropTarget.boundingBox();
      if (!sourceBox || !targetBox)
        throw new Error("Could not get bounding boxes");

      const startX = sourceBox.x + sourceBox.width / 2;
      const startY = sourceBox.y + sourceBox.height / 2;
      const endX = targetBox.x + Math.min(48, targetBox.width / 2);
      const endY = targetBox.y + Math.min(48, targetBox.height / 2);

      // Perform drag operation
      await draggable.dispatchEvent("pointerdown", {
        clientX: startX,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointermove", {
        clientX: startX + 10,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointermove", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointerup", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });

      // Wait for PATCH
      const patchResponse = page.waitForResponse(
        (res) =>
          new URL(res.url()).pathname === `${WS_API}/issues/open-1` &&
          res.request().method() === "PATCH",
      );
      await skipClaimModalIfShown(page);
      await patchResponse;

      // Verify API call
      expect(patchCalls).toHaveLength(1);
      expect(patchCalls[0].body).toEqual({ status: "review" });

      // Verify UI update
      await expect(reviewColumn.getByText("Open Issue One")).toBeVisible();
    });

    test("drag issue to closed column works", async ({ page }) => {
      const patchCalls: { url: string; body: object }[] = [];
      await setupMocks(page, mockIssues, patchCalls);
      await navigateAndWait(page, "?groupBy=none");

      const openColumn = page.locator('section[data-status="ready"]');
      const closedColumn = page.locator('section[data-status="done"]');

      const card = openColumn
        .locator("article")
        .filter({ hasText: "Open Bug Two" });
      const draggable = card.locator("..");
      const dropTarget = closedColumn.locator('[data-droppable-id="done"]');

      const sourceBox = await draggable.boundingBox();
      const targetBox = await dropTarget.boundingBox();
      if (!sourceBox || !targetBox)
        throw new Error("Could not get bounding boxes");

      const startX = sourceBox.x + sourceBox.width / 2;
      const startY = sourceBox.y + sourceBox.height / 2;
      const endX = targetBox.x + targetBox.width / 2;
      const endY = targetBox.y + targetBox.height / 2;

      await draggable.dispatchEvent("pointerdown", {
        clientX: startX,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointermove", {
        clientX: startX + 10,
        clientY: startY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointermove", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 1,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });
      await page.waitForTimeout(50);

      await page.dispatchEvent("body", "pointerup", {
        clientX: endX,
        clientY: endY,
        button: 0,
        buttons: 0,
        pointerId: 1,
        pointerType: "mouse",
        isPrimary: true,
      });

      const patchResponse = page.waitForResponse(
        (res) =>
          new URL(res.url()).pathname === `${WS_API}/issues/open-2` &&
          res.request().method() === "PATCH",
      );
      await skipClaimModalIfShown(page);
      await patchResponse;

      expect(patchCalls[0].body).toEqual({ status: "closed" });
      await expect(closedColumn.getByText("Open Bug Two")).toBeVisible();
    });
  });

  test.describe("View Switching", () => {
    test("switching from epic grouping to none shows flat view", async ({
      page,
    }) => {
      // Add parent fields for epic grouping
      const issuesWithEpic = mockIssues.map((i, idx) => ({
        ...i,
        parent: idx < 3 ? "epic-1" : undefined,
        parent_title: idx < 3 ? "Epic One" : undefined,
      }));
      await setupMocks(page, issuesWithEpic);
      await navigateAndWait(page, "?groupBy=epic");

      // Verify swim lanes exist
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true }),
      ).toBeVisible();

      // Navigate to explicit flat view. The current app default is epic when
      // groupBy is omitted, so flat view must keep groupBy=none in the URL.
      await navigateAndWait(page, "?groupBy=none");

      // Verify flat view
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();
      await expect(page.locator('section[data-status="ready"]')).toBeVisible();

      // No epic lane headers
      await expect(
        page.getByRole("heading", { name: "Epic One", exact: true }),
      ).not.toBeVisible();
    });

    test("switching from priority grouping to none hides swim lanes", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=priority");

      // Verify priority lanes exist
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();
      // Check for P2 (Medium) which is in our mock data (priority: 2)
      await expect(
        page.getByRole("heading", { name: "P2 (Medium)" }),
      ).toBeVisible();

      // Navigate to explicit flat view.
      await navigateAndWait(page, "?groupBy=none");

      // Verify flat view - no swim lane board
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();

      // No swim lane elements
      const lanes = page.locator('[data-testid^="swim-lane-lane-"]');
      await expect(lanes).toHaveCount(0);
    });

    test("switching groupBy repeatedly works correctly", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // Start flat
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();

      // Switch to priority
      await navigateAndWait(page, "?groupBy=priority");
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Back to flat
      await navigateAndWait(page, "?groupBy=none");
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();

      // To assignee
      await navigateAndWait(page, "?groupBy=assignee");
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Back to flat
      await navigateAndWait(page, "?groupBy=none");
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();

      // All status columns visible
      await expect(page.locator('section[data-status="ready"]')).toBeVisible();
    });
  });

  test.describe("URL State", () => {
    test("URL preserves groupBy=none for explicit flat view", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      await expect(page.locator('section[data-status="ready"]')).toBeVisible();
      expect(page.url()).toContain("groupBy=none");
    });

    test("page reload preserves epic swim lane view (default)", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page);

      // Verify epic swim lane view (default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Re-setup mocks before reload
      await setupMocks(page);
      await page.reload();
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Still epic swim lane view
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();
    });

    test("invalid groupBy URL param defaults to epic swim lane view", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=invalid");

      // Epic swim lane view renders (invalid param falls back to default)
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();

      // Dropdown shows 'epic' (default)
      await showMoreFilters(page);
      await expect(page.getByLabel("Group issues by")).toHaveValue("epic");
    });
  });

  test.describe("Filter Integration", () => {
    test("priority filter reduces visible issues in flat view", async ({
      page,
    }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // All 5 issues visible initially
      await expect(page.locator("article")).toHaveCount(5);

      // Filter to P2 only (2 issues: open-1 and progress-1)
      await page.getByTestId("priority-filter").selectOption("2");

      // Only P2 issues visible
      await expect(page.locator("article")).toHaveCount(2);
      await expect(page.getByText("Open Issue One")).toBeVisible();
      await expect(page.getByText("In Progress Task")).toBeVisible();
    });

    test("type filter works with flat view", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      // Filter to bugs only (2 bugs: open-2 and closed-1)
      await page.getByTestId("type-filter").selectOption("bug");

      await expect(page.locator("article")).toHaveCount(2);
      await expect(page.getByText("Open Bug Two")).toBeVisible();
      await expect(page.getByText("Closed Bug")).toBeVisible();
    });

    test("clear filters restores all issues in flat view", async ({ page }) => {
      await setupMocks(page);
      await navigateAndWait(page, "?groupBy=none");

      const clearButton = page.getByTestId("clear-filters");
      const priorityFilter = page.getByTestId("priority-filter");

      // Explicit groupBy=none is represented as an active URL filter in the
      // current app, so the clear button is visible before priority changes.
      await expect(clearButton).toBeVisible();

      // Apply filter
      await priorityFilter.selectOption("0");
      await expect(page.locator("article")).toHaveCount(1);

      // Wait for clear button to become visible
      await expect(clearButton).toBeVisible();

      // Click clear button using JS to bypass element interception
      await clearButton.evaluate((button: HTMLElement) => button.click());

      // Wait for URL to clear priority and groupBy params.
      await expect(async () => {
        expect(page.url()).not.toContain("priority=");
        expect(page.url()).not.toContain("groupBy=");
      }).toPass({ timeout: 2000 });

      // Dropdown should show "All" option
      await expect(priorityFilter).toHaveValue("");

      // All issues restored under the current default grouped view
      await expect(page.locator("article")).toHaveCount(5);
      await expect(page.getByTestId("swim-lane-board")).toBeVisible();
    });
  });

  test.describe("Edge Cases", () => {
    test("empty issues shows empty status columns", async ({ page }) => {
      await setupMocks(page, []);
      await navigateAndWait(page, "?groupBy=none");

      await expect(page.getByText("No issues yet")).toBeVisible();
      await expect(page.locator("article")).toHaveCount(0);
      await expect(page.getByTestId("swim-lane-board")).not.toBeVisible();
    });

    test("all issues in one status column works correctly", async ({
      page,
    }) => {
      const allOpenIssues = [
        {
          id: "o1",
          title: "Issue 1",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
        {
          id: "o2",
          title: "Issue 2",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
        {
          id: "o3",
          title: "Issue 3",
          status: "open",
          priority: 2,
          issue_type: "task",
          ...timestamps,
        },
      ];
      await setupMocks(page, allOpenIssues);
      await navigateAndWait(page, "?groupBy=none");

      const openColumn = page.locator('section[data-status="ready"]');
      const inProgressColumn = page.locator(
        'section[data-status="in_progress"]',
      );
      const closedColumn = page.locator('section[data-status="done"]');

      // All in open
      await expect(openColumn.locator("article")).toHaveCount(3);
      await expect(inProgressColumn.locator("article")).toHaveCount(0);
      await expect(closedColumn.locator("article")).toHaveCount(0);
    });
  });
});

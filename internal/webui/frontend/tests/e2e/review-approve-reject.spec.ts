import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Review and approve/reject agent plan.
 *
 * Tests the full user journey of a project lead reviewing an agent's plan
 * through the Kanban board and IssueDetailPanel:
 *   1. Approve a plan review (panel opens, design renders, approve updates board)
 *   2. Reject a plan review (reject form, Ctrl+Enter submit, issue leaves Review column)
 *   3. Reject form cancel returns to action bar
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

// -- Issue mock data --

const reviewIssue1 = {
  id: "review-plan-001",
  title: "Agent Plan: Add caching layer",
  status: "review",
  priority: 1,
  issue_type: "task",
  created_at: "2026-01-15T10:00:00Z",
  updated_at: "2026-01-15T10:00:00Z",
};

const reviewIssue2 = {
  id: "review-plan-002",
  title: "Agent Plan: Migrate to new API",
  status: "review",
  priority: 2,
  issue_type: "task",
  created_at: "2026-01-15T10:00:00Z",
  updated_at: "2026-01-15T10:00:00Z",
};

const reviewDetails1 = {
  ...reviewIssue1,
  description: "Add Redis caching to reduce API latency",
  design:
    "## Approach\n\nUse Redis as a write-through cache.\n\n### Files to modify\n\n- `src/api/cache.ts`\n- `src/middleware/cache.ts`\n\n```typescript\nconst cache = new RedisCache(config);\n```\n\n### Testing\n\n1. Unit tests for cache invalidation\n2. Integration tests with Redis testcontainer",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
};

const reviewDetails2 = {
  ...reviewIssue2,
  description: "Migrate from v1 to v2 API endpoints",
  design: "## Migration Plan\n\n- Phase 1: Add v2 routes\n- Phase 2: Deprecate v1",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
};

// -- Common mock setup --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

/**
 * Set up all baseline API mocks required for the app to boot.
 * Uses workspace-scoped URL patterns matching the actual API paths.
 */
async function setupBaseMocks(page: Page) {
  // App config (auth mode discovery) — bootstrap fetch in main.tsx.
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

  // Workspace metadata (both /active and /default for redirect + validation)
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    });
  });
  await page.route("**/api/workspaces/default", async (route) => {
    // Only handle exact workspace metadata requests, not sub-resource paths
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

  // Health check — daemon must appear available to avoid the overlay
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
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

  // Monitor agent endpoints — return empty data
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [] }),
    });
  });

  // Stats endpoint (workspace-scoped: /api/workspaces/default/stats)
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 0,
        open_issues: 0,
        in_progress_issues: 0,
        closed_issues: 0,
        blocked_issues: 0,
        deferred_issues: 0,
        ready_issues: 0,
        tombstone_issues: 0,
        pinned_issues: 0,
        epics_eligible_for_closure: 0,
        average_lead_time_hours: 0,
      }),
    });
  });

  // Blocked endpoint (workspace-scoped: /api/workspaces/default/blocked)
  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  // Terminal sessions-by-issue endpoint (workspace-scoped)
  await page.route("**/workspaces/*/terminal/sessions/by-issue", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({}),
    });
  });

  // SSE events endpoint (workspace-scoped: /api/workspaces/default/events)
  // Pattern must NOT match Vite module paths like /src/api/events.ts
  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body: "event: connected\ndata: {\"message\":\"connected\"}\n\n",
    });
  });
}

/**
 * Install a browser-level fetch interceptor for the issues list endpoint.
 * This runs BEFORE React mounts, so responses are delivered synchronously
 * and are immune to React StrictMode's AbortController cleanup.
 *
 * The interceptor stores issues data on window.__mockIssues, which can be
 * updated from the test via page.evaluate to change what subsequent
 * fetches return.
 */
async function installIssuesMock(page: Page, initialIssues: unknown[]) {
  await page.addInitScript(
    (issues: unknown[]) => {
      // Store mock data on window for dynamic updates
      (window as any).__mockIssues = issues;

      const originalFetch = window.fetch.bind(window);
      window.fetch = function (
        input: RequestInfo | URL,
        init?: RequestInit,
      ): Promise<Response> {
        const url =
          input instanceof Request
            ? input.url
            : typeof input === "string"
              ? input
              : input.toString();
        // Match: /api/workspaces/{id}/issues?... but NOT /api/workspaces/{id}/issues/{id}
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
    initialIssues,
  );
}

/**
 * Navigate directly to the workspace and wait for the board to be ready.
 */
async function navigateAndWaitForBoard(page: Page) {
  await page.goto("/ws/default/kanban?groupBy=none", {
    waitUntil: "domcontentloaded",
  });
}

// -- Tests --

test.describe("E2E Journey: Review and approve/reject agent plan", () => {
  test("approve plan review: panel opens, design renders, approve updates board", async ({
    page,
  }) => {
    // Install browser-level issues mock (immune to StrictMode abort)
    await installIssuesMock(page, [reviewIssue1, reviewIssue2]);

    // Set up base mocks
    await setupBaseMocks(page);

    // Mock GET /api/workspaces/default/issues/review-plan-001
    await page.route("**/workspaces/*/issues/review-plan-001", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(reviewDetails1),
        });
      } else {
        await route.fallback();
      }
    });

    // Mock PATCH /api/workspaces/default/issues/review-plan-001
    const patchCalls: Array<Record<string, unknown>> = [];
    await page.route("**/workspaces/*/issues/review-plan-001", async (route) => {
      if (route.request().method() === "PATCH") {
        patchCalls.push(route.request().postDataJSON());
        // Update browser mock data so refetch returns updated list
        await page.evaluate((data) => {
          (window as any).__mockIssues = data;
        }, [reviewIssue2]);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ ...reviewIssue1, status: "open" }),
        });
      } else {
        await route.fallback();
      }
    });

    // Navigate and wait for board
    await navigateAndWaitForBoard(page);

    // Wait for board to render with review issues
    const reviewColumn = page.locator('section[data-status="review"]');
    await expect(reviewColumn).toBeVisible();
    await expect(reviewColumn.locator("article")).toHaveCount(2);

    // Click review-plan-001 card → panel opens
    const card = page
      .locator("article")
      .filter({ hasText: "Agent Plan: Add caching layer" });
    await expect(card).toBeVisible();
    await card.click();

    // Wait for panel to open and data to load
    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");
    await expect(page.getByTestId("issue-id")).toContainText("review-plan-001");

    // Verify review action bar is visible with Approve/Reject buttons
    await expect(page.getByTestId("review-action-bar")).toBeVisible();
    await expect(page.getByTestId("panel-approve-button")).toBeVisible();
    await expect(page.getByTestId("panel-reject-button")).toBeVisible();

    // Verify design markdown renders (check for heading from design content)
    await expect(page.getByText("Approach")).toBeVisible();

    // Click Approve button
    await page.getByTestId("panel-approve-button").click();

    // Wait for PATCH API call
    await page.waitForResponse(
      (res) =>
        res.url().includes("/issues/review-plan-001") &&
        res.request().method() === "PATCH",
    );

    // Verify PATCH body: plan approval sets status to "open" AND clears the
    // rejection marker. Removal is unconditional — this fixture was never
    // rejected, and sending it anyway is an idempotent no-op server-side.
    // Asserting it here is deliberate: the previous exact-match on
    // { status: "open" } is what locked the one-way behaviour in place.
    expect(patchCalls).toHaveLength(1);
    expect(patchCalls[0]).toEqual({
      status: "open",
      remove_labels: ["needs-revision"],
    });

    // Panel should close after approve
    await expect(panel).toHaveAttribute("data-state", "closed");

    // Board refetches — Review column should now have 1 card
    await expect(reviewColumn.locator("article")).toHaveCount(1);
    await expect(
      reviewColumn.getByText("Agent Plan: Migrate to new API"),
    ).toBeVisible();
  });

  // The reject -> approve round trip. Previously untested: the approve fixture
  // carried no labels, so nothing exercised approving an issue that had ALREADY
  // been rejected — the only case where the loop bites. Without remove_labels
  // the issue returns to `open` still carrying needs-revision, the planner
  // re-claims it (NeedsPlan matches on that label), and the human is asked to
  // approve the same plan again indefinitely.
  test("approve a previously-rejected plan clears needs-revision", async ({
    page,
  }) => {
    const rejectedIssue = { ...reviewIssue1, labels: ["needs-revision"] };
    const rejectedDetails = { ...reviewDetails1, labels: ["needs-revision"] };

    await installIssuesMock(page, [rejectedIssue, reviewIssue2]);
    await setupBaseMocks(page);

    await page.route("**/workspaces/*/issues/review-plan-001", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(rejectedDetails),
        });
      } else {
        await route.fallback();
      }
    });

    const patchCalls: Array<Record<string, unknown>> = [];
    await page.route("**/workspaces/*/issues/review-plan-001", async (route) => {
      if (route.request().method() === "PATCH") {
        patchCalls.push(route.request().postDataJSON());
        // The server applied the delta: status open, label gone.
        await page.evaluate((data) => {
          (window as any).__mockIssues = data;
        }, [reviewIssue2]);
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({ ...reviewIssue1, status: "open", labels: [] }),
        });
      } else {
        await route.fallback();
      }
    });

    await navigateAndWaitForBoard(page);

    const reviewColumn = page.locator('section[data-status="review"]');
    await expect(reviewColumn).toBeVisible();

    const card = page
      .locator("article")
      .filter({ hasText: "Agent Plan: Add caching layer" });
    await expect(card).toBeVisible();
    await card.click();

    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");
    await page.getByTestId("panel-approve-button").click();

    await page.waitForResponse(
      (res) =>
        res.url().includes("/issues/review-plan-001") &&
        res.request().method() === "PATCH",
    );

    // The label must be cleared in the SAME call that reopens the issue.
    // Reopening without clearing is exactly the non-terminating state.
    expect(patchCalls).toHaveLength(1);
    expect(patchCalls[0]).toEqual({
      status: "open",
      remove_labels: ["needs-revision"],
    });

    // And it leaves the Review column rather than coming back around.
    await expect(reviewColumn.locator("article")).toHaveCount(1);
  });

  test("reject plan review: reject form, Ctrl+Enter submit, issue leaves Review column", async ({
    page,
  }) => {
    // Install browser-level issues mock (immune to StrictMode abort)
    await installIssuesMock(page, [reviewIssue2]);

    // Set up base mocks
    await setupBaseMocks(page);

    // Mock GET issue detail for review-plan-002
    await page.route("**/workspaces/*/issues/review-plan-002", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(reviewDetails2),
        });
      } else {
        await route.fallback();
      }
    });

    // Mock POST /api/workspaces/default/issues/review-plan-002/comments
    const commentCalls: Array<Record<string, unknown>> = [];
    await page.route("**/workspaces/*/issues/review-plan-002/comments", async (route) => {
      if (route.request().method() === "POST") {
        commentCalls.push(route.request().postDataJSON());
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            id: 1,
            issue_id: "review-plan-002",
            author: "test-user",
            text: route.request().postDataJSON().text,
            created_at: new Date().toISOString(),
          }),
        });
      } else {
        await route.fallback();
      }
    });

    // Mock PATCH /api/workspaces/default/issues/review-plan-002
    const patchCalls: Array<Record<string, unknown>> = [];
    await page.route("**/workspaces/*/issues/review-plan-002", async (route) => {
      if (route.request().method() === "PATCH") {
        patchCalls.push(route.request().postDataJSON());
        // Update browser mock data so refetch returns empty list
        await page.evaluate(() => {
          (window as any).__mockIssues = [];
        });
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok({
            ...reviewIssue2,
            status: "open",
            labels: ["needs-revision"],
          }),
        });
      } else {
        await route.fallback();
      }
    });

    // Navigate and wait for board
    await navigateAndWaitForBoard(page);

    // Wait for board to render
    const reviewColumn = page.locator('section[data-status="review"]');
    await expect(reviewColumn).toBeVisible();
    await expect(reviewColumn.locator("article")).toHaveCount(1);

    // Click review-plan-002 card → panel opens
    const card = page
      .locator("article")
      .filter({ hasText: "Agent Plan: Migrate to new API" });
    await expect(card).toBeVisible();
    await card.click();

    // Wait for panel to open
    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");
    await expect(page.getByTestId("issue-id")).toContainText("review-plan-002");

    // Verify review action bar is visible
    await expect(page.getByTestId("review-action-bar")).toBeVisible();

    // Click Reject button → RejectCommentForm appears
    await page.getByTestId("panel-reject-button").click();
    await expect(page.getByTestId("reject-comment-form")).toBeVisible();

    // Verify action bar is hidden while reject form is showing
    await expect(page.getByTestId("review-action-bar")).not.toBeVisible();

    // Type feedback in textarea
    const textarea = page.getByTestId("reject-textarea");
    await expect(textarea).toBeVisible();
    await textarea.fill("Design needs more error handling detail");

    // Submit with Ctrl+Enter (Linux - component handles both metaKey and ctrlKey)
    const commentResponse = page.waitForResponse(
      (res) =>
        res.url().includes("/issues/review-plan-002/comments") &&
        res.request().method() === "POST",
    );
    const patchResponse = page.waitForResponse(
      (res) =>
        res.url().includes("/issues/review-plan-002") &&
        res.request().method() === "PATCH",
    );
    await textarea.press("Control+Enter");

    // Wait for comment POST
    await commentResponse;

    // Wait for PATCH update
    await patchResponse;

    // Verify comment POST body: "FEEDBACK: <user's text>"
    expect(commentCalls).toHaveLength(1);
    expect(commentCalls[0].text).toBe(
      "FEEDBACK: Design needs more error handling detail",
    );

    // Verify PATCH body: status=open with needs-revision label
    expect(patchCalls).toHaveLength(1);
    expect(patchCalls[0]).toEqual({
      status: "open",
      add_labels: ["needs-revision"],
    });

    // Panel should close after reject (may be removed from DOM or closed)
    await expect(panel).not.toBeVisible();

    // Board refetches — Review column should now have 0 cards
    await expect(reviewColumn.locator("article")).toHaveCount(0);
  });

  test("reject form cancel returns to action bar", async ({ page }) => {
    // Install browser-level issues mock (immune to StrictMode abort)
    await installIssuesMock(page, [reviewIssue1]);

    // Set up base mocks
    await setupBaseMocks(page);

    // Mock GET issue detail for review-plan-001
    await page.route("**/workspaces/*/issues/review-plan-001", async (route) => {
      if (route.request().method() === "GET") {
        await route.fulfill({
          status: 200,
          contentType: "application/json",
          body: ok(reviewDetails1),
        });
      } else {
        await route.fallback();
      }
    });

    // Navigate and wait for board
    await navigateAndWaitForBoard(page);

    // Click review card → panel opens
    const card = page
      .locator("article")
      .filter({ hasText: "Agent Plan: Add caching layer" });
    await expect(card).toBeVisible();
    await card.click();

    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Click Reject → form appears
    await page.getByTestId("panel-reject-button").click();
    await expect(page.getByTestId("reject-comment-form")).toBeVisible();
    await expect(page.getByTestId("review-action-bar")).not.toBeVisible();

    // Click Cancel → form hides, action bar returns
    await page.getByTestId("reject-cancel").click();
    await expect(page.getByTestId("reject-comment-form")).not.toBeVisible();
    await expect(page.getByTestId("review-action-bar")).toBeVisible();
    await expect(page.getByTestId("panel-approve-button")).toBeVisible();
    await expect(page.getByTestId("panel-reject-button")).toBeVisible();
  });
});

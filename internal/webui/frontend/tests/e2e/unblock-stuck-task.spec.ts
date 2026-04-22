import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Unblock stuck task via dependency navigation.
 *
 * Uses serial mode with shared page (following journey-project-status.spec.ts
 * pattern) to avoid React StrictMode double-mount issues with AbortController.
 */

// -- Mock data --

const WORKSPACE_ID = "test-ws";

const mockWorkspace = {
  id: WORKSPACE_ID,
  name: "Test Workspace",
  path: "/tmp/test-ws",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WORKSPACE_ID,
      name: "Test Workspace",
      path: "/tmp/test-ws",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: [WORKSPACE_ID],
  default_workspace: WORKSPACE_ID,
};

const blockerIssue = {
  id: "blocker-1",
  title: "Setup CI pipeline for staging",
  status: "in_progress",
  priority: 3,
  issue_type: "task",
  assignee: "alice",
  created_at: "2026-01-27T10:00:00Z",
  updated_at: "2026-01-27T10:00:00Z",
};

const blockedIssue = {
  id: "blocked-1",
  title: "Deploy feature flags to staging",
  status: "blocked",
  priority: 1,
  issue_type: "task",
  assignee: "bob",
  created_at: "2026-01-27T11:00:00Z",
  updated_at: "2026-01-27T11:00:00Z",
};

const blockedIssueDetail = {
  ...blockedIssue,
  dependencies: [{ ...blockerIssue, dependency_type: "blocks" }],
  dependents: [],
  comments: [],
};

const blockerIssueDetail = {
  ...blockerIssue,
  dependencies: [],
  dependents: [{ ...blockedIssue, dependency_type: "blocks" }],
  comments: [],
};

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

interface TrackedCall {
  method: string;
  url: string;
  body?: unknown;
}

// -- Setup --

/**
 * Install browser-level fetch interceptor for ALL workspace data endpoints.
 * This is the only reliable way to mock fetch in Playwright with React StrictMode,
 * since page.route operates at the CDP level and the synthetic responses may
 * not integrate correctly with the browser's fetch/Response API.
 */
async function installDataInterceptor(page: Page) {
  await page.addInitScript(
    (params: {
      issues: unknown[];
      blockedDetail: unknown;
      blockerDetail: unknown;
      blockedIssues: unknown[];
    }) => {
      const originalFetch = window.fetch.bind(window);
      window.fetch = function (
        input: RequestInfo | URL,
        init?: RequestInit,
      ): Promise<Response> {
        const url = typeof input === "string" ? input : input.toString();
        const method = (init?.method ?? "GET").toUpperCase();

        if (method !== "GET") return originalFetch(input, init);

        // Ready endpoint
        if (/\/api\/workspaces\/[^/]+\/ready(\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ success: true, data: params.issues }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }

        // Issues list endpoint
        if (/\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ success: true, data: params.issues }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }

        // Issue detail endpoint — getIssue() calls unwrap() expecting { success, data }
        // Must NOT match /issues/{id}/events or /issues/{id}/dependencies sub-paths
        const detailMatch = url.match(
          /\/api\/workspaces\/[^/]+\/issues\/([^/?]+)(?:\?|$)/,
        );
        if (detailMatch) {
          const issueId = decodeURIComponent(detailMatch[1]);
          const detail =
            issueId === "blocked-1"
              ? params.blockedDetail
              : issueId === "blocker-1"
                ? params.blockerDetail
                : null;

          if (detail) {
            const body = JSON.stringify({ success: true, data: detail });
            return new Promise<Response>((resolve) => {
              resolve(new Response(body, { status: 200 }));
            });
          }
        }

        // Issue events endpoint (/issues/{id}/events)
        if (/\/api\/workspaces\/[^/]+\/issues\/[^/]+\/events/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ success: true, data: [] }),
              {
                status: 200,
                headers: { "Content-Type": "application/json" },
              },
            ),
          );
        }

        // Blocked endpoint
        if (/\/api\/workspaces\/[^/]+\/blocked/.test(url)) {
          return Promise.resolve(
            new Response(
              JSON.stringify({ success: true, data: params.blockedIssues }),
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
    {
      issues: [blockerIssue, blockedIssue],
      blockedDetail: blockedIssueDetail,
      blockerDetail: blockerIssueDetail,
      blockedIssues: [
        {
          ...blockedIssue,
          blocked_by_count: 1,
          blocked_by: ["blocker-1"],
          blocked_by_details: [
            {
              id: "blocker-1",
              title: "Setup CI pipeline for staging",
              priority: 3,
            },
          ],
        },
      ],
    },
  );
}

async function setupInfrastructureMocks(page: Page) {
  await page.route("**/api/workspaces/*/events/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-sse-token" }),
    });
  });

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
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

  await page.route("**/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspace),
    });
  });

  await page.route(`**/api/workspaces/${WORKSPACE_ID}`, async (route) => {
    const url = new URL(route.request().url());
    if (
      url.pathname === `/api/workspaces/${WORKSPACE_ID}` ||
      url.pathname === `/api/workspaces/${WORKSPACE_ID}/`
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockWorkspace),
      });
    } else {
      await route.fallback();
    }
  });

  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: { "Cache-Control": "no-cache", Connection: "keep-alive" },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });

  await page.route(`**/workspaces/${WORKSPACE_ID}/stats`, async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 2,
        open_issues: 0,
        in_progress_issues: 1,
        closed_issues: 0,
        blocked_issues: 1,
        deferred_issues: 0,
        ready_issues: 2,
        tombstone_issues: 0,
        pinned_issues: 0,
        epics_eligible_for_closure: 0,
        average_lead_time_hours: 0,
      }),
    });
  });

  await page.route(
    `**/workspaces/${WORKSPACE_ID}/terminal/**`,
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok([]),
      });
    },
  );

  await page.route(
    `**/workspaces/${WORKSPACE_ID}/config/**`,
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({ backends: [] }),
      });
    },
  );
}

// -- Tests (serial mode with shared page) --

test.describe("E2E Journey: Unblock stuck task via dependency navigation", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;
  const patchCalls: TrackedCall[] = [];

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();

    // Install data interceptor (addInitScript must be before any navigation)
    await installDataInterceptor(page);

    // Set up infrastructure mocks
    await setupInfrastructureMocks(page);

    // PATCH handler for issue updates (priority changes)
    await page.route(
      `**/workspaces/${WORKSPACE_ID}/issues/**`,
      async (route) => {
        if (route.request().method() === "PATCH") {
          const url = route.request().url();
          const body = JSON.parse(
            (await route.request().postData()) || "{}",
          );
          patchCalls.push({ method: "PATCH", url, body });

          const idMatch = url.match(/\/issues\/([^/?]+)/);
          const issueId = idMatch ? idMatch[1] : null;
          const base =
            issueId === "blocker-1" ? blockerIssue : blockedIssue;

          await route.fulfill({
            status: 200,
            contentType: "application/json",
            body: JSON.stringify({ ...base, ...body }),
          });
        } else {
          // GET requests handled by addInitScript interceptor
          await route.continue();
        }
      },
    );

    // Navigate to table view
    await page.goto(`/ws/${WORKSPACE_ID}/?view=table`, {
      waitUntil: "domcontentloaded",
    });

    // Wait for table data to load
    await expect(
      page
        .locator("tr")
        .filter({ hasText: "Deploy feature flags to staging" }),
    ).toBeVisible({ timeout: 15000 });
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("table shows both issues including blocked status", async () => {
    await expect(
      page
        .locator("tr")
        .filter({ hasText: "Deploy feature flags to staging" }),
    ).toBeVisible();
    await expect(
      page
        .locator("tr")
        .filter({ hasText: "Setup CI pipeline for staging" }),
    ).toBeVisible();

    await expect(page.getByTestId("issue-table")).toBeVisible();

    const blockedRow = page.getByTestId("issue-row-blocked-1");
    await expect(blockedRow).toBeVisible();
    const hasBlockedClass = await blockedRow.evaluate((el) =>
      el.classList.contains("issue-table__row--blocked"),
    );
    expect(hasBlockedClass).toBe(true);

    await page.screenshot({ path: "blocked-issue-table.png" });
  });

  test("click blocked issue opens panel with dependency chips and blocking banner", async () => {
    // Click the blocked issue row
    await page
      .locator("tr")
      .filter({ hasText: "Deploy feature flags to staging" })
      .click();

    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open", {
      timeout: 10000,
    });
    await expect(panel).toHaveAttribute("data-loading", "false", {
      timeout: 10000,
    });

    // Verify panel title
    await expect(
      panel.locator("text=Deploy feature flags to staging"),
    ).toBeVisible({ timeout: 10000 });

    // Verify blocking banner
    const banner = page.getByTestId("blocking-banner");
    await expect(banner).toBeVisible({ timeout: 10000 });
    await expect(banner).toContainText("Blocked by 1 issue");

    // Verify dependency section and chip
    await expect(page.getByTestId("dependency-section")).toBeVisible();
    const depChip = page.getByTestId("dependency-item-blocker-1");
    await expect(depChip).toBeVisible();
    await expect(depChip).toContainText("blocker-1");
    await expect(depChip).toContainText("Setup CI pipeline for staging");

    await page.screenshot({
      path: "blocked-issue-panel-dependencies.png",
    });
  });

  test("click dependency chip navigates to blocking issue", async () => {
    // Click the dependency chip
    const depChip = page.getByTestId("dependency-item-blocker-1");
    await expect(depChip).toBeVisible({ timeout: 5000 });
    await depChip.click();

    const panel = page.getByTestId("issue-detail-panel");

    // Verify panel shows blocker issue
    await expect(
      panel.locator("text=Setup CI pipeline for staging"),
    ).toBeVisible({ timeout: 10000 });

    // Verify blocking banner is NOT visible (status=in_progress, not blocked)
    await expect(page.getByTestId("blocking-banner")).not.toBeVisible();

    // Verify dependents section shows the blocked issue
    const blocksSection = page
      .locator("section")
      .filter({ hasText: /^Blocks/ });
    await expect(blocksSection).toBeVisible({ timeout: 5000 });
    await expect(blocksSection).toContainText(
      "Deploy feature flags to staging",
    );

    await page.screenshot({ path: "blocker-issue-panel.png" });
  });

  test("change blocking issue priority via PriorityDropdown", async () => {
    // Open priority dropdown
    const trigger = page.getByTestId("priority-dropdown-trigger");
    await expect(trigger).toBeVisible({ timeout: 10000 });
    await trigger.click();

    const menu = page.getByTestId("priority-dropdown-menu");
    await expect(menu).toBeVisible({ timeout: 5000 });

    // Click P1 option
    const [patchResponse] = await Promise.all([
      page.waitForResponse(
        (res) =>
          res.url().includes(`/issues/blocker-1`) &&
          res.request().method() === "PATCH",
        { timeout: 10000 },
      ),
      page.getByTestId("priority-option-1").click(),
    ]);
    expect(patchResponse.ok()).toBe(true);

    // Verify PATCH was captured
    expect(patchCalls.length).toBeGreaterThanOrEqual(1);
    const lastPatch = patchCalls[patchCalls.length - 1];
    expect(lastPatch.url).toContain(`/issues/blocker-1`);
    expect(lastPatch.body).toEqual(
      expect.objectContaining({ priority: 1 }),
    );
  });

  test("close panel and verify table is intact", async () => {
    // Close panel via Escape
    await page.keyboard.press("Escape");

    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "closed", {
      timeout: 5000,
    });

    // Verify table still shows both issues
    await expect(
      page
        .locator("tr")
        .filter({ hasText: "Deploy feature flags to staging" }),
    ).toBeVisible();
    await expect(
      page
        .locator("tr")
        .filter({ hasText: "Setup CI pipeline for staging" }),
    ).toBeVisible();

    await expect(page.getByTestId("issue-row-blocked-1")).toBeVisible();
  });

  test("re-open blocked issue after navigation round-trip", async () => {
    // Click the blocked issue row again
    await page
      .locator("tr")
      .filter({ hasText: "Deploy feature flags to staging" })
      .click();

    const panel = page.getByTestId("issue-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open", {
      timeout: 10000,
    });
    await expect(panel).toHaveAttribute("data-loading", "false", {
      timeout: 10000,
    });

    // Verify panel shows the blocked issue
    await expect(
      panel.locator("text=Deploy feature flags to staging"),
    ).toBeVisible({ timeout: 10000 });

    // Verify dependency section still shows blocker chip
    await expect(page.getByTestId("dependency-section")).toBeVisible();
    await expect(
      page.getByTestId("dependency-item-blocker-1"),
    ).toBeVisible();

    // Verify blocking banner
    const banner = page.getByTestId("blocking-banner");
    await expect(banner).toBeVisible();
    await expect(banner).toContainText("Blocked by 1 issue");
  });
});

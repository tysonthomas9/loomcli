import { test, expect, Page } from "@playwright/test";

/**
 * E2E: agent status row on kanban IssueCards.
 *
 * Covers VAL-FE-KANBAN-023 — the AgentRow must render on in-progress and
 * review cards when the issue has an assignee, degrade gracefully when no
 * matching agent is in the loom-status store, and stay hidden on other
 * columns or when the assignee is missing.
 */

const WS_ID = "default";

const mockWorkspaceData = {
  id: WS_ID,
  name: "default",
  path: "/test",
  repos: [],
  groups: [],
  agents: [],
  workspaces: [
    {
      id: WS_ID,
      name: "default",
      path: "/test",
      active: true,
      repo_count: 0,
      is_default: true,
    },
  ],
  workspace_order: [WS_ID],
  default_workspace: WS_ID,
};

// Two in-progress issues: one assignee matches an agent (nova), the other
// (ghost) has no matching agent — exercises the "name-only" fallback path.
const mockIssues = [
  {
    id: "ip-with-agent",
    title: "In Progress With Agent",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "nova",
    created_at: "2026-01-27T10:00:00Z",
    updated_at: "2026-01-27T10:00:00Z",
  },
  {
    id: "ip-no-match",
    title: "In Progress No Match",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    assignee: "ghost",
    created_at: "2026-01-27T10:01:00Z",
    updated_at: "2026-01-27T10:01:00Z",
  },
  {
    id: "ip-no-assignee",
    title: "In Progress No Assignee",
    status: "in_progress",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-27T10:02:00Z",
    updated_at: "2026-01-27T10:02:00Z",
  },
  {
    id: "review-with-assignee",
    title: "Review With Assignee",
    status: "review",
    priority: 2,
    issue_type: "task",
    assignee: "nova",
    created_at: "2026-01-27T10:03:00Z",
    updated_at: "2026-01-27T10:03:00Z",
  },
  {
    id: "open-with-assignee",
    title: "Open With Assignee",
    status: "open",
    priority: 2,
    issue_type: "task",
    assignee: "nova",
    created_at: "2026-01-27T10:04:00Z",
    updated_at: "2026-01-27T10:04:00Z",
  },
  {
    id: "done-with-assignee",
    title: "Done With Assignee",
    status: "closed",
    priority: 3,
    issue_type: "task",
    assignee: "nova",
    created_at: "2026-01-27T10:05:00Z",
    updated_at: "2026-01-27T10:05:00Z",
  },
];

const mockAgents = [
  {
    name: "nova",
    status: "working: ip-with-agent (2m)",
    branch: "nova",
    path: "/tmp/worktrees/nova",
    repo: "loomcli",
    cross_repo: false,
    ahead: 1,
    behind: 0,
    role: "task",
    commits: [],
    changes: [],
  },
];

const mockStatus = {
  agents: mockAgents,
  tasks: {
    needs_planning: 0,
    ready_to_implement: 0,
    in_progress: 2,
    need_review: 1,
    backlog: 0,
  },
  in_progress_list: [],
  agent_tasks: {
    nova: {
      id: "ip-with-agent",
      title: "In Progress With Agent",
      priority: 2,
    },
  },
  stats: {
    open: 1,
    closed: 1,
    total: 6,
    completion: 17,
    remaining: 5,
    in_progress: 2,
    review: 1,
    blocked: 0,
  },
  sync: {
    db_synced: true,
    db_last_sync: "2026-01-27T10:00:00Z",
    git_needs_push: 0,
    git_needs_pull: 0,
    git_push_details: [],
  },
  timestamp: "2026-01-27T10:00:00Z",
};

async function setupMocks(page: Page) {
  // Neutralize AbortController signals. React StrictMode in dev double-fires
  // effects and the cleanup aborts in-flight fetches before route.fulfill()
  // dispatches — stripping the signal keeps mocks reliable. (Mirrors the
  // pattern used in groupby-epic.spec.ts.)
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

  await page.route("**/api/config", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ mode: "open" }),
    });
  });

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token-e2e" }),
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

  // Playwright routes are LIFO — the catch-all MUST be registered first so
  // the specific /agents and /status handlers (registered after) take priority.
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  // Loom status — provides the agent store with matching "nova" agent data
  await page.route("**/api/monitor/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: mockAgents,
        timestamp: "2026-01-27T10:00:00Z",
      }),
    });
  });

  await page.route("**/api/monitor/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockStatus),
    });
  });

  // Workspace-scoped endpoints
  await page.route("**/api/workspaces/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    const pathname = url.pathname;
    const method = request.method();

    if (/\/api\/workspaces\/[^/]+\/events/.test(pathname)) {
      await route.abort();
      return;
    }

    if (
      (/\/api\/workspaces\/[^/]+\/issues$/.test(pathname) ||
        /\/api\/workspaces\/[^/]+\/ready$/.test(pathname)) &&
      method === "GET"
    ) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      });
      return;
    }

    if (/\/api\/workspaces\/[^/]+\/stats$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({
          success: true,
          data: { open: 1, closed: 1, total: 6, completion: 17 },
        }),
      });
      return;
    }

    if (/\/api\/workspaces\/[^/]+\/blocked$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: [] }),
      });
      return;
    }

    if (/\/api\/workspaces\/[^/]+\/issues\/graph$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockIssues }),
      });
      return;
    }

    if (/^\/api\/workspaces\/[^/]+\/?$/.test(pathname)) {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify({ success: true, data: mockWorkspaceData }),
      });
      return;
    }

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ success: true, data: [] }),
    });
  });
}

async function navigateToKanban(page: Page) {
  await Promise.all([
    page.waitForResponse(
      (res) =>
        res.url().includes("/api/workspaces/") &&
        /\/issues(\?|$)/.test(res.url()) &&
        res.status() === 200,
    ),
    page.goto(`/ws/${WS_ID}/kanban?groupBy=none`),
  ]);
  await expect(page.locator('section[data-status="in_progress"]')).toBeVisible();

  // Wait for the agentStore poll so tests that inspect live status don't race.
  await page.waitForResponse(
    (res) =>
      res.url().includes("/api/monitor/agents") && res.status() === 200,
    { timeout: 10000 },
  );
}

test.describe("IssueCard agent row on kanban", () => {
  test.beforeEach(async ({ page }) => {
    await setupMocks(page);
  });

  test("in-progress card with matching agent shows AgentRow with status", async ({
    page,
  }) => {
    await navigateToKanban(page);

    const inProgressColumn = page.locator(
      'section[data-status="in_progress"]',
    );
    const card = inProgressColumn
      .locator("article")
      .filter({ hasText: "In Progress With Agent" });
    await expect(card).toBeVisible();

    const agentRow = card.locator('[data-testid="agent-row"]');
    await expect(agentRow).toBeVisible();
    await expect(agentRow).toContainText("nova");

    // Avatar initial: uppercase of the first letter of the stripped display name.
    const avatar = agentRow.locator('[class*="avatar"]').first();
    await expect(avatar).toHaveText("N");

    // Matching agent in store → live status dot + activity text must render.
    // This is the specific path VAL-FE-KANBAN-023 regressed on.
    const statusDot = agentRow.locator('[class*="statusDot"]');
    await expect(statusDot).toHaveCount(1);
    const activity = agentRow.locator('[class*="activity"]');
    await expect(activity).toBeVisible();
    await expect(activity).toContainText("Working");
  });

  test("in-progress card with assignee but no matching agent shows name only", async ({
    page,
  }) => {
    await navigateToKanban(page);

    const inProgressColumn = page.locator(
      'section[data-status="in_progress"]',
    );
    const card = inProgressColumn
      .locator("article")
      .filter({ hasText: "In Progress No Match" });
    await expect(card).toBeVisible();

    const agentRow = card.locator('[data-testid="agent-row"]');
    await expect(agentRow).toBeVisible();
    await expect(agentRow).toContainText("ghost");

    // No agent in the store for "ghost" — no status dot or activity text
    const statusDot = agentRow.locator('[class*="statusDot"]');
    await expect(statusDot).toHaveCount(0);
    const activity = agentRow.locator('[class*="activity"]');
    await expect(activity).toHaveCount(0);
  });

  test("review card with assignee shows 'Submitted for review' text", async ({
    page,
  }) => {
    await navigateToKanban(page);

    const reviewColumn = page.locator('section[data-status="review"]');
    const card = reviewColumn
      .locator("article")
      .filter({ hasText: "Review With Assignee" });
    await expect(card).toBeVisible();

    const agentRow = card.locator('[data-testid="agent-row"]');
    await expect(agentRow).toBeVisible();
    await expect(agentRow).toContainText("nova");
    await expect(agentRow).toContainText("Submitted for review");

    // Review column uses static text — no live status dot regardless of store
    const statusDot = agentRow.locator('[class*="statusDot"]');
    await expect(statusDot).toHaveCount(0);
  });

  test("in-progress card without assignee does not render AgentRow", async ({
    page,
  }) => {
    await navigateToKanban(page);

    const inProgressColumn = page.locator(
      'section[data-status="in_progress"]',
    );
    const card = inProgressColumn
      .locator("article")
      .filter({ hasText: "In Progress No Assignee" });
    await expect(card).toBeVisible();

    await expect(card.locator('[data-testid="agent-row"]')).toHaveCount(0);
  });

  test("open and done cards do not render AgentRow even with assignee", async ({
    page,
  }) => {
    await navigateToKanban(page);

    const readyColumn = page.locator('section[data-status="ready"]');
    const openCard = readyColumn
      .locator("article")
      .filter({ hasText: "Open With Assignee" });
    await expect(openCard).toBeVisible();
    await expect(openCard.locator('[data-testid="agent-row"]')).toHaveCount(0);

    const doneColumn = page.locator('section[data-status="done"]');
    const doneCard = doneColumn
      .locator("article")
      .filter({ hasText: "Done With Assignee" });
    await expect(doneCard).toBeVisible();
    await expect(doneCard.locator('[data-testid="agent-row"]')).toHaveCount(0);
  });
});

import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E Journey: Review agent code changes (Git/Diff/Files tabs).
 *
 * Tests the full user journey of a lead reviewing what an agent actually did:
 *   1-3. Open agent panel from sidebar, verify Info tab content
 *   4-5. Panel mutual exclusivity (agent → issue → agent)
 *   6.   Git tab renders branch, commits, working tree
 *   7-8. Diff tab renders file list, expand shows diff viewer
 *   9.   Files tab renders file explorer with directory expand
 *   10.  Close panel (X button and Escape key)
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

// -- Agent mock data --

const mockAgent = {
  name: "ember",
  status: "working: loom-103 (30s)",
  branch: "fix-auth-bug",
  path: "/tmp/worktrees/ember",
  repo: "loomcli",
  cross_repo: false,
  ahead: 3,
  behind: 1,
  role: "task",
  commits: [
    { hash: "abc1234", message: "Add auth middleware", url: "" },
    { hash: "def5678", message: "Fix token validation", url: "" },
    { hash: "ghi9012", message: "Add auth tests", url: "" },
  ],
  changes: [
    { path: "src/auth.go", status: "M" },
    { path: "src/auth_test.go", status: "A" },
  ],
};

const mockAgentTask = {
  id: "loom-103",
  title: "Fix authentication bug",
  priority: 1,
};

const mockLoomStatus = {
  agents: [mockAgent],
  tasks: {
    needs_planning: 0,
    ready_to_implement: 1,
    in_progress: 1,
    need_review: 0,
    backlog: 0,
  },
  in_progress_list: [
    { id: "loom-103", title: "Fix authentication bug", priority: 1 },
  ],
  agent_tasks: { ember: mockAgentTask },
  stats: {
    open: 2,
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
    db_last_sync: "2026-01-15T10:00:00Z",
    git_needs_push: 1,
    git_needs_pull: 0,
    git_push_details: [{ name: "ember", count: 3 }],
  },
  timestamp: "2026-01-15T10:00:00Z",
};

const mockLoomTasks = {
  summary: mockLoomStatus.tasks,
  needs_planning: [],
  ready_to_implement: [
    { id: "loom-104", title: "Add rate limiter", priority: 2 },
  ],
  needs_review: [],
  in_progress: [
    { id: "loom-103", title: "Fix authentication bug", priority: 1 },
  ],
  backlog: [],
  closed: [],
  timestamp: "2026-01-15T10:00:00Z",
};

// -- Issue mock data --

const mockIssues = [
  {
    id: "loom-103",
    title: "Fix authentication bug",
    status: "in_progress",
    priority: 1,
    issue_type: "bug",
    assignee: "ember",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
  {
    id: "loom-104",
    title: "Add rate limiter",
    status: "open",
    priority: 2,
    issue_type: "task",
    created_at: "2026-01-15T10:00:00Z",
    updated_at: "2026-01-15T10:00:00Z",
  },
];

const mockIssueDetail = {
  ...mockIssues[0],
  description: "Auth tokens expire prematurely under load",
  design: "",
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
};

// -- Git mock data --

const mockGitStatus = {
  branch: "fix-auth-bug",
  target_branch: "main",
  is_clean: false,
  ahead: 3,
  behind: 1,
  changed_files: ["src/auth.go", "src/auth_test.go"],
  conflicted_files: [],
  has_conflicts: false,
  stash_count: 0,
};

// -- Diff mock data --

const mockDiffCommits = [
  {
    hash: "abc1234567890",
    short_hash: "abc1234",
    subject: "Add auth middleware",
    author: "ember",
    email: "ember@test.com",
    date: new Date(Date.now() - 30 * 60 * 1000).toISOString(),
  },
  {
    hash: "def5678901234",
    short_hash: "def5678",
    subject: "Fix token validation",
    author: "ember",
    email: "ember@test.com",
    date: new Date(Date.now() - 20 * 60 * 1000).toISOString(),
  },
  {
    hash: "ghi9012345678",
    short_hash: "ghi9012",
    subject: "Add auth tests",
    author: "ember",
    email: "ember@test.com",
    date: new Date(Date.now() - 10 * 60 * 1000).toISOString(),
  },
];

const mockDiffFiles = [
  { path: "src/auth.go", status: "M" as const, additions: 45, deletions: 12 },
  {
    path: "src/auth_test.go",
    status: "A" as const,
    additions: 89,
    deletions: 0,
  },
  {
    path: "settings.yml",
    status: "M" as const,
    additions: 3,
    deletions: 1,
  },
];

const mockDiffPatch = {
  patch:
    "@@ -10,6 +10,8 @@ func validateToken(token string) error {\n" +
    '     if token == "" {\n' +
    "         return ErrEmptyToken\n" +
    "     }\n" +
    "+    if len(token) < 32 {\n" +
    "+        return ErrTokenTooShort\n" +
    "+    }\n" +
    "     return nil\n" +
    " }\n",
  is_binary: false,
  is_too_large: false,
  additions: 3,
  deletions: 0,
};

// -- File tree mock data --

const mockFileTreeRoot = {
  path: "",
  entries: [
    { name: "src", is_dir: true, size: 0, mod_time: "2026-01-15T10:00:00Z" },
    {
      name: "README.md",
      is_dir: false,
      size: 512,
      mod_time: "2026-01-15T10:00:00Z",
    },
  ],
};

const mockFileTreeSrc = {
  path: "src",
  entries: [
    {
      name: "auth.go",
      is_dir: false,
      size: 1024,
      mod_time: "2026-01-15T10:00:00Z",
    },
    {
      name: "auth_test.go",
      is_dir: false,
      size: 2048,
      mod_time: "2026-01-15T10:00:00Z",
    },
  ],
};

const mockFileContent = {
  path: "src/auth.go",
  content:
    "package auth\n\nfunc validateToken(token string) error {\n    return nil\n}\n",
  size: 1024,
  binary: false,
};

// -- Helper functions --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

/**
 * Set up all baseline API mocks required for the app to boot.
 */
async function setupBaseMocks(page: Page) {
  // Boot-time mocks: /api/config and /api/auth/token must succeed for app to render
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

  await page.route("**/api/auth/token", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ token: "test-token" }),
    });
  });

  // Workspace metadata
  await page.route("**/api/workspaces/active", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok(mockWorkspaceData),
    });
  });
  await page.route("**/api/workspaces/default", async (route) => {
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

  // Health check
  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  // Monitor catch-all (lowest priority — LIFO)
  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({}),
    });
  });

  // Health endpoint (now served at /health directly)
  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  // Specific monitor endpoints (registered last = highest priority)
  await page.route("**/api/monitor/agents", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agents: [mockAgent],
        timestamp: "2026-01-15T10:00:00Z",
      }),
    });
  });
  await page.route("**/api/monitor/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    });
  });
  await page.route("**/api/workspaces/*/monitor/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomStatus),
    });
  });
  await page.route("**/api/workspaces/*/agents/*/runs*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        agent_id: "ember",
        runs: [],
        sessions: [],
      }),
    });
  });
  await page.route("**/api/monitor/tasks", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockLoomTasks),
    });
  });

  // Stats endpoint
  await page.route("**/workspaces/*/stats", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({
        total_issues: 2,
        open_issues: 1,
        in_progress_issues: 1,
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
  });

  // Blocked endpoint
  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

  // Terminal sessions-by-issue endpoint
  await page.route(
    "**/workspaces/*/terminal/sessions/by-issue",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({}),
      });
    },
  );

  // SSE events endpoint
  await page.route("**/workspaces/*/events**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "text/event-stream",
      headers: {
        "Cache-Control": "no-cache",
        Connection: "keep-alive",
      },
      body: 'event: connected\ndata: {"message":"connected"}\n\n',
    });
  });

  // Issue detail endpoint (for task click navigation)
  await page.route("**/workspaces/*/issues/loom-103", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockIssueDetail),
      });
    } else {
      await route.fallback();
    }
  });
}

/**
 * Set up agent-specific API mocks for git, diff, and files tabs.
 */
async function setupAgentMocks(page: Page) {
  // Git status
  await page.route("**/workspaces/*/agents/ember/git/status", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockGitStatus),
    });
  });

  // Diff commits
  await page.route(
    "**/workspaces/*/agents/ember/diff/commits*",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({ commits: mockDiffCommits }),
      });
    },
  );

  // Diff files
  await page.route(
    "**/workspaces/*/agents/ember/diff/files*",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok({ files: mockDiffFiles }),
      });
    },
  );

  // Diff file (single file patch)
  await page.route(
    "**/workspaces/*/agents/ember/diff/file?*",
    async (route) => {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockDiffPatch),
      });
    },
  );

  // Files tree
  await page.route("**/workspaces/*/files/tree?*", async (route) => {
    const url = new URL(route.request().url());
    const path = url.searchParams.get("path") ?? "";
    if (path === "src") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockFileTreeSrc),
      });
    } else {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: JSON.stringify(mockFileTreeRoot),
      });
    }
  });

  // File content
  await page.route("**/workspaces/*/files?*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify(mockFileContent),
    });
  });

  // Agent terminal WebSocket — abort to prevent hanging
  await page.route(
    "**/workspaces/*/agents/ember/terminal/**",
    async (route) => {
      await route.abort();
    },
  );

  // Agent terminal log archive
  await page.route("**/workspaces/*/agents/ember/logs*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ lines: [], total: 0, has_more: false }),
    });
  });
}

/**
 * Install browser-level fetch interceptor for the issues list endpoint.
 */
async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript((issueData: unknown[]) => {
    (window as any).__mockIssues = issueData;
    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      const url = typeof input === "string" ? input : input.toString();
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
  }, issues);
}

async function navigateAndWaitForBoard(page: Page) {
  await page.goto("/ws/default/", { waitUntil: "domcontentloaded" });
}

// -- Tests --

test.describe("E2E Journey: Review agent code changes", () => {
  test.describe.configure({ mode: "serial" });

  let page: Page;

  test.beforeAll(async ({ browser }) => {
    page = await browser.newPage();
    await installIssuesMock(page, mockIssues);
    await setupBaseMocks(page);
    await setupAgentMocks(page);
    await navigateAndWaitForBoard(page);
  });

  test.afterAll(async () => {
    await page.close();
  });

  test("Step 1-3: Open routed agent detail and verify Runs and Info tabs", async () => {
    const sidebar = page.getByRole("complementary");

    // Wait for agent data to load
    await expect(
      sidebar.getByRole("button", { name: "Agent: ember" }),
    ).toBeVisible({ timeout: 10000 });

    // Click ember agent card in sidebar
    await sidebar.getByRole("button", { name: "Agent: ember" }).click();

    // Agents are route-owned editor surfaces now, not slide-over drawers.
    await expect(page).toHaveURL(/\/ws\/default\/agents\/ember$/);
    const detail = page.getByRole("region", { name: "Agent details" });
    await expect(detail.getByTestId("agent-editor-groups")).toBeVisible();

    // Run history is the default surface and a successful empty response must
    // render the intentional empty state rather than an API error.
    const runsTab = detail.getByRole("button", { name: "Runs" });
    await expect(runsTab).toHaveAttribute("aria-current", "page");
    await expect(detail.getByTestId("agent-history-no-runs")).toBeVisible();
    await expect(detail.getByText(/API Error:/)).toBeHidden();

    // The route keeps the complete review surface available through editor
    // tabs. Verify the monitor-backed identity metadata on Info.
    const infoTab = detail.getByRole("button", { name: "Info" });
    await infoTab.click();
    await expect(infoTab).toHaveAttribute("aria-current", "page");
    const visiblePane = detail.locator('[role="tabpanel"]:visible');
    await expect(
      visiblePane.getByRole("heading", { name: "ember" }),
    ).toBeVisible();
    await expect(visiblePane.getByText("fix-auth-bug")).toBeVisible();
    await expect(visiblePane.getByText("loomcli")).toBeVisible();
  });

  test("Step 4-5: Panel mutual exclusivity (agent → issue → agent)", async () => {
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Click task title link to navigate to issue detail
    const taskLink = panel
      .getByRole("button")
      .filter({ hasText: "Fix authentication bug" });
    await taskLink.first().click();

    // Wait for agent panel to close
    await expect(panel).toHaveAttribute("data-state", "closed");

    // Verify IssueDetailPanel opens with loom-103
    const issuePanel = page.getByTestId("issue-detail-panel");
    await expect(issuePanel).toHaveAttribute("data-state", "open");
    await expect(page.getByTestId("issue-id")).toContainText("loom-103");

    // Verify agent panel is NOT open while issue panel is open (mutual exclusivity)
    await expect(panel).toHaveAttribute("data-state", "closed");

    // Close issue panel to access sidebar again
    await page.keyboard.press("Escape");
    await expect(issuePanel).toHaveAttribute("data-state", "closed");

    // Click ember agent card again in sidebar
    const sidebar = page.getByRole("complementary");
    await sidebar.getByRole("button", { name: "Agent: ember" }).click();

    // Agent panel reopens
    await expect(panel).toHaveAttribute("data-state", "open");
    await expect(panel.locator("h2")).toContainText("ember");
  });

  test("Step 6: Git tab renders commits and status", async () => {
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Click Git tab
    await page.getByRole("tab", { name: "Git" }).click();

    // Verify Git tab is selected
    await expect(page.locator("#agent-panel-tab-git")).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Wait for git tab panel to be visible
    const gitPanel = page.locator("#agent-panel-tabpanel-git");
    await expect(gitPanel).toBeVisible();

    // Verify branch header shows "fix-auth-bug"
    await expect(gitPanel.getByText("fix-auth-bug")).toBeVisible();

    // Verify commit badges
    await expect(gitPanel.getByText("+3 ahead")).toBeVisible();
    await expect(gitPanel.getByText("-1 behind")).toBeVisible();

    // Verify commit list renders commits (from diff commits or agent commits)
    // The GitTab tries to fetch rich diff commits, falling back to agent commits
    await expect(gitPanel.getByText("Add auth middleware")).toBeVisible({
      timeout: 5000,
    });
    await expect(gitPanel.getByText("Fix token validation")).toBeVisible();
    await expect(gitPanel.getByText("Add auth tests")).toBeVisible();

    // Working tree file details are covered by the Diff tab.
  });

  test("Step 7-8: Diff tab renders files, expand shows diff viewer", async () => {
    // Click Diff tab (lazy-loaded)
    await page.getByRole("tab", { name: "Diff" }).click();

    // Verify Diff tab is selected
    await expect(page.locator("#agent-panel-tab-diff")).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Wait for lazy-loaded DiffTab to render (through Suspense)
    const diffPanel = page.locator("#agent-panel-tabpanel-diff");
    await expect(diffPanel).toBeVisible();

    // Wait for diff files to load — summary bar should appear
    await expect(diffPanel.getByText("3 files changed")).toBeVisible({
      timeout: 10000,
    });

    // Verify aggregate stats
    await expect(diffPanel.getByText("+137")).toBeVisible();
    await expect(diffPanel.getByText("-13")).toBeVisible();

    // Verify file rows render with status badges and paths
    await expect(diffPanel.getByText("src/auth.go")).toBeVisible();
    await expect(diffPanel.getByText("src/auth_test.go")).toBeVisible();
    await expect(diffPanel.getByText("settings.yml")).toBeVisible();

    // Click first file row (src/auth.go) to expand
    // DiffFileRow uses <div role="button"> — must use getByRole, not locator("button")
    const firstFileRow = diffPanel
      .getByRole("button")
      .filter({ hasText: "src/auth.go" });
    await firstFileRow.click();

    // Wait for DiffFileViewer to appear with patch content
    // The patch contains validateToken function diff
    await expect(diffPanel.getByText("ErrTokenTooShort")).toBeVisible({
      timeout: 10000,
    });

    // Verify chevron shows expanded state
    await expect(
      diffPanel.locator('[data-expanded="true"]').first(),
    ).toBeVisible();

    // Verify diff lines render with correct types
    await expect(diffPanel.locator('[data-type="add"]').first()).toBeVisible();
  });

  test("Step 9: Files tab renders file explorer", async () => {
    // Click Files tab (lazy-loaded)
    await page.getByRole("tab", { name: "Files" }).click();

    // Verify Files tab is selected
    await expect(page.locator("#agent-panel-tab-files")).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // Wait for lazy-loaded FileEditorPanel to render
    const filesPanel = page.locator("#agent-panel-tabpanel-files");
    await expect(filesPanel).toBeVisible();
    await expect(filesPanel.getByTestId("file-editor-panel")).toBeVisible({
      timeout: 10000,
    });

    // Verify file tree renders root entries: "src" directory and "README.md" file
    await expect(filesPanel.getByText("src")).toBeVisible({ timeout: 5000 });
    await expect(filesPanel.getByText("README.md")).toBeVisible();

    // Click "src" directory to expand
    const srcButton = filesPanel
      .locator("button")
      .filter({ hasText: "src" })
      .first();
    await srcButton.click();

    // Verify children appear: auth.go and auth_test.go
    await expect(filesPanel.getByText("auth.go")).toBeVisible({
      timeout: 5000,
    });
    await expect(filesPanel.getByText("auth_test.go")).toBeVisible();

    // Click auth.go to load file content
    // FileTreeNode renders entry.name in a span — target exact text to avoid matching auth_test.go
    const authGoButton = filesPanel
      .locator("button")
      .filter({ has: page.locator("span", { hasText: /^auth\.go$/ }) })
      .first();
    await authGoButton.click();

    // Verify file content loads in editor pane
    await expect(filesPanel.getByText("src/auth.go")).toBeVisible();
    // Verify "Select a file to edit" is no longer shown
    await expect(
      filesPanel.getByText("Select a file to edit"),
    ).not.toBeVisible();
  });

  test("Step 10: Close panel", async () => {
    const panel = page.getByTestId("agent-detail-panel");
    await expect(panel).toHaveAttribute("data-state", "open");

    // Close via close button
    await panel.getByRole("button", { name: "Close panel" }).click();
    await expect(panel).toHaveAttribute("data-state", "closed");

    // Reopen panel
    const sidebar = page.getByRole("complementary");
    await sidebar.getByRole("button", { name: "Agent: ember" }).click();
    await expect(panel).toHaveAttribute("data-state", "open");

    // Close via Escape key
    await page.keyboard.press("Escape");
    await expect(panel).toHaveAttribute("data-state", "closed");
  });
});

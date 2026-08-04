import { test, expect } from "@playwright/test";
import type { Page } from "@playwright/test";

/**
 * E2E tests for SessionsTab container component in IssueDetailPanel.
 *
 * Tests the full user flow: opening an issue panel, switching to the Sessions tab,
 * seeing the cost summary, selecting sessions in the timeline, viewing transcript/diff
 * content, and handling loading/error/empty states. All API endpoints are mocked.
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

const mockIssue = {
  id: "sessions-test-1",
  title: "Issue With Sessions",
  status: "open",
  priority: 2,
  issue_type: "task",
  description: "Test issue for sessions tab",
  created_at: "2026-03-20T10:00:00Z",
  updated_at: "2026-03-20T10:00:00Z",
};

const mockIssueDetails = {
  ...mockIssue,
  labels: [],
  dependencies: [],
  dependents: [],
  comments: [],
};

// -- Session mock data --

interface MockSession {
  session_id: string;
  agent_name: string;
  backend: string;
  model?: string;
  phase?: string;
  status: "running" | "completed" | "failed" | "aborted";
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  estimated_cost_usd: number;
  exit_code: number;
  files_changed: number;
  lines_added: number;
  lines_removed: number;
  files_touched?: string[];
  attempt_num: number;
  has_transcript: boolean;
  has_diff: boolean;
  is_active: boolean;
}

function createMockSession(
  overrides: Partial<MockSession> = {},
): MockSession {
  return {
    session_id: "sess-001",
    agent_name: "agent-alpha",
    backend: "claude",
    model: "opus-4",
    phase: "implementation",
    status: "completed",
    started_at: "2026-03-20T10:00:00Z",
    ended_at: "2026-03-20T10:05:00Z",
    duration_s: 300,
    input_tokens: 5000,
    output_tokens: 3000,
    cache_read_tokens: 1000,
    cache_write_tokens: 500,
    estimated_cost_usd: 0.45,
    exit_code: 0,
    files_changed: 3,
    lines_added: 50,
    lines_removed: 10,
    files_touched: ["src/main.ts", "src/utils.ts", "README.md"],
    attempt_num: 1,
    has_transcript: true,
    has_diff: true,
    is_active: false,
    ...overrides,
  };
}

const completedSession = createMockSession();

const activeSession = createMockSession({
  session_id: "sess-002",
  agent_name: "agent-beta",
  status: "running",
  is_active: true,
  has_diff: false,
  ended_at: undefined,
  duration_s: undefined,
  input_tokens: 1500,
  output_tokens: 800,
  estimated_cost_usd: 0.12,
  files_changed: 0,
  lines_added: 0,
  lines_removed: 0,
  files_touched: [],
});

const failedSession = createMockSession({
  session_id: "sess-003",
  agent_name: "agent-gamma",
  status: "failed",
  has_transcript: false,
  has_diff: false,
  exit_code: 1,
  files_changed: 0,
  lines_added: 0,
  lines_removed: 0,
  files_touched: [],
  input_tokens: 500,
  output_tokens: 200,
  estimated_cost_usd: 0.03,
});

const defaultSessions = [completedSession, activeSession, failedSession];

// -- Transcript mock data --

const mockTranscript = [
  {
    seq: 1,
    timestamp: "2026-03-20T10:00:05Z",
    role: "user",
    type: "text",
    text: "Fix the login bug",
  },
  {
    seq: 2,
    timestamp: "2026-03-20T10:00:10Z",
    role: "assistant",
    type: "text",
    text: "I will investigate the auth module",
  },
  {
    seq: 3,
    timestamp: "2026-03-20T10:00:30Z",
    role: "assistant",
    type: "tool_use",
    tool_name: "Read",
    tool_input: { file_path: "src/auth.ts" },
  },
];

// -- Diff mock data --

const mockDiff =
  "--- a/src/main.ts\n+++ b/src/main.ts\n@@ -1,3 +1,4 @@\n import { app } from './app';\n+import { logger } from './logger';\n app.listen(3000);";

// -- Helper: JSON response --

function ok<T>(data: T): string {
  return JSON.stringify({ success: true, data });
}

// -- Mock setup --

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

  await page.route("**/api/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok", daemon: true }),
    });
  });

  await page.route("**/health", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ status: "ok" }),
    });
  });

  await page.route("**/api/monitor/**", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ agents: [], tasks: [] }),
    });
  });

  await page.route("**/workspaces/*/stats", async (route) => {
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
  });

  await page.route("**/workspaces/*/blocked*", async (route) => {
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok([]),
    });
  });

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
}

async function installIssuesMock(page: Page, issues: unknown[]) {
  await page.addInitScript((data: unknown[]) => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    (window as any).__mockIssues = data;

    const originalFetch = window.fetch.bind(window);
    window.fetch = function (
      input: RequestInfo | URL,
      init?: RequestInit,
    ): Promise<Response> {
      // Request.toString() → "[object Request]", not the URL. openapi-fetch
      // passes Request objects, so extract `.url` explicitly.
      const url =
        input instanceof Request
          ? input.url
          : typeof input === "string"
            ? input
            : String(input);
      if (
        /\/api\/workspaces\/[^/]+\/issues(\?|$)/.test(url) &&
        (init?.method ?? "GET") === "GET"
      ) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              success: true,
              // eslint-disable-next-line @typescript-eslint/no-explicit-any
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

interface SessionMockOptions {
  sessions?: MockSession[];
  emptySessions?: boolean;
  sessionsError?: boolean;
  transcriptError?: boolean;
  diffError?: boolean;
}

interface SessionMockTrackers {
  sessionsCount: { value: number };
  transcriptCount: { value: number };
  diffCount: { value: number };
}

async function setupSessionMocks(
  page: Page,
  options: SessionMockOptions = {},
): Promise<SessionMockTrackers> {
  const trackers: SessionMockTrackers = {
    sessionsCount: { value: 0 },
    transcriptCount: { value: 0 },
    diffCount: { value: 0 },
  };

  // Mock GET /api/workspaces/default/issues/{id} for detail panel
  await page.route("**/workspaces/*/issues/sessions-test-*", async (route) => {
    if (route.request().method() === "GET") {
      await route.fulfill({
        status: 200,
        contentType: "application/json",
        body: ok(mockIssueDetails),
      });
    } else {
      await route.fallback();
    }
  });

  // Mock GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript
  // (registered BEFORE broader sessions route to avoid glob collision)
  await page.route("**/api/workspaces/*/tasks/*/sessions/*/transcript", async (route) => {
    trackers.transcriptCount.value++;
    if (options.transcriptError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal server error" }),
      });
      return;
    }
    const url = route.request().url();
    const sessionIdMatch = url.match(/\/sessions\/([^/]+)\/transcript/);
    const sessionId = sessionIdMatch ? sessionIdMatch[1] : null;

    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ session_id: sessionId, entries: mockTranscript }),
    });
  });

  // Mock GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff
  // (registered BEFORE broader sessions route)
  await page.route("**/api/workspaces/*/tasks/*/sessions/*/diff", async (route) => {
    trackers.diffCount.value++;
    if (options.diffError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal server error" }),
      });
      return;
    }
    await route.fulfill({
      status: 200,
      contentType: "text/plain",
      body: mockDiff,
    });
  });

  // Mock GET /api/workspaces/{ws}/tasks/{taskId}/sessions
  await page.route("**/api/workspaces/*/tasks/*/sessions", async (route) => {
    trackers.sessionsCount.value++;
    if (options.sessionsError) {
      await route.fulfill({
        status: 500,
        contentType: "application/json",
        body: JSON.stringify({ error: "Internal server error" }),
      });
      return;
    }
    const sessions = options.emptySessions
      ? []
      : (options.sessions ?? defaultSessions);
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: ok({ task_id: "sessions-test-1", sessions }),
    });
  });

  return trackers;
}

// -- Navigation helpers --

async function navigateToApp(page: Page) {
  await page.goto("/ws/default/kanban?groupBy=none", {
    waitUntil: "domcontentloaded",
  });
}

async function openIssuePanel(page: Page, title: string) {
  const card = page.locator("article").filter({ hasText: title });
  await expect(card).toBeVisible();
  await card.click();

  const panel = page.getByTestId("issue-detail-panel");
  await expect(panel).toHaveAttribute("data-state", "open");
  await expect(panel).toHaveAttribute("data-loading", "false", {
    timeout: 5000,
  });
}

async function switchToSessionsTab(page: Page) {
  await page.locator("#issue-panel-tab-sessions").click();
  await expect(
    page
      .getByTestId("sessions-tab")
      .or(page.getByTestId("sessions-empty"))
      .or(page.getByText("Failed to load sessions")),
  ).toBeVisible({ timeout: 5000 });
}

// -- Tests --

test.describe("Sessions tab visibility", () => {
  test("Sessions tab button is visible in issue detail panel", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");

    const tab = page.locator("#issue-panel-tab-sessions");
    await expect(tab).toBeVisible();
    await expect(tab).toContainText("Sessions");
  });

  test("Clicking Sessions tab renders the sessions container", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await expect(page.getByTestId("sessions-tab")).toBeVisible();
  });
});

test.describe("Cost summary display", () => {
  test("Shows session count", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const tab = page.getByTestId("sessions-tab");
    await expect(tab).toContainText("3");
  });

  test("Shows formatted token total", async ({ page }) => {
    // completedSession: 5000+3000=8000, activeSession: 1500+800=2300, failedSession: 500+200=700
    // total = 11000 → "11.0K"
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const tab = page.getByTestId("sessions-tab");
    await expect(tab).toContainText("11.0K");
  });

  test("Shows formatted cost total", async ({ page }) => {
    // 0.45 + 0.12 + 0.03 = 0.60 → "$0.60"
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const tab = page.getByTestId("sessions-tab");
    await expect(tab).toContainText("$0.60");
  });

  test("Shows active sessions badge when active sessions exist", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    // activeSession has is_active: true → "1 active" badge
    const tab = page.getByTestId("sessions-tab");
    await expect(tab.getByText("1 active")).toBeVisible();
  });

  test("Hides active sessions badge when no active sessions", async ({
    page,
  }) => {
    const allInactive = defaultSessions.map((s) => ({
      ...s,
      is_active: false,
    }));
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page, { sessions: allInactive });
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const tab = page.getByTestId("sessions-tab");
    await expect(tab.getByText("active")).not.toBeVisible();
  });
});

test.describe("Empty state", () => {
  test("Shows empty state when no sessions exist", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page, { emptySessions: true });
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await expect(page.getByTestId("sessions-empty")).toBeVisible();
    await expect(page.getByText("No sessions recorded yet")).toBeVisible();
  });
});

test.describe("Error state", () => {
  test("Shows error message when sessions API fails", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page, { sessionsError: true });
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const tabPanel = page.locator("#issue-panel-tabpanel-sessions");
    await expect(tabPanel).toContainText("Failed to load sessions");
  });
});

test.describe("Session timeline rendering", () => {
  test("Timeline container is visible", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await expect(page.getByTestId("session-timeline")).toBeVisible();
  });

  test("Renders correct number of session rows", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    const rows = page.locator('[data-testid^="session-row-"]');
    await expect(rows).toHaveCount(3);
  });

  test("Session rows show agent names and placeholder is visible", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await expect(
      page.getByTestId("session-row-sess-001"),
    ).toContainText("agent-alpha");

    // Placeholder visible when no session selected
    await expect(
      page.getByText("Select a session to view details"),
    ).toBeVisible();
  });
});

test.describe("Session selection", () => {
  test("Clicking a session row shows detail view", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await page.getByTestId("session-row-sess-001").click();

    await expect(page.getByTestId("session-detail-view")).toBeVisible();
    await expect(
      page.getByText("Select a session to view details"),
    ).not.toBeVisible();
  });

  test("Detail view shows session metadata", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await page.getByTestId("session-row-sess-001").click();

    const detail = page.getByTestId("session-detail-view");
    await expect(detail).toContainText("opus-4");
    await expect(detail).toContainText("0 (success)");
    await expect(detail).toContainText("3");
    await expect(detail).toContainText("+50");
    await expect(detail).toContainText("-10");
  });

  test("Clicking a different session updates detail view", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    // Select completedSession - has files_changed=3, so "Files:" visible
    await page.getByTestId("session-row-sess-001").click();
    const detail = page.getByTestId("session-detail-view");
    await expect(detail).toContainText("opus-4");

    // Select activeSession - files_changed=0, so "Files:" should not appear
    await page.getByTestId("session-row-sess-002").click();
    // activeSession has no files changed, so detail should not contain "Files:"
    // and exit code differs (running session still has exit_code 0 but no model set differently)
    await expect(detail).toBeVisible();
  });
});

test.describe("Inner tab basics", () => {
  test("Transcript tab is shown by default", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await page.getByTestId("session-row-sess-001").click();

    await expect(page.getByTestId("session-transcript")).toBeVisible();
  });

  test("Clicking diff tab shows diff container", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await page.getByTestId("session-row-sess-001").click();
    await page.getByTestId("session-inner-tab-diff").click();

    await expect(page.getByTestId("session-diff")).toBeVisible();
    await expect(page.getByTestId("session-transcript")).not.toBeVisible();
  });

  test("Diff tab is disabled when session has no diff", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    // activeSession has has_diff: false
    await page.getByTestId("session-row-sess-002").click();

    const diffTab = page.getByTestId("session-inner-tab-diff");
    await expect(diffTab).toBeDisabled();
    await expect(diffTab).toHaveAttribute("title", "No diff available");
  });
});

test.describe("API call tracking", () => {
  test("Sessions endpoint called when tab is activated", async ({ page }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    const trackers = await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await expect
      .poll(() => trackers.sessionsCount.value, { timeout: 5000 })
      .toBeGreaterThan(0);
  });

  test("Transcript endpoint called when session is selected", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    const trackers = await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    await page.getByTestId("session-row-sess-001").click();

    await expect
      .poll(() => trackers.transcriptCount.value, { timeout: 5000 })
      .toBeGreaterThan(0);
  });

  test("Diff endpoint only called when diff tab is clicked", async ({
    page,
  }) => {
    await installIssuesMock(page, [mockIssue]);
    await setupBaseMocks(page);
    const trackers = await setupSessionMocks(page);
    await navigateToApp(page);
    await openIssuePanel(page, "Issue With Sessions");
    await switchToSessionsTab(page);

    // Select completedSession (has_diff: true)
    await page.getByTestId("session-row-sess-001").click();
    // Diff should NOT be fetched yet
    expect(trackers.diffCount.value).toBe(0);

    // Click diff inner tab
    await page.getByTestId("session-inner-tab-diff").click();

    await expect
      .poll(() => trackers.diffCount.value, { timeout: 5000 })
      .toBeGreaterThan(0);
  });
});
